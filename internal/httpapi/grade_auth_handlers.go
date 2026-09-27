package httpapi

import (
	"net/http"
	"time"

	"zonenan-backend/internal/auth"
)

// requireCampus 取当前用户的 student_hash;未绑校园返回空并已写出 403。
func (s *Server) requireCampus(w http.ResponseWriter, r *http.Request) (string, bool) {
	hash, err := s.users.StudentHashOf(r.Context(), userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return "", false
	}
	if hash == "" {
		Fail(w, http.StatusForbidden, "请先绑定校园身份（登录信息门户）")
		return "", false
	}
	return hash, true
}

type consentReq struct {
	DeviceInfo string `json:"device_info"`
}

// handleGradeAuthorize:同意协议(状态→active)。必须已绑校园。
func (s *Server) handleGradeAuthorize(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCampus(w, r); !ok {
		return
	}
	var req consentReq
	if !decodeJSONOptional(w, r, &req) {
		return
	}
	fp := auth.DeviceFingerprint(req.DeviceInfo)
	if err := s.grades.Consent(r.Context(), userIDFrom(r), fp); err != nil {
		Fail(w, http.StatusInternalServerError, "授权失败")
		return
	}
	OK(w, map[string]string{"consent_status": "active"})
}

// handleGradeRevoke:撤销同意(状态→revoked)。已上传数据保留。
func (s *Server) handleGradeRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.grades.Revoke(r.Context(), userIDFrom(r)); err != nil {
		Fail(w, http.StatusInternalServerError, "撤销失败")
		return
	}
	OK(w, map[string]string{"consent_status": "revoked"})
}

// handleGradeAuthStatus:返回授权状态 + 是否该重抓(节流)+ 是否该全量 + 查询权限详情。
func (s *Server) handleGradeAuthStatus(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	st, err := s.grades.GetAuthStatus(r.Context(), uid)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	throttleH := s.setting.GetInt(r.Context(), "grade_sync_throttle_hours", 24)

	// 与实际查询守卫共用同一套 normal/grace/privileged 判定。授权状态只控制
	// 是否抓取成绩；管理员/白名单即使未授权也仍是无限查询，但不会自动贡献数据。
	graceDays := s.setting.GetInt(r.Context(), "grade_contrib_grace_days", 30)
	privileged := uid > 0 && s.users.IsPrivileged(r.Context(), uid)
	queryPath := unlimitedGradePath(st, privileged, graceDays, time.Now())
	canQuery := queryPath != ""
	freeRemaining := 0

	if !canQuery {
		freeLimit := s.setting.GetInt(r.Context(), "grade_free_query_limit", 2)
		deviceFP := r.Header.Get("X-Device-Fingerprint")
		used, _ := s.grades.FreeQueryCount(r.Context(), uid, deviceFP)
		if freeLimit > used {
			canQuery = true
			queryPath = "free"
			freeRemaining = freeLimit - used
		} else {
			queryPath = "none"
		}
	}

	OK(w, map[string]interface{}{
		"consent_status":     st.ConsentStatus,
		"can_query":          canQuery,
		"query_path":         queryPath,
		"free_remaining":     freeRemaining,
		"first_consented_at": st.FirstConsentedAt,
		"last_sync_at":       st.LastSyncAt,
		"last_full_sync_at":  st.LastFullSyncAt,
		"should_sync":        s.grades.ShouldSync(st, throttleH),
		"should_full_sync":   s.grades.ShouldFullSync(st),
	})
}
