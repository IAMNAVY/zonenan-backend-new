package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"zonenan-backend/internal/adminauth"
)

func (s *Server) handleAdminPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		v1Error(w, r, 400, "INVALID_JSON", "请求格式无效")
		return
	}
	err := s.adminAccounts.ChangePassword(r.Context(), adminPrincipalFrom(r).ID, body.Current, body.Next)
	if errors.Is(err, adminauth.ErrInvalidPassword) {
		v1Error(w, r, 400, "INVALID_PASSWORD", "新密码须为 12–72 字节，且不能与原密码相同")
		return
	}
	if errors.Is(err, adminauth.ErrInvalidCredentials) {
		v1Error(w, r, 400, "INVALID_CURRENT_PASSWORD", "原密码不正确")
		return
	}
	if err != nil {
		v1Error(w, r, 500, "PASSWORD_CHANGE_FAILED", "修改失败，请稍后重试")
		return
	}
	secure := s.cfg.Env != "development"
	for _, c := range []*http.Cookie{
		{Name: adminSessionCookie, Path: "/admin-api/v1", HttpOnly: true, Secure: secure, MaxAge: -1, SameSite: http.SameSiteStrictMode},
		{Name: adminCSRFCookie, Path: "/", Secure: secure, MaxAge: -1, SameSite: http.SameSiteStrictMode},
	} {
		http.SetCookie(w, c)
	}
	v1Data(w, r, 200, map[string]bool{"reauthenticate": true})
}
