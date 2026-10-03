package httpapi

import (
	"net/http"
	"regexp"
	"strings"
	"zonenan-backend/internal/auth"
	"zonenan-backend/internal/store"
)

var campusStudentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

type campusLoginReq struct {
	StudentID        string `json:"student_id"`
	DeviceInfo       string `json:"device_info"`
	DeviceName       string `json:"device_name"`
	DeviceCredential string `json:"device_credential"`
}

// Nicknames are public display data. Email, roles, memberships and recovery
// methods remain unavailable to restricted sessions.
func campusClaimUser(uid int64, nickname ...string) map[string]interface{} {
	name := "中南同学"
	if len(nickname) > 0 && strings.TrimSpace(nickname[0]) != "" {
		name = nickname[0]
	}
	return map[string]interface{}{"id": uid, "nickname": name, "has_campus": true, "campus_verified": false, "role": "user"}
}

func (s *Server) handleCampusLogin(w http.ResponseWriter, r *http.Request) {
	var req campusLoginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.StudentID = strings.TrimSpace(req.StudentID)
	req.DeviceInfo = strings.TrimSpace(req.DeviceInfo)
	if !campusStudentIDPattern.MatchString(req.StudentID) || req.DeviceInfo == "" || len(req.DeviceInfo) > 512 || len(req.DeviceName) > 200 {
		Fail(w, http.StatusBadRequest, "校园账号或设备信息无效")
		return
	}
	if s.telemetry.IsDeviceBlocked(r.Context(), req.DeviceInfo) {
		Fail(w, http.StatusForbidden, "该设备已被限制")
		return
	}
	hash := auth.StudentHash(req.StudentID, s.cfg.GradePepper)
	user, _, err := s.users.ReserveCampusClaim(r.Context(), hash)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "校园账号连接失败")
		return
	}
	trustID, err := s.devices.TrustID(r.Context(), hash, req.DeviceInfo)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "可信设备校验失败")
		return
	}
	if trustedCampusDeviceAllowed(s.tokens, user.ID, req.DeviceInfo, trustID, req.DeviceCredential) && !user.IsBanned {
		s.issueForDevice(w, r, user, hash, req.DeviceInfo, false, false)
		return
	}
	// Existing account credentials can upgrade legacy trust rows without email.
	if userIDFrom(r) == user.ID && !user.IsBanned {
		s.issueForDevice(w, r, user, hash, req.DeviceInfo, false, false)
		return
	}
	token, err := s.tokens.IssueCampusClaim(user.ID, req.DeviceInfo)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "令牌签发失败")
		return
	}
	OK(w, authResp{Token: token, User: campusClaimUser(user.ID, user.Nickname)})
}

func trustedCampusDeviceAllowed(tokens *auth.TokenManager, uid int64, device string, trustID int64, credential string) bool {
	if trustID <= 0 || device == "" {
		return false
	}
	// Logout and upgrades do not revoke server-side legacy fingerprint trust.
	if credential == "" {
		return true
	}
	claim, err := tokens.ParseTrustedDevice(credential)
	return err == nil && claim.UserID == uid && claim.Device == device && claim.TrustID == trustID
}

func (s *Server) issueForDevice(w http.ResponseWriter, r *http.Request, user *store.User, hash, device string, isNew, switched bool, preparedToken ...string) {
	var token string
	var err error
	if len(preparedToken) > 0 {
		token = preparedToken[0]
	} else {
		token, err = s.tokens.Issue(user.ID)
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "令牌签发失败")
		return
	}
	id, err := s.devices.TrustID(r.Context(), hash, device)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "可信设备校验失败")
		return
	}
	credential := ""
	if id > 0 {
		credential, err = s.tokens.IssueTrustedDevice(user.ID, device, id)
		if err != nil {
			Fail(w, http.StatusInternalServerError, "设备凭据签发失败")
			return
		}
	}
	OK(w, authResp{Token: token, User: user, IsNew: isNew, Switched: switched, DeviceCredential: credential})
}

func (s *Server) campusClaimFrom(w http.ResponseWriter, r *http.Request) (*auth.CampusClaim, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		Fail(w, http.StatusUnauthorized, "缺少登录令牌")
		return nil, false
	}
	claim, err := s.tokens.ParseCampusClaim(strings.TrimPrefix(h, "Bearer "))
	if err != nil {
		Fail(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return nil, false
	}
	return claim, true
}

func (s *Server) handleCampusVerify(w http.ResponseWriter, r *http.Request) {
	claim, ok := s.campusClaimFrom(w, r)
	if !ok {
		return
	}
	var req campusLoginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.StudentID = strings.TrimSpace(req.StudentID)
	if !campusStudentIDPattern.MatchString(req.StudentID) || len(req.DeviceName) > 200 {
		Fail(w, http.StatusBadRequest, "校园账号或设备信息无效")
		return
	}
	hash := auth.StudentHash(req.StudentID, s.cfg.GradePepper)
	user, err := s.users.GetByStudentHash(r.Context(), hash)
	if err != nil || user.ID != claim.UserID {
		Fail(w, http.StatusForbidden, "校园账号与当前会话不匹配")
		return
	}
	if user.IsBanned || s.telemetry.IsDeviceBlocked(r.Context(), claim.Device) {
		Fail(w, http.StatusForbidden, "账号或设备已被限制")
		return
	}
	// No code is sent during login. The existing challenge protocol sends it
	// only after the user chooses verification while accessing a feature.
	s.beginDeviceVerify(w, r, casLoginReq{StudentID: req.StudentID, DeviceInfo: claim.Device, DeviceName: req.DeviceName, DeferDeviceEmail: true}, hash)
}

func (s *Server) campusMeHandler() http.HandlerFunc {
	full := s.authMiddleware(http.HandlerFunc(s.handleMe))
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") {
			if claim, err := s.tokens.ParseCampusClaim(strings.TrimPrefix(h, "Bearer ")); err == nil {
				if s.users.CheckStatus(r.Context(), claim.UserID) == store.UserNotFound {
					Fail(w, http.StatusUnauthorized, "账号不存在，请重新登录")
					return
				}
				user, err := s.users.GetByID(r.Context(), claim.UserID)
				if err != nil {
					Fail(w, http.StatusInternalServerError, "读取账号失败")
					return
				}
				OK(w, campusClaimUser(claim.UserID, user.Nickname))
				return
			}
		}
		full.ServeHTTP(w, r)
	}
}
