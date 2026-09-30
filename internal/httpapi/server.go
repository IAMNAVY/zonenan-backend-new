package httpapi

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"zonenan-backend/internal/adminauth"
	"zonenan-backend/internal/afdian"
	"zonenan-backend/internal/auth"
	"zonenan-backend/internal/config"
	"zonenan-backend/internal/db"
	"zonenan-backend/internal/ids"
	"zonenan-backend/internal/mailer"
	"zonenan-backend/internal/maptiles"
	"zonenan-backend/internal/merchantauth"
	"zonenan-backend/internal/pushnotify"
	"zonenan-backend/internal/store"
)

// Server 持有依赖并暴露 HTTP 路由。
type Server struct {
	cfg                *config.Config
	pool               *db.Pool
	tokens             *auth.TokenManager
	webTokens          *auth.TokenManager
	webSessions        *store.WebSessionStore
	users              *store.UserStore
	grades             *store.GradeStore
	setting            *store.SettingStore
	codes              *store.EmailCodeStore
	legal              *store.LegalStore
	passkeys           *store.PasskeyStore
	announce           *store.AnnouncementStore
	ads                *store.HomeAdStore
	beta               *store.BetaMembershipStore
	releaseBeta        *store.ReleaseBetaMembershipStore
	releases           *store.AppReleaseStore
	artifact           *store.APKArtifactVerifier
	classroomData      *store.ClassroomArtifactStore
	classroomArtifact  *store.SQLiteArtifactVerifier
	campusMap          *store.CampusMapPlaceStore
	campusMapContrib   *store.CampusMapContributionStore
	campusMapIncidents *store.CampusMapIncidentStore
	push               *store.PushNotificationStore
	pushDispatcher     *pushnotify.Dispatcher
	devices            *store.TrustedDeviceStore
	challenges         *store.DeviceLoginChallengeStore
	telemetry          *store.TelemetryStore
	analytics          *store.AnalyticsStore
	membership         *store.MembershipStore
	afdian             *afdian.Client
	mail               mailer.Mailer
	ids                *ids.Verifier // S1:IDS 学号回验
	mapTiles           *maptiles.Service
	adminAccounts      *adminauth.Store
	merchantAccounts   *merchantauth.Store
}

// New 组装 Server。
func New(cfg *config.Config, pool *db.Pool) *Server {
	pushStore := store.NewPushNotificationStore(pool)
	return &Server{
		cfg:                cfg,
		pool:               pool,
		tokens:             auth.NewTokenManager(cfg.JWTSigningKey, cfg.JWTExpire),
		webTokens:          auth.NewTokenManager(cfg.JWTSigningKey, cfg.WebAccessExpire),
		webSessions:        store.NewWebSessionStore(pool),
		users:              store.NewUserStore(pool),
		grades:             store.NewGradeStore(pool),
		setting:            store.NewSettingStore(pool),
		codes:              store.NewEmailCodeStore(pool),
		legal:              store.NewLegalStore(pool),
		passkeys:           store.NewPasskeyStore(pool),
		announce:           store.NewAnnouncementStore(pool),
		ads:                store.NewHomeAdStore(pool),
		beta:               store.NewBetaMembershipStore(pool),
		releaseBeta:        store.NewReleaseBetaMembershipStore(pool),
		releases:           store.NewAppReleaseStore(pool),
		artifact:           store.NewAPKArtifactVerifier(store.ArtifactURLPolicy{AllowedHosts: cfg.ArtifactAllowedHosts, MaxBytes: cfg.ArtifactMaxBytes, Timeout: cfg.ArtifactTimeout}),
		classroomData:      store.NewClassroomArtifactStore(pool),
		classroomArtifact:  store.NewSQLiteArtifactVerifier(store.ArtifactURLPolicy{AllowedHosts: cfg.ArtifactAllowedHosts, MaxBytes: cfg.ArtifactMaxBytes, Timeout: cfg.ArtifactTimeout}),
		campusMap:          store.NewCampusMapPlaceStore(pool),
		campusMapContrib:   store.NewCampusMapContributionStore(pool),
		campusMapIncidents: store.NewCampusMapIncidentStore(pool),
		push:               pushStore,
		pushDispatcher:     pushnotify.New(cfg.FCMServiceAccountJSON, pushStore),
		devices:            store.NewTrustedDeviceStore(pool),
		challenges:         store.NewDeviceLoginChallengeStore(pool),
		telemetry:          store.NewTelemetryStore(pool),
		analytics:          store.NewAnalyticsStore(pool),
		membership:         store.NewMembershipStore(pool, cfg.MembershipBindingOffset, cfg.MembershipActivationPepper),
		afdian:             afdian.NewClient(cfg.AfdianAPIBase, cfg.AfdianAPIUserID, cfg.AfdianAPIToken),
		mail:               mailer.New(),
		ids:                ids.NewVerifierWithCASService(cfg.IDSAuthServer, cfg.IDSAppID, cfg.IDSAppSecret, cfg.CASServiceURL),
		mapTiles: maptiles.New(maptiles.Config{
			Key:              cfg.MapTileKey,
			UpstreamEnabled:  cfg.MapTileUpstream,
			UploadSigningKey: cfg.JWTSigningKey,
			CacheDir:         cfg.MapTileCacheDir,
			UpstreamParallel: cfg.MapTileParallel,
			CacheMaxBytes:    cfg.MapTileMaxBytes,
			CacheTrimToBytes: cfg.MapTileTrimBytes,
		}),
		adminAccounts:    adminauth.NewStore(pool),
		merchantAccounts: merchantauth.NewStore(pool),
	}
}

// Router 构建 chi 路由树。
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.corsMiddleware)
	r.Use(trustedRealIPMiddleware)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	globalRL := newRateLimiter(240, time.Minute)
	r.Use(func(next http.Handler) http.Handler {
		limited := globalRL.middleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			// 管理端由 ADMIN_SECRET 与部署层 Cloudflare Access 保护；不能与
			// App 公共接口共用按 IP 限流，否则 Cloudflare 出口 IP 会让所有
			// 管理请求共享额度，并在刷新后表现为无法重新登录。
			if strings.HasPrefix(request.URL.Path, "/admin/") || strings.HasPrefix(request.URL.Path, "/map/tiles/") {
				next.ServeHTTP(w, request)
				return
			}
			limited.ServeHTTP(w, request)
		})
	})
	s.mountMerchantV1(r)
	r.Handle("/rental-media/*", http.StripPrefix("/rental-media/", http.FileServer(http.Dir(filepath.Join(s.cfg.MerchantUploadDir, "rental")))))

	// H4:敏感端点独立更严限流(按 IP),叠加在全局限流之上。
	sendCodeRL := newRateLimiter(3, 10*time.Minute)         // 发验证码:防邮件轰炸/刷账单
	loginRL := newRateLimiter(10, time.Minute)              // 登录:防撞库
	registerRL := newRateLimiter(5, time.Hour)              // 注册:防批量开号(配合 H3)
	analyticsRL := newRateLimiter(60, time.Minute)          // 分析批量上报:限制异常客户端
	mapContributionRL := newRateLimiter(30, time.Minute)    // 只拦脚本突发，不限制每日贡献总量
	mapIncidentWriteRL := newRateLimiter(60, time.Minute)   // 临时事件上报、确认和撤销
	challengePollRL := newRateLimiter(120, time.Minute)     // 新设备轮询状态
	challengeActionRL := newRateLimiter(30, time.Minute)    // 设备批准/拒绝/完成
	afdianWebhookRL := newRateLimiter(60, time.Minute)      // 爱发电回调防滥用
	membershipActivateRL := newRateLimiter(10, time.Minute) // 激活码防撞库

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { OK(w, map[string]string{"status": "ok"}) })
	r.Get("/web/config", s.handleWebConfig)

	// 公开:长沙五区天地图瓦片。后端按需回源并持久缓存，不按用户计次。
	// GET 与 PUT 均不挂通用 IP 限流：一次正常的地图拖动会并发请求大量瓦片，
	// 而校园 NAT 下多个用户还会共享 IP。PUT 由短时、坐标绑定的签名凭证及
	// ServeUpload 内的区域、体积、图片尺寸和不可覆盖校验保护。
	r.Get("/map/tiles/{layer}/{z}/{x}/{y}.png", s.mapTiles.ServeTile)
	r.Put("/map/tiles/{layer}/{z}/{x}/{y}.png", s.mapTiles.ServeUpload)

	// Digital Asset Links (Android Passkey 验证).
	r.Get("/.well-known/assetlinks.json", s.handleAssetLinks)
	r.Get("/.well-known/webauthn", s.handleWebAuthnRelatedOrigins)

	// 公开:版本检查(热更新)。
	r.With(s.optionalAuthMiddleware).Get("/app/version", s.handleAppVersion)
	r.With(s.optionalAuthMiddleware).Get("/app/changelog", s.handleAppChangelog)

	// 公开:空教室离线数据包版本清单。数据包本身由客户端直连 R2 下载。
	r.Get("/classroom-data/manifest", s.handleClassroomManifest)

	// Phase 6/7 public discovery and authenticated rental operations.
	r.Get("/app/pois", s.handleAppPOIs)
	r.Get("/app/pois/manifest", s.handlePOIManifest)
	r.With(s.authMiddleware).Post("/app/pois/contributions", s.handlePOIContribution)
	r.Get("/app/rentals", s.handleAppRentals)
	r.Get("/app/food/recommendations", s.handleFoodRecommendations)
	r.With(s.optionalAuthMiddleware).Post("/app/merchants/{id}/events", s.handleMerchantEvent)
	r.With(s.authMiddleware).Post("/app/merchants/{id}/favorite", s.handleMerchantFavorite)
	r.With(s.authMiddleware).Delete("/app/merchants/{id}/favorite", s.handleMerchantFavorite)
	r.Get("/app/rentals/{id}", s.handleAppRentalDetail)
	r.With(s.authMiddleware).Get("/app/rentals/mine", s.handleMyRentals)
	r.With(s.authMiddleware).Post("/app/rentals", s.handleMyRentals)
	r.With(s.authMiddleware).Post("/app/rentals/media", s.handleRentalMedia)
	r.With(s.authMiddleware).Put("/app/rentals/{id}", s.handleMyRentals)
	r.With(s.authMiddleware).Delete("/app/rentals/{id}", s.handleMyRentals)
	r.With(s.authMiddleware).Post("/app/rentals/{id}/favorite", s.handleRentalFavorite)
	r.With(s.authMiddleware).Delete("/app/rentals/{id}/favorite", s.handleRentalFavorite)
	r.With(s.authMiddleware).Post("/app/rentals/{id}/report", s.handleRentalReport)
	r.With(s.authMiddleware).Post("/app/rentals/{id}/contact", s.handleRentalContact)
	r.With(s.authMiddleware).Post("/app/rentals/{id}/renew", s.handleRentalRenew)

	// 公开:校园地图正式地点。校区元数据由客户端内置，地点由后端维护。
	r.Get("/campus-map/manifest", s.handleCampusMapManifest)
	r.Get("/campus-map/places", s.handleCampusMapPlaces)
	r.With(s.authMiddleware, mapContributionRL.middlewareByUser).Post("/campus-map/contributions", s.handleCampusMapContribution)
	r.Get("/campus-map/incidents/policy", s.handleCampusMapIncidentPolicy)
	r.With(s.optionalAuthMiddleware).Get("/campus-map/incidents", s.handleCampusMapIncidents)
	r.With(s.authMiddleware, mapIncidentWriteRL.middlewareByUser).Post("/campus-map/incidents", s.handleSubmitCampusMapIncident)
	r.With(s.authMiddleware, mapIncidentWriteRL.middlewareByUser).Put("/campus-map/incidents/{incidentID}/vote", s.handleVoteCampusMapIncident)
	r.With(s.authMiddleware).Get("/campus-map/incidents/mine", s.handleMyCampusMapIncidentReports)
	r.With(s.authMiddleware, mapIncidentWriteRL.middlewareByUser).Delete("/campus-map/incidents/reports/{reportID}", s.handleWithdrawCampusMapIncidentReport)

	// Authenticated device registration and per-device topic preferences.
	r.With(s.authMiddleware).Put("/push/devices", s.handleRegisterPushDevice)
	r.With(s.authMiddleware).Delete("/push/devices/{installationID}", s.handleDeletePushDevice)
	r.With(s.authMiddleware).Get("/push/preferences", s.handlePushPreferences)
	r.Get("/push/poll", s.handlePollPushMessages)
	r.Post("/push/poll/ack", s.handleAcknowledgePushMessages)

	// 爱发电 Webhook 不使用 ZoneNaN JWT；依靠不可猜测的路径密钥和订单幂等。
	r.With(afdianWebhookRL.middleware).Post("/webhooks/afdian/{secret}", s.handleAfdianWebhook)

	// 公开:在线协议文档(用户协议/隐私政策/给分授权说明)。
	r.Get("/legal/versions", s.handleLegalVersions)
	r.Get("/legal/{docType}", s.handleLegalDoc)

	// 公开:运营内容(公告 / 广告位)。
	r.With(s.optionalAuthMiddleware).Get("/announcements", s.handleAnnouncements)
	r.With(s.optionalAuthMiddleware).Get("/home/ads", s.handleHomeAds)

	// 遥测上报(登录可选:带 Bearer 关联账号,否则仅设备/IP)。
	r.Group(func(r chi.Router) {
		r.Use(s.optionalAuthMiddleware)
		r.Post("/telemetry/event", s.handleTelemetryEvent)
		r.Post("/telemetry/crash", s.handleCrashReport)
	})

	// 隐私分析批量上报(登录可选,服务端仅保存 HMAC 后的标识)。
	r.With(analyticsRL.middleware, s.optionalAuthMiddleware).Post("/analytics/events", s.handleAnalyticsEvents)

	// 认证。
	r.Route("/auth", func(r chi.Router) {
		r.With(sendCodeRL.middleware).Post("/send-code", s.handleSendCode) // 邮箱验证码
		r.With(registerRL.middleware).Post("/register", s.handleRegister)  // 邮箱注册
		r.With(loginRL.middleware).Post("/login", s.handleLogin)           // 邮箱登录
		r.With(loginRL.middleware).Post("/refresh", s.handleWebRefresh)
		r.Post("/web-logout", s.handleWebLogout)
		// S5:找回密码(发 reset 验证码 + 重设)。
		r.With(sendCodeRL.middleware).Post("/send-reset-code", s.handleSendResetCode)
		r.With(loginRL.middleware).Post("/reset-password", s.handleResetPassword)
		// cas-login:可带 Bearer(绑到当前账号)也可不带(自动开户/登入)。
		// 客户端在登录/进前台/补绑定时会自动幂等调用,走 loginRL(10/分钟);
		// 不能挂 registerRL(5/小时),正常 App 流程一小时内就会撞 429。
		r.With(loginRL.middleware, s.optionalAuthMiddleware).Post("/cas-login", s.handleCasLogin)
		// Legacy email verification remains available for older clients.
		r.With(loginRL.middleware).Post("/cas-device-verify", s.handleCasDeviceVerify)
		// Challenge credentials are required on every unauthenticated new-device action.
		r.With(sendCodeRL.middleware).Post("/device-challenge/email/start", s.handleDeviceChallengeEmailStart)
		r.With(loginRL.middleware).Post("/device-challenge/email/verify", s.handleDeviceChallengeEmailVerify)
		r.With(challengePollRL.middleware).Post("/device-challenge/status", s.handleDeviceChallengeStatus)
		r.With(challengeActionRL.middleware).Post("/device-challenge/finish", s.handleDeviceChallengeFinish)
		// Passkey 登录(无需已登录)。H5:免登录认证入口,挂 loginRL 防爆破。
		r.With(loginRL.middleware).Post("/passkey/login-begin", s.handlePasskeyLoginBegin)
		r.With(loginRL.middleware).Post("/passkey/login-finish", s.handlePasskeyLoginFinish)
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)
			r.Get("/me", s.handleMe)
			r.Post("/profile", s.handleUpdateProfile)
			r.Post("/logout", s.handleLogout)
			r.Post("/bind-email", s.handleBindEmail)
			r.Post("/change-password", s.handleChangePassword) // S5:登录态改密
			// 可信设备自管及新设备批准。
			r.Get("/devices", s.handleListDevices)
			r.Post("/devices/revoke", s.handleRevokeDevice)
			r.With(challengeActionRL.middleware).Get("/device-challenges/pending", s.handleListPendingDeviceChallenges)
			r.With(challengeActionRL.middleware).Post("/device-challenges/approve", s.handleApproveDeviceChallenge)
			r.With(challengeActionRL.middleware).Post("/device-challenges/reject", s.handleRejectDeviceChallenge)
			// Passkey 注册/管理(需已登录)。
			r.Post("/passkey/register-begin", s.handlePasskeyRegisterBegin)
			r.Post("/passkey/register-finish", s.handlePasskeyRegisterFinish)
			r.Get("/passkey/list", s.handlePasskeyList)
			r.Post("/passkey/delete", s.handlePasskeyDelete)
		})
	})

	// 会员(需登录):权益只由服务端当前 JWT 用户决定。
	r.Route("/membership", func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Get("/me", s.handleMembershipMe)
		r.With(membershipActivateRL.middleware).Post("/activate", s.handleMembershipActivate)
	})

	// 给分(需登录)。
	r.Route("/grade", func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Post("/authorize", s.handleGradeAuthorize)
		r.Post("/revoke", s.handleGradeRevoke)
		r.Get("/auth-status", s.handleGradeAuthStatus)
		r.Post("/sync", s.handleGradeSync)
		r.Get("/semesters", s.handleGradeSemesters)
		r.Get("/suggest", s.handleGradeSuggest)
		r.Post("/search", s.handleGradeSearch)
		r.Get("/detail/{courseId}/{semester}", s.handleGradeDetail)
		r.Post("/evaluate", s.handleGradeEvaluate)
		r.Post("/evaluate/delete", s.handleGradeEvaluateDelete)
		r.Post("/evaluate/update", s.handleGradeEvaluateUpdate)
		r.Get("/mine", s.handleGradeMine)
		r.Post("/favorite", s.handleGradeFavorite)
		r.Get("/favorites", s.handleGradeFavorites)
		r.Get("/my-evaluations", s.handleGradeMyEvaluations)
		r.Get("/my-ranking", s.handleGradeMyRanking)
	})

	// 公开接口。
	r.Get("/app/download-url", s.handleAppDownloadURL)

	// Internal administration API: independent cookie session, CSRF, RBAC and
	// audit. The legacy /panel and /admin surfaces are permanently retired below.
	r.Route("/admin-api/v1", func(r chi.Router) {
		r.With(loginRL.middleware).Post("/auth/login", s.handleAdminV1Login)
		r.Group(func(r chi.Router) {
			r.Use(s.adminV1Auth)
			r.Get("/auth/me", s.handleAdminV1Me)
			r.Post("/auth/logout", s.handleAdminV1Logout)
			r.With(loginRL.middleware).Post("/auth/password", s.handleAdminPassword)
			r.With(requireAdminPermission("audit.read")).Get("/audit-logs", s.handleAdminV1Audit)
			r.With(requireAdminPermission("admin.read")).Get("/admins", s.handleAdminV1Admins)
			r.With(requireAdminPermission("admin.read")).Get("/roles", s.handleAdminV1Roles)
			r.With(requireAdminPermission("admin.read")).Get("/permissions", s.handleAdminV1Permissions)
			r.With(requireAdminPermission("admin.manage")).Post("/admins", s.handleAdminV1CreateAdmin)
			r.With(requireAdminPermission("admin.manage")).Post("/roles", s.handleAdminV1SaveRole)
			r.With(requireAdminPermission("admin.manage")).Put("/roles", s.handleAdminV1SaveRole)
			r.With(requireAdminPermission("admin.manage")).Put("/admins/roles", s.handleAdminV1SetRoles)
			mount := func(method, path, permission string, handler http.HandlerFunc) {
				r.With(requireAdminPermission(permission)).Method(method, path, s.wrapAdminV1(method, path, handler))
			}
			mountNative := func(method, path, permission string, handler http.HandlerFunc) {
				var mounted http.Handler = handler
				if method != http.MethodGet {
					mounted = s.adminV1Audit(adminAction(method, path), adminResource(path), handler)
				}
				r.With(requireAdminPermission(permission)).Method(method, path, mounted)
			}
			mountNative(http.MethodGet, "/merchants", "merchant.read", s.handleAdminMerchants)
			mountNative(http.MethodPost, "/merchants", "merchant.manage", s.handleAdminCreateMerchant)
			mountNative(http.MethodPost, "/merchants/review", "merchant.review", s.handleAdminReviewMerchant)
			mountNative(http.MethodGet, "/merchant-claims", "merchant.read", s.handleAdminMerchantClaims)
			mountNative(http.MethodPost, "/merchant-claims/review", "merchant.review", s.handleAdminReviewMerchantClaim)
			mountNative(http.MethodGet, "/merchant-promotions", "merchant.read", s.handleAdminMerchantPromotions)
			mountNative(http.MethodPost, "/merchant-promotions/review", "merchant.review", s.handleAdminReviewMerchantPromotion)
			mountNative(http.MethodGet, "/merchant-stores", "merchant.read", s.handleAdminMerchantStores)
			mountNative(http.MethodPost, "/merchant-stores/review", "merchant.review", s.handleAdminReviewMerchantStore)
			mountNative(http.MethodGet, "/merchant-apartments", "merchant.read", s.handleAdminMerchantApartments)
			mountNative(http.MethodPost, "/merchant-apartments/review", "merchant.review", s.handleAdminReviewMerchantApartment)
			mountNative(http.MethodPut, "/merchant-settings", "merchant.manage", s.handleAdminMerchantSettings)
			mountNative(http.MethodGet, "/pois", "poi.read", s.handleAdminPOIs)
			mountNative(http.MethodPost, "/pois", "poi.manage", s.handleAdminPOIs)
			mountNative(http.MethodPut, "/pois", "poi.manage", s.handleAdminPOIs)
			mountNative(http.MethodGet, "/poi-contributions", "poi.read", s.handleAdminPOIContributions)
			mountNative(http.MethodPost, "/poi-contributions/review", "poi.review", s.handleAdminPOIContributions)
			mountNative(http.MethodGet, "/rentals", "rental.read", s.handleAdminRentals)
			mountNative(http.MethodPost, "/rentals/review", "rental.review", s.handleAdminRentalReview)
			mountNative(http.MethodGet, "/rental-reports", "rental.read", s.handleAdminRentalReports)
			mountNative(http.MethodPost, "/rental-reports", "rental.manage", s.handleAdminRentalReports)
			mountNative(http.MethodGet, "/rental-stats", "rental.read", s.handleAdminRentalStats)
			mountNative(http.MethodGet, "/merchant-analytics", "merchant.analytics", s.handleAdminMerchantAnalytics)
			mountNative(http.MethodGet, "/jobs", "jobs.read", s.handleAdminJobs)
			mount(http.MethodGet, "/dashboard/grade-stats", "dashboard.read", s.handleAdminGradeStats)
			mount(http.MethodGet, "/analytics/summary", "analytics.read", s.handleAdminAnalyticsSummary)
			mount(http.MethodGet, "/analytics/trends", "analytics.read", s.handleAdminAnalyticsTrends)
			mount(http.MethodGet, "/analytics/exclusions", "analytics.read", s.handleAdminAnalyticsExclusions)
			mount(http.MethodPost, "/analytics/exclusions", "analytics.read", s.handleAdminAnalyticsExclusions)
			mount(http.MethodGet, "/users", "user.read", s.handleAdminUsers)
			mount(http.MethodPost, "/users/ban", "user.ban", s.handleAdminBanUser)
			mount(http.MethodDelete, "/users", "user.delete", s.handleAdminDeleteUser)
			mount(http.MethodPost, "/users/role", "admin.manage", s.handleAdminSetUserRole)
			mount(http.MethodGet, "/beta", "content.read", s.handleAdminBeta)
			mount(http.MethodPost, "/beta", "content.edit", s.handleAdminSetBeta)
			mount(http.MethodDelete, "/beta", "content.edit", s.handleAdminDeleteBeta)
			mount(http.MethodGet, "/crash-reports", "crash.read", s.handleAdminCrashReports)
			mount(http.MethodDelete, "/crash-reports", "crash.delete", s.handleAdminDeleteCrashReports)
			mount(http.MethodGet, "/settings", "config.read", s.handleAdminGetSettings)
			mount(http.MethodPost, "/settings", "config.edit", s.handleAdminSetSetting)
			mount(http.MethodGet, "/legal", "content.read", s.handleAdminGetLegalDocs)
			mount(http.MethodPost, "/legal", "content.edit", s.handleAdminUpdateLegalDoc)
			mount(http.MethodPost, "/legal/edit", "content.edit", s.handleAdminEditLegalDoc)
			mount(http.MethodGet, "/announcements", "content.read", s.handleAdminAnnouncements)
			mount(http.MethodPost, "/announcements", "content.edit", s.handleAdminSaveAnnouncement)
			mount(http.MethodDelete, "/announcements", "content.edit", s.handleAdminDeleteAnnouncement)
			mount(http.MethodGet, "/ads", "content.read", s.handleAdminAds)
			mount(http.MethodPost, "/ads", "content.edit", s.handleAdminSaveAd)
			mount(http.MethodDelete, "/ads", "content.edit", s.handleAdminDeleteAd)
			mount(http.MethodGet, "/evaluations", "content.read", s.handleAdminEvaluations)
			mount(http.MethodPost, "/evaluations/hide", "content.edit", s.handleAdminHideEvaluation)
			mount(http.MethodGet, "/risk/flags", "risk.read", s.handleAdminRiskFlags)
			mount(http.MethodPost, "/risk/resolve", "risk.manage", s.handleAdminResolveRiskFlag)
			mount(http.MethodPost, "/risk/block-device", "risk.manage", s.handleAdminBlockDevice)
			mount(http.MethodGet, "/releases", "release.read", s.handleAdminReleases)
			mount(http.MethodPost, "/releases", "release.manage", s.handleAdminSaveRelease)
			mount(http.MethodPost, "/releases/whats-new", "release.manage", s.handleAdminSaveWhatsNew)
			mount(http.MethodPost, "/releases/backfill", "release.manage", s.handleAdminBackfillRelease)
			mount(http.MethodPost, "/releases/inspect-apk", "release.manage", s.handleAdminInspectAPK)
			mount(http.MethodPost, "/releases/archive", "release.manage", s.handleAdminArchiveRelease)
			mount(http.MethodPost, "/releases/restore", "release.manage", s.handleAdminRestoreRelease)
			mount(http.MethodDelete, "/releases", "release.manage", s.handleAdminDeleteRelease)
			mount(http.MethodGet, "/release-beta", "release.read", s.handleAdminReleaseBeta)
			mount(http.MethodPost, "/release-beta", "release.manage", s.handleAdminSetReleaseBeta)
			mount(http.MethodDelete, "/release-beta", "release.manage", s.handleAdminDeleteReleaseBeta)
			mount(http.MethodGet, "/classroom-data", "classroom.read", s.handleAdminClassroomData)
			mount(http.MethodPost, "/classroom-data", "classroom.manage", s.handleAdminSaveClassroomData)
			mount(http.MethodDelete, "/classroom-data", "classroom.manage", s.handleAdminArchiveClassroomData)
			mount(http.MethodGet, "/campus-map/places", "map.read", s.handleAdminCampusMapPlaces)
			mount(http.MethodPost, "/campus-map/places", "map.edit", s.handleAdminSaveCampusMapPlace)
			mount(http.MethodDelete, "/campus-map/places", "map.edit", s.handleAdminDeactivateCampusMapPlace)
			mount(http.MethodGet, "/campus-map/contributions", "map.read", s.handleAdminCampusMapContributions)
			mount(http.MethodPost, "/campus-map/contributions/review", "map.review", s.handleAdminReviewCampusMapContribution)
			mount(http.MethodGet, "/campus-map/incidents", "map.read", s.handleAdminCampusMapIncidents)
			mount(http.MethodPost, "/campus-map/incidents/remove", "map.edit", s.handleAdminRemoveCampusMapIncident)
			mount(http.MethodGet, "/campus-map/incidents/policy", "map.read", s.handleAdminCampusMapIncidentPolicy)
			mount(http.MethodPost, "/campus-map/incidents/policy", "map.edit", s.handleAdminSetCampusMapIncidentPolicy)
			mount(http.MethodPost, "/push/messages", "content.edit", s.handleAdminPushMessage)
			mount(http.MethodGet, "/push/messages", "content.edit", s.handleAdminPushMessages)
			mount(http.MethodDelete, "/push/messages/{messageID}", "content.edit", s.handleAdminDeletePushMessage)
			mount(http.MethodGet, "/memberships/overview", "premium.read", s.handleAdminMembershipOverview)
			mount(http.MethodGet, "/memberships/types", "premium.read", s.handleAdminMembershipTypes)
			mount(http.MethodPost, "/memberships/types", "premium.manage", s.handleAdminSaveMembershipType)
			mount(http.MethodGet, "/memberships/members", "premium.read", s.handleAdminMembershipMembers)
			mount(http.MethodGet, "/memberships/events", "premium.read", s.handleAdminMembershipEvents)
			mount(http.MethodGet, "/memberships/activation-codes", "premium.read", s.handleAdminMembershipActivationCodes)
			mount(http.MethodPost, "/memberships/activation-codes", "premium.manage", s.handleAdminGenerateActivationCode)
			mount(http.MethodPost, "/memberships/activation-codes/disable", "premium.manage", s.handleAdminDisableActivationCode)
			mount(http.MethodDelete, "/memberships/activation-codes", "premium.manage", s.handleAdminDeleteActivationCode)
			mount(http.MethodPost, "/memberships/grants/expire", "premium.manage", s.handleAdminMembershipGrantExpiry)
			mount(http.MethodPost, "/memberships/grants/revoke", "premium.manage", s.handleAdminRevokeMembershipGrant)
			mount(http.MethodPost, "/memberships/grant", "premium.manage", s.handleAdminMembershipGrant)
			mount(http.MethodPost, "/memberships/revoke", "premium.manage", s.handleAdminMembershipRevoke)
			mount(http.MethodPost, "/memberships/afdian-sync", "premium.manage", s.handleAdminAfdianSync)
		})
	})

	// The secret-based legacy management surface is permanently retired.
	retired := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v1Error(w, r, http.StatusGone, "LEGACY_ADMIN_RETIRED", "旧管理入口已下线，请使用新管理后台")
	})
	for _, path := range []string{"/panel", "/panel/*", "/admin", "/admin/*"} {
		r.Handle(path, retired)
	}

	return r
}
