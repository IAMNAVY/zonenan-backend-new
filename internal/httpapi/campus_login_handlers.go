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
	StudentID  string `json:"student_id"`
	DeviceInfo string `json:"device_info"`
	DeviceName string `json:"device_name"`
}

// The response is deliberately independent of stored profile, roles, memberships
// and recovery methods, even when the claimed student already has an account.
func campusClaimUser(uid int64) map[string]interface{} {
	return map[string]interface{}{"id": uid, "nickname": "中南同学", "has_campus": true, "campus_verified": false, "role": "user"}
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
	// Only possession of an existing valid ZoneNaN credential can resume full
	// access. A known device fingerprint, name, or school login is not proof.
	if userIDFrom(r) == user.ID && !user.IsBanned {
		s.issueFor(w, user, false)
		return
	}
	token, err := s.tokens.IssueCampusClaim(user.ID, req.DeviceInfo)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "令牌签发失败")
		return
	}
	OK(w, authResp{Token: token, User: campusClaimUser(user.ID)})
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
				OK(w, campusClaimUser(claim.UserID))
				return
			}
		}
		full.ServeHTTP(w, r)
	}
}
