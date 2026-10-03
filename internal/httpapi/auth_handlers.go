package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"zonenan-backend/internal/auth"
	"zonenan-backend/internal/store"
)

// 验证码冷却与有效期的默认值(可被 app_settings 覆盖)。
const (
	defaultCodeCooldownSeconds = 300 // 5 分钟只能发一次
	defaultCodeTTLMinutes      = 5
)

type authResp struct {
	DeviceCredential string      `json:"device_credential,omitempty"`
	Token            string      `json:"token"`
	User             interface{} `json:"user"`
	IsNew            bool        `json:"is_new"`
	Switched         bool        `json:"switched,omitempty"`
}

type webAuthResp struct {
	Token     string      `json:"token"`
	ExpiresAt time.Time   `json:"expires_at"`
	User      interface{} `json:"user"`
}

func (s *Server) issueWebFor(w http.ResponseWriter, r *http.Request, user *store.User, remember bool, device string) {
	if strings.TrimSpace(device) == "" {
		device = "Web 浏览器"
	}
	ttl := s.cfg.WebSessionShort
	if remember {
		ttl = s.cfg.WebSessionLong
	}
	sid, refresh, sessionExpiry, err := s.webSessions.Create(r.Context(), user.ID, device, remember, ttl)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "创建登录会话失败")
		return
	}
	token, err := s.webTokens.IssueSession(user.ID, sid)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "令牌签发失败")
		return
	}
	setRefreshCookie(w, refresh, sessionExpiry)
	OK(w, webAuthResp{Token: token, ExpiresAt: time.Now().Add(s.cfg.WebAccessExpire), User: user})
}

func setRefreshCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: "zonenan_refresh", Value: value, Path: "/auth", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) issueFor(w http.ResponseWriter, user *store.User, isNew bool) {
	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "令牌签发失败")
		return
	}
	OK(w, authResp{Token: token, User: user, IsNew: isNew})
}

func (s *Server) issueForSwitched(w http.ResponseWriter, user *store.User, switched bool) {
	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "令牌签发失败")
		return
	}
	OK(w, authResp{Token: token, User: user, Switched: switched})
}

// normalizeEmail 规范化邮箱(小写去空格);v1 仅接受 QQ 邮箱。
func normalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || !strings.Contains(e, "@") {
		return "", errors.New("邮箱格式不正确")
	}
	return e, nil
}

type sendCodeReq struct {
	Email string `json:"email"`
}

// handleSendCode:发邮箱验证码(注册用)。
func (s *Server) handleSendCode(w http.ResponseWriter, r *http.Request) {
	var req sendCodeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	cooldown := s.setting.GetInt(r.Context(), "email_code_resend_seconds", defaultCodeCooldownSeconds)
	if left := cooldown - s.codes.SecondsSinceLast(r.Context(), email); left > 0 {
		Fail(w, http.StatusTooManyRequests, fmt.Sprintf("请 %d 秒后再试", left))
		return
	}
	ttl := time.Duration(s.setting.GetInt(r.Context(), "email_code_ttl_minutes", defaultCodeTTLMinutes)) * time.Minute
	code := auth.RandomCode()
	if err := s.codes.Save(r.Context(), email, code, "register", ttl); err != nil {
		Fail(w, http.StatusInternalServerError, "验证码生成失败")
		return
	}
	body := fmt.Sprintf("你的 ZoneNaN 验证码是 %s，%d 分钟内有效。", code, int(ttl.Minutes()))
	if err := s.mail.Send(r.Context(), email, "ZoneNaN 验证码", body); err != nil {
		// M2:不回显内部发信错误,仅记日志。H6:邮箱脱敏,不打明文学号。
		log.Printf("send code to %s failed: %v", maskEmail(email), err)
		Fail(w, http.StatusInternalServerError, "验证码发送失败,请稍后重试")
		return
	}
	OK(w, map[string]bool{"sent": true})
}

type registerReq struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
}

// handleRegister:校验验证码 → 建号(邮箱身份)→ 直接登入。
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Password) < 6 {
		Fail(w, http.StatusBadRequest, "密码至少 6 位")
		return
	}
	if len(req.Password) > 72 {
		Fail(w, http.StatusBadRequest, "密码最长 72 字节")
		return
	}
	if err := s.codes.Verify(r.Context(), email, strings.TrimSpace(req.Code), "register"); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.users.RegisterEmail(r.Context(), email, req.Password, strings.TrimSpace(req.Nickname))
	if errors.Is(err, store.ErrEmailTaken) {
		Fail(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "注册失败")
		return
	}
	s.issueFor(w, user, true)
}

// handleSendResetCode:发送找回密码验证码(scope=reset)。
// S5:为防账号枚举,无论邮箱是否已注册都返回成功文案,仅对已注册邮箱真正发信。
func (s *Server) handleSendResetCode(w http.ResponseWriter, r *http.Request) {
	var req sendCodeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	cooldown := s.setting.GetInt(r.Context(), "email_code_resend_seconds", defaultCodeCooldownSeconds)
	if left := cooldown - s.codes.SecondsSinceLast(r.Context(), email); left > 0 {
		Fail(w, http.StatusTooManyRequests, fmt.Sprintf("请 %d 秒后再试", left))
		return
	}
	// 仅对已注册的邮箱真正发码;未注册则静默跳过(仍返回成功,防枚举)。
	if s.users.EmailRegistered(r.Context(), email) {
		ttl := time.Duration(s.setting.GetInt(r.Context(), "email_code_ttl_minutes", defaultCodeTTLMinutes)) * time.Minute
		code := auth.RandomCode()
		if err := s.codes.Save(r.Context(), email, code, "reset", ttl); err != nil {
			Fail(w, http.StatusInternalServerError, "验证码生成失败")
			return
		}
		body := fmt.Sprintf("你的 ZoneNaN 找回密码验证码是 %s，%d 分钟内有效。若非本人操作请忽略。", code, int(ttl.Minutes()))
		if err := s.mail.Send(r.Context(), email, "ZoneNaN 找回密码", body); err != nil {
			log.Printf("send reset code to %s failed: %v", maskEmail(email), err)
			Fail(w, http.StatusInternalServerError, "验证码发送失败,请稍后重试")
			return
		}
	}
	OK(w, map[string]bool{"sent": true})
}

type resetPasswordReq struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

// handleResetPassword:校验 reset 验证码后重设密码(S5 找回密码)。
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordReq
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Password) < 6 {
		Fail(w, http.StatusBadRequest, "密码至少 6 位")
		return
	}
	if len(req.Password) > 72 {
		Fail(w, http.StatusBadRequest, "密码最长 72 字节")
		return
	}
	if err := s.codes.Verify(r.Context(), email, strings.TrimSpace(req.Code), "reset"); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.users.ResetEmailPassword(r.Context(), email, req.Password); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(w, http.StatusBadRequest, "该邮箱未注册")
			return
		}
		Fail(w, http.StatusInternalServerError, "重设密码失败")
		return
	}
	OK(w, map[string]bool{"reset": true})
}

type changePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// handleChangePassword:登录态下校验旧密码后改密(S5)。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 6 {
		Fail(w, http.StatusBadRequest, "密码至少 6 位")
		return
	}
	if len(req.NewPassword) > 72 {
		Fail(w, http.StatusBadRequest, "密码最长 72 字节")
		return
	}
	err := s.users.ChangeEmailPassword(r.Context(), userIDFrom(r), req.OldPassword, req.NewPassword)
	if errors.Is(err, store.ErrNotFound) {
		Fail(w, http.StatusBadRequest, "当前账号未设置邮箱密码")
		return
	}
	if errors.Is(err, store.ErrWrongPassword) {
		Fail(w, http.StatusBadRequest, "原密码错误")
		return
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "修改密码失败")
		return
	}
	OK(w, map[string]bool{"changed": true})
}

type loginReq struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	WebSession bool   `json:"web_session,omitempty"`
	RememberMe bool   `json:"remember_me,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
}

// handleLogin:邮箱+密码登录。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.users.LoginEmail(r.Context(), email, req.Password)
	if err != nil {
		Fail(w, http.StatusUnauthorized, "邮箱或密码错误")
		return
	}
	if user.IsBanned {
		Fail(w, http.StatusForbidden, "账号已被封禁")
		return
	}
	if req.WebSession {
		s.issueWebFor(w, r, user, req.RememberMe, req.DeviceName)
		return
	}
	s.issueFor(w, user, false)
}

func (s *Server) handleWebRefresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("zonenan_refresh")
	if err != nil {
		Fail(w, http.StatusUnauthorized, "登录会话已过期")
		return
	}
	sid, uid, refresh, expires, err := s.webSessions.Rotate(r.Context(), cookie.Value)
	if err != nil {
		setRefreshCookie(w, "", time.Unix(1, 0))
		Fail(w, http.StatusUnauthorized, "登录会话已过期")
		return
	}
	user, err := s.users.GetByID(r.Context(), uid)
	if err != nil || user.IsBanned {
		Fail(w, http.StatusUnauthorized, "账号不可用")
		return
	}
	token, err := s.webTokens.IssueSession(uid, sid)
	if err != nil {
		Fail(w, 500, "令牌签发失败")
		return
	}
	setRefreshCookie(w, refresh, expires)
	OK(w, webAuthResp{Token: token, ExpiresAt: time.Now().Add(s.cfg.WebAccessExpire), User: user})
}

func (s *Server) handleWebLogout(w http.ResponseWriter, r *http.Request) {
	h := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	uid, sid, _ := s.tokens.ParseSession(h)
	if sid != "" {
		_ = s.webSessions.Revoke(r.Context(), sid, uid)
	}
	setRefreshCookie(w, "", time.Unix(1, 0))
	OK(w, map[string]bool{"logged_out": true})
}

type casLoginReq struct {
	StudentID string `json:"student_id"`
	Name      string `json:"name"`
	// IdsToken:旧客户端 IDS 登录令牌，必须经学校回验；仅为兼容保留。
	IdsToken string `json:"ids_token"`
	// CASTicket:当前 CAS 流程针对固定 service 申请的一次性票据。
	// 新客户端优先使用它，避免依赖已下线的 IDS mobile_code 换票链。
	CASTicket string `json:"cas_ticket"`
	// DeviceInfo:设备信息原文,服务端算指纹用于 TOFU 锁设备。
	DeviceInfo string `json:"device_info"`
	// DeviceName:可读机型名(设备管理界面展示)。
	DeviceName string `json:"device_name"`
	// DeferDeviceEmail:新客户端设为 true,先展示可选验证方式,选择邮箱后再调用 email/start。
	// 旧客户端省略此字段时保持自动发邮件行为。
	DeferDeviceEmail bool `json:"defer_device_email,omitempty"`
}

// handleCasLogin:信息门户登录后调用。新客户端带 CAS service ticket，旧客户端带 IDS token。
// 带 Bearer=绑到当前账号,不带=自动开户/登入(反一人两号)。
// 若 CAS 身份已绑定另一账号,返回该账号并标记 switched=true,客户端切换。
//
// 身份回验统一在学校 CAS/IDS 服务端完成，客户端上报的 student_id 只用于设备绑定探测。
// 新客户端走 CAS service ticket，旧客户端走 IDS token。
//   - 新账号首次 CAS 登录 → TOFU:信任当前设备。
//   - 老账号从「新设备」登录 → 不直接签发,返回 need_device_verify,
//     要求走邮箱验证(默认 学号@csu.edu.cn,不可伪造)后再 confirm 信任设备。
//   - 设备在黑名单 → 拒绝。
func (s *Server) handleCasLogin(w http.ResponseWriter, r *http.Request) {
	var req casLoginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.StudentID = strings.TrimSpace(req.StudentID)
	if req.StudentID == "" {
		Fail(w, http.StatusBadRequest, "缺少学号")
		return
	}
	// 新客户端使用 CAS service ticket；旧客户端继续使用 IDS token。
	// 两者至少提供一个，服务端始终从学校认证系统取得真实学号。
	if strings.TrimSpace(req.CASTicket) == "" && strings.TrimSpace(req.IdsToken) == "" {
		Fail(w, http.StatusBadRequest, "缺少校园身份凭证")
		return
	}

	// 设备指纹直接用客户端 device_info(= App 的 deviceId),与请求头
	// X-Device-Fingerprint、设备黑名单三处保持一致,不再二次 hash。
	deviceFP := strings.TrimSpace(req.DeviceInfo)
	if deviceFP == "" {
		Fail(w, http.StatusBadRequest, "缺少设备指纹")
		return
	}
	if len(deviceFP) > 512 || len(req.DeviceName) > 200 {
		Fail(w, http.StatusBadRequest, "设备信息过长")
		return
	}

	// 设备黑名单:直接拒绝。
	if s.telemetry.IsDeviceBlocked(r.Context(), deviceFP) {
		Fail(w, http.StatusForbidden, "该设备已被限制")
		return
	}

	currentUID := userIDFrom(r)

	// Preserve the legacy trusted-device fast path, including logged-out clients.
	if currentUID == 0 {
		claimedHash := auth.StudentHash(req.StudentID, s.cfg.GradePepper)
		trusted, err := s.devices.IsTrusted(r.Context(), claimedHash, deviceFP)
		if err != nil {
			Fail(w, http.StatusInternalServerError, "可信设备校验失败")
			return
		}
		if trusted {
			s.finishCasLogin(w, r, claimedHash, req.Name, deviceFP, req.DeviceName, 0)
			return
		}
	}

	// 新客户端用 CAS service ticket 回验，旧客户端才走 IDS userProfile。
	// 两条路径都由学校认证服务返回真实学号，绝不信任 req.StudentID。
	var verifiedID string
	var err error
	if strings.TrimSpace(req.CASTicket) != "" {
		verifiedID, err = s.ids.VerifyCASTicket(r.Context(), req.CASTicket)
	} else {
		verifiedID, err = s.ids.VerifyStudentID(r.Context(), req.IdsToken)
	}
	if err != nil {
		// 回验失败(网络/被封/token 无效):fail-closed,拒绝新绑定,不退回信任客户端。
		// 新绑定本就罕见,短时不可用可接受,绝不为可用性牺牲防抢注。
		log.Printf("cas-login campus identity verify failed (device=%s): %v", maskFP(deviceFP), err)
		Fail(w, http.StatusServiceUnavailable, "校园身份校验暂不可用,请稍后重试")
		return
	}
	// 用回验学号重算 hash,后续一切以此为准(忽略 req.StudentID)。
	studentHash := auth.StudentHash(verifiedID, s.cfg.GradePepper)

	// TOFU 换设备保护:无当前登录 + 该学号已有可信设备 + 当前是新设备 → 先走校园邮箱验证。
	if currentUID == 0 {
		deviceCount, err := s.devices.Count(r.Context(), studentHash)
		if err != nil {
			Fail(w, http.StatusInternalServerError, "可信设备校验失败")
			return
		}
		if deviceCount > 0 {
			trusted, err := s.devices.IsTrusted(r.Context(), studentHash, deviceFP)
			if err != nil {
				Fail(w, http.StatusInternalServerError, "可信设备校验失败")
				return
			}
			if !trusted {
				// 新设备:创建一次性挑战。旧客户端仍自动发校园邮箱验证码。
				req.StudentID = verifiedID
				s.beginDeviceVerify(w, r, req, studentHash)
				return
			}
		}
	}

	s.finishCasLogin(w, r, studentHash, req.Name, deviceFP, req.DeviceName, currentUID)
}

// finishCasLogin:完成 CAS 绑定/登入并签发令牌(学号 hash 已确定可信)。
func (s *Server) finishCasLogin(w http.ResponseWriter, r *http.Request, studentHash, name, deviceFP, deviceName string, currentUID int64) {
	user, isNew, err := s.users.BindOrLoginCAS(r.Context(), studentHash, strings.TrimSpace(name), currentUID)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "校园绑定失败")
		return
	}
	if user.IsBanned {
		Fail(w, http.StatusForbidden, "账号已被封禁")
		return
	}
	// TOFU:信任当前设备(首次即绑定;已存在则刷新)。
	if err := s.devices.Trust(r.Context(), studentHash, deviceFP, strings.TrimSpace(deviceName)); err != nil {
		Fail(w, http.StatusInternalServerError, "可信设备保存失败")
		return
	}

	switched := currentUID > 0 && user.ID != currentUID
	s.issueForDevice(w, r, user, studentHash, deviceFP, isNew, switched)
}

// campusEmail 由学号拼默认校园邮箱(不可伪造:发到此邮箱只有本人能收)。
func campusEmail(studentID string) string {
	return strings.ToLower(strings.TrimSpace(studentID)) + "@csu.edu.cn"
}

// beginDeviceVerify creates a one-time challenge bound to the verified campus
// identity and target device. Older clients still get an automatically sent email;
// newer clients can defer it until the user selects the email method.
func (s *Server) beginDeviceVerify(w http.ResponseWriter, r *http.Request, req casLoginReq, studentHash string) {
	email := campusEmail(req.StudentID)
	challengeTTL := time.Duration(s.setting.GetInt(r.Context(), "device_login_challenge_ttl_minutes", defaultDeviceChallengeTTLMinutes)) * time.Minute
	challenge, secret, err := s.challenges.Create(
		r.Context(), studentHash, strings.TrimSpace(req.DeviceInfo), strings.TrimSpace(req.DeviceName), challengeTTL,
	)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "设备登录请求创建失败")
		return
	}

	response := map[string]interface{}{
		"need_device_verify": true,
		"email_masked":       maskEmail(email),
		"challenge_id":       challenge.ID,
		"secret":             secret,
		"available_methods":  []string{"email", "trusted_device"},
		"expires_at":         challenge.ExpiresAt,
	}
	if !req.DeferDeviceEmail {
		left, err := s.sendDeviceChallengeEmail(r, email)
		if err != nil {
			Fail(w, http.StatusInternalServerError, "验证码发送失败,请稍后重试")
			return
		}
		if left > 0 {
			response["resend_in"] = left
		}
	}
	OK(w, response)
}

type casDeviceVerifyReq struct {
	StudentID  string `json:"student_id"`
	Name       string `json:"name"`
	Code       string `json:"code"`
	DeviceInfo string `json:"device_info"`
	DeviceName string `json:"device_name"`
}

// handleCasDeviceVerify:新设备验证码校验通过后,信任设备并签发令牌。
func (s *Server) handleCasDeviceVerify(w http.ResponseWriter, r *http.Request) {
	var req casDeviceVerifyReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.StudentID = strings.TrimSpace(req.StudentID)
	if req.StudentID == "" {
		Fail(w, http.StatusBadRequest, "缺少学号")
		return
	}
	deviceFP := strings.TrimSpace(req.DeviceInfo)
	if deviceFP == "" || len(deviceFP) > 512 || len(req.DeviceName) > 200 {
		Fail(w, http.StatusBadRequest, "设备信息无效")
		return
	}
	if s.telemetry.IsDeviceBlocked(r.Context(), deviceFP) {
		Fail(w, http.StatusForbidden, "该设备已被限制")
		return
	}
	email := campusEmail(req.StudentID)
	if err := s.codes.Verify(r.Context(), email, strings.TrimSpace(req.Code), "device_verify"); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	studentHash := auth.StudentHash(req.StudentID, s.cfg.GradePepper)

	user, isNew, err := s.users.BindOrLoginCAS(r.Context(), studentHash, strings.TrimSpace(req.Name), 0)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "校园绑定失败")
		return
	}
	if user.IsBanned {
		Fail(w, http.StatusForbidden, "账号已被封禁")
		return
	}
	if err := s.devices.Trust(r.Context(), studentHash, deviceFP, strings.TrimSpace(req.DeviceName)); err != nil {
		Fail(w, http.StatusInternalServerError, "可信设备保存失败")
		return
	}
	s.issueForDevice(w, r, user, studentHash, deviceFP, isNew, false)
}

// maskFP 遮蔽设备指纹(日志用):只留前 6 位,不打完整标识。
func maskFP(fp string) string {
	if len(fp) <= 6 {
		return "***"
	}
	return fp[:6] + "***"
}

// maskEmail 遮蔽邮箱本地部分(展示用):a***@csu.edu.cn。
func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 1 {
		return email
	}
	return email[:1] + "***" + email[at:]
}

// handleListDevices:列出当前账号(校园身份)的可信设备。
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireCampus(w, r)
	if !ok {
		return
	}
	list, err := s.devices.List(r.Context(), hash)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, list)
}

// handleRevokeDevice:撤销当前账号名下的一台可信设备。
func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireCampus(w, r)
	if !ok {
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.devices.Revoke(r.Context(), hash, req.ID); err != nil {
		Fail(w, http.StatusInternalServerError, "撤销失败")
		return
	}
	OK(w, map[string]bool{"ok": true})
}

// handleMe:返回当前登录账号。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, err := s.users.GetByID(r.Context(), userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusNotFound, "账号不存在")
		return
	}
	OK(w, u)
}

type profileReq struct {
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
}

// handleUpdateProfile:设置昵称/头像。
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req profileReq
	if !decodeJSON(w, r, &req) {
		return
	}
	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		Fail(w, http.StatusBadRequest, "昵称不能为空")
		return
	}
	if len([]rune(nickname)) > 20 {
		Fail(w, http.StatusBadRequest, "昵称最长 20 字")
		return
	}
	if err := s.users.UpdateProfile(r.Context(), userIDFrom(r), nickname, strings.TrimSpace(req.AvatarURL)); err != nil {
		Fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	OK(w, map[string]bool{"updated": true})
}

// handleLogout:无状态,客户端丢弃令牌即可。
func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	OK(w, map[string]bool{"ok": true})
}

type bindEmailReq struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

// handleBindEmail:已登录用户给当前账号绑定邮箱+密码(需验证码)。
func (s *Server) handleBindEmail(w http.ResponseWriter, r *http.Request) {
	var req bindEmailReq
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Password) < 6 {
		Fail(w, http.StatusBadRequest, "密码至少 6 位")
		return
	}
	if len(req.Password) > 72 {
		Fail(w, http.StatusBadRequest, "密码最长 72 字节")
		return
	}
	if err := s.codes.Verify(r.Context(), email, strings.TrimSpace(req.Code), "register"); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	uid := userIDFrom(r)
	if err := s.users.BindEmail(r.Context(), uid, email, req.Password); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			Fail(w, http.StatusConflict, "该邮箱已被其他账号绑定")
			return
		}
		Fail(w, http.StatusInternalServerError, "绑定失败")
		return
	}
	u, _ := s.users.GetByID(r.Context(), uid)
	OK(w, u)
}
