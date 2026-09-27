package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration, loaded from environment.
type Config struct {
	Port                   string
	DatabaseURL            string
	JWTSigningKey          string        // Zonenan 会话 JWT 签名密钥(全新生成,与教务无关)
	JWTExpire              time.Duration // Zonenan 会话有效期
	GradePepper            string        // = 旧后端 JWT_SECRET,保成绩 hash 连续性;仅服务端持有
	AuthExpire             time.Duration // 给分授权有效期(默认3天)
	AdminSecret            string        // 管理后台鉴权密钥(无默认值,缺失即启动失败)
	AdminBootstrapEmail    string
	AdminBootstrapPassword string
	AdminBootstrapName     string
	AdminSessionTTL        time.Duration
	MerchantSessionTTL     time.Duration
	MerchantUploadDir      string
	AnalyticsPepper        string   // 分析安装/用户标识 HMAC 密钥(仅服务端持有)
	MapTileKey             string   // 天地图服务端 Key,仅用于后端回源
	MapTileCacheDir        string   // 共享瓦片持久缓存目录
	MapTileParallel        int      // 同时回源天地图的最大请求数
	MapTileMaxBytes        int64    // 共享瓦片缓存硬上限
	MapTileTrimBytes       int64    // 超限后清理到该水位
	MapTileUpstream        bool     // 是否允许服务器直接回源;境外部署默认关闭
	WebAllowedOrigins      []string // 允许调用 API 的 Web Origin；无 Origin 的 App 请求不受影响
	TiandituWebKey         string   // 浏览器专用、受域名白名单保护的天地图 Key
	WebAccessExpire        time.Duration
	WebSessionShort        time.Duration
	WebSessionLong         time.Duration
	PasskeyRPID            string
	PasskeyRelatedOrigins  []string
	// Public APK verification policy. Hosts are optional; when set, every
	// redirect destination must match one of them.
	ArtifactAllowedHosts []string
	ArtifactMaxBytes     int64
	ArtifactTimeout      time.Duration
	Env                  string

	// S1:IDS(统一身份)回验配置,用 ids_token 向学校换真实学号,防 cas-login 抢注。
	// 这几个值逆向自学校官方 App、非敏感密钥,但走 env 便于学校轮换时不改代码。
	IDSAuthServer string
	IDSAppID      string
	IDSAppSecret  string

	// CAS service used for server-side ticket validation. It must match the
	// service value used when the client requests the one-time ticket.
	CASServiceURL string

	// Membership/Afdian integration. Secrets remain server-side; the app only
	// receives a public payment-note binding code.
	AfdianAPIUserID            string
	AfdianAPIToken             string
	AfdianAPIBase              string
	AfdianWebhookPathSecret    string
	AfdianWebhookPublicKey     string
	MembershipBindingOffset    int64
	MembershipBindingAlgorithm int
	MembershipActivationPepper string
}

// Load reads configuration from the environment and validates required fields.
func Load() (*Config, error) {
	c := &Config{
		Port:                   getenv("PORT", "8080"),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		JWTSigningKey:          os.Getenv("ZONENAN_JWT_SIGNING_KEY"),
		GradePepper:            os.Getenv("GRADE_PEPPER"),
		AdminSecret:            os.Getenv("ADMIN_SECRET"),
		AdminBootstrapEmail:    strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_BOOTSTRAP_EMAIL"))),
		AdminBootstrapPassword: os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"),
		AdminBootstrapName:     strings.TrimSpace(os.Getenv("ADMIN_BOOTSTRAP_NAME")),
		AnalyticsPepper:        os.Getenv("ANALYTICS_PEPPER"),
		MerchantUploadDir:      getenv("MERCHANT_UPLOAD_DIR", "data/merchant-uploads"),
		MapTileKey:             os.Getenv("TIANDITU_MAP_KEY"),
		MapTileCacheDir:        getenv("MAP_TILE_CACHE_DIR", "data/map-tiles"),
		MapTileParallel:        getenvInt("MAP_TILE_UPSTREAM_PARALLEL", 4),
		MapTileMaxBytes:        getenvInt64("MAP_TILE_CACHE_MAX_BYTES", 4<<30),
		MapTileTrimBytes:       getenvInt64("MAP_TILE_CACHE_TRIM_TO_BYTES", (4<<30)*4/5),
		MapTileUpstream:        getenvBool("MAP_TILE_UPSTREAM_ENABLED", false),
		WebAllowedOrigins:      splitOrigins(os.Getenv("WEB_ALLOWED_ORIGINS")),
		TiandituWebKey:         strings.TrimSpace(os.Getenv("TIANDITU_WEB_KEY")),
		PasskeyRPID:            getenv("PASSKEY_RP_ID", "zonenan.skina.cn"),
		PasskeyRelatedOrigins:  splitOrigins(getenv("PASSKEY_RELATED_ORIGINS", "https://web.zonenan.pro")),
		ArtifactAllowedHosts:   splitCSV(os.Getenv("APP_ARTIFACT_ALLOWED_HOSTS")),
		ArtifactMaxBytes:       getenvInt64("APP_ARTIFACT_MAX_BYTES", 512<<20),
		ArtifactTimeout:        time.Duration(getenvInt("APP_ARTIFACT_TIMEOUT_SECONDS", 45)) * time.Second,
		// S1:IDS 回验(默认值 = 客户端 ids_oauth.dart 里的公开值,逆向自学校官方 App)。
		IDSAuthServer:              getenv("IDS_AUTH_SERVER", "https://ca.csu.edu.cn/authserver"),
		IDSAppID:                   getenv("IDS_APP_ID", "841252795775451136"),
		IDSAppSecret:               getenv("IDS_APP_SECRET", "17953FF13AAPX7S84WZ7"),
		CASServiceURL:              getenv("CAS_SERVICE_URL", "http://ca.csu.edu.cn/personalInfo/personCenter/index.html"),
		AfdianAPIUserID:            os.Getenv("AFDIAN_API_USER_ID"),
		AfdianAPIToken:             os.Getenv("AFDIAN_API_TOKEN"),
		AfdianAPIBase:              getenv("AFDIAN_API_BASE", "https://afdian.com"),
		AfdianWebhookPathSecret:    os.Getenv("AFDIAN_WEBHOOK_PATH_SECRET"),
		AfdianWebhookPublicKey:     strings.ReplaceAll(os.Getenv("AFDIAN_WEBHOOK_PUBLIC_KEY"), `\n`, "\n"),
		MembershipBindingOffset:    getenvInt64("MEMBERSHIP_BINDING_OFFSET", 10007),
		MembershipBindingAlgorithm: getenvInt("MEMBERSHIP_BINDING_ALGORITHM", 1),
		MembershipActivationPepper: os.Getenv("MEMBERSHIP_ACTIVATION_PEPPER"),
		// 默认 production:忘设 APP_ENV 时走 fail-closed 分支(密钥缺失即 fatal),
		// 杜绝"直接跑二进制/换编排忘设环境→用公开硬编码密钥给所有人签令牌"的伪造风险。
		// 本地开发须显式 APP_ENV=development 才启用不安全的开发默认密钥。
		Env: getenv("APP_ENV", "production"),
	}
	c.JWTExpire = time.Duration(getenvInt("ZONENAN_JWT_EXPIRE_HOURS", 720)) * time.Hour
	c.WebAccessExpire = time.Duration(getenvInt("WEB_ACCESS_TOKEN_HOURS", 24)) * time.Hour
	c.WebSessionShort = time.Duration(getenvInt("WEB_SESSION_SHORT_HOURS", 168)) * time.Hour
	c.WebSessionLong = time.Duration(getenvInt("WEB_SESSION_LONG_HOURS", 720)) * time.Hour
	c.AuthExpire = time.Duration(getenvInt("GRADE_AUTH_EXPIRE_DAYS", 3)) * 24 * time.Hour
	c.AdminSessionTTL = time.Duration(getenvInt("ADMIN_SESSION_HOURS", 12)) * time.Hour
	c.MerchantSessionTTL = time.Duration(getenvInt("MERCHANT_SESSION_HOURS", 24)) * time.Hour

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.GradePepper == "" {
		return nil, fmt.Errorf("GRADE_PEPPER is required (= old backend JWT_SECRET, keeps score hashes continuous)")
	}
	// H1:签名密钥缺失,仅在显式 development 环境下回退到不安全的开发密钥,
	// 其余任何环境(含默认/拼错的 APP_ENV)一律 fatal,杜绝"忘设 APP_ENV=production
	// 就用公开硬编码密钥给所有人签令牌"的账号伪造风险。
	if c.JWTSigningKey == "" {
		if c.Env != "development" {
			return nil, fmt.Errorf("ZONENAN_JWT_SIGNING_KEY is required (set APP_ENV=development only for local dev)")
		}
		c.JWTSigningKey = "dev-insecure-signing-key-change-me"
	}
	// B2:管理密钥无硬编码兜底。非 development 环境缺失即 fatal,避免后台裸奔。
	if c.AdminSecret == "" {
		if c.Env != "development" {
			return nil, fmt.Errorf("ADMIN_SECRET is required (set APP_ENV=development only for local dev)")
		}
		c.AdminSecret = "dev-insecure-admin-secret-change-me"
	}
	if c.AnalyticsPepper == "" {
		if c.Env != "development" {
			return nil, fmt.Errorf("ANALYTICS_PEPPER is required (use a dedicated random secret)")
		}
		c.AnalyticsPepper = "dev-insecure-analytics-pepper-change-me"
	}
	if c.Env != "development" && len(c.AnalyticsPepper) < 32 {
		return nil, fmt.Errorf("ANALYTICS_PEPPER must be at least 32 characters")
	}
	if c.MembershipBindingOffset <= 0 {
		return nil, fmt.Errorf("MEMBERSHIP_BINDING_OFFSET must be positive")
	}
	if c.MembershipBindingAlgorithm != 1 {
		return nil, fmt.Errorf("MEMBERSHIP_BINDING_ALGORITHM must be 1")
	}
	if c.MembershipActivationPepper == "" {
		if c.Env != "development" {
			return nil, fmt.Errorf("MEMBERSHIP_ACTIVATION_PEPPER is required")
		}
		c.MembershipActivationPepper = "dev-insecure-membership-activation-pepper-change-me"
	}
	if (c.AdminBootstrapEmail == "") != (c.AdminBootstrapPassword == "") {
		return nil, fmt.Errorf("ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD must be set together")
	}
	if c.AdminBootstrapPassword != "" && len(c.AdminBootstrapPassword) < 12 {
		return nil, fmt.Errorf("ADMIN_BOOTSTRAP_PASSWORD must be at least 12 characters")
	}
	if c.Env != "development" && len(c.MembershipActivationPepper) < 32 {
		return nil, fmt.Errorf("MEMBERSHIP_ACTIVATION_PEPPER must be at least 32 characters")
	}
	return c, nil
}

func splitOrigins(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimRight(strings.TrimSpace(item), "/"); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvInt64(k string, def int64) int64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func getenvBool(k string, def bool) bool {
	if value := strings.TrimSpace(os.Getenv(k)); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return def
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			out = append(out, item)
		}
	}
	return out
}
