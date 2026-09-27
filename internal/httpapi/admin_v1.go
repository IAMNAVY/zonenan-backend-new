package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"zonenan-backend/internal/adminauth"
)

const (
	adminSessionCookie = "zonenan_admin_session"
	adminCSRFCookie    = "zonenan_admin_csrf"
)

type adminContextKey string

const adminPrincipalKey adminContextKey = "admin-principal"

type v1Meta struct {
	RequestID string `json:"request_id,omitempty"`
}

func v1Data(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "meta": v1Meta{RequestID: chimiddleware.GetReqID(r.Context())}})
}

func v1Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message}, "meta": v1Meta{RequestID: chimiddleware.GetReqID(r.Context())}})
}

func adminPrincipalFrom(r *http.Request) *adminauth.Principal {
	p, _ := r.Context().Value(adminPrincipalKey).(*adminauth.Principal)
	return p
}

func (s *Server) handleAdminV1Login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&request); err != nil {
		v1Error(w, r, http.StatusBadRequest, "INVALID_JSON", "请求格式无效")
		return
	}
	principal, err := s.adminAccounts.Authenticate(r.Context(), request.Email, request.Password, clientIP(r))
	if err != nil {
		v1Error(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "邮箱或密码错误")
		return
	}
	sessionToken, csrfToken, expires, err := s.adminAccounts.CreateSession(r.Context(), principal.ID, clientIP(r), r.UserAgent(), s.cfg.AdminSessionTTL)
	if err != nil {
		v1Error(w, r, http.StatusInternalServerError, "SESSION_CREATE_FAILED", "创建管理会话失败")
		return
	}
	secure := s.cfg.Env != "development"
	http.SetCookie(w, &http.Cookie{Name: adminSessionCookie, Value: sessionToken, Path: "/admin-api/v1", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	// The SPA document is served from /. A cookie scoped to /admin-api/v1 is
	// not visible to JavaScript on that document, so it cannot be mirrored in
	// X-CSRF-Token. The HttpOnly session remains restricted to the API path.
	http.SetCookie(w, &http.Cookie{Name: adminCSRFCookie, Value: csrfToken, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: false, Secure: secure, SameSite: http.SameSiteStrictMode})
	v1Data(w, r, http.StatusOK, map[string]any{"user": principal, "csrf_token": csrfToken, "expires_at": expires})
}

func (s *Server) adminV1Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(adminSessionCookie)
		if err != nil || cookie.Value == "" {
			v1Error(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请登录管理后台")
			return
		}
		principal, csrfHash, err := s.adminAccounts.Resolve(r.Context(), cookie.Value)
		if err != nil {
			v1Error(w, r, http.StatusUnauthorized, "SESSION_EXPIRED", "管理会话已过期")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			provided := r.Header.Get("X-CSRF-Token")
			digest := sha256.Sum256([]byte(provided))
			if provided == "" || subtle.ConstantTimeCompare(digest[:], csrfHash) != 1 {
				v1Error(w, r, http.StatusForbidden, "CSRF_FAILED", "CSRF 校验失败")
				return
			}
		}
		ctx := context.WithValue(r.Context(), adminPrincipalKey, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requireAdminPermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := adminPrincipalFrom(r)
			for _, candidate := range principal.Permissions {
				if candidate == permission {
					next.ServeHTTP(w, r)
					return
				}
			}
			v1Error(w, r, http.StatusForbidden, "PERMISSION_DENIED", "没有执行此操作的权限")
		})
	}
}

func (s *Server) handleAdminV1Me(w http.ResponseWriter, r *http.Request) {
	v1Data(w, r, http.StatusOK, adminPrincipalFrom(r))
}

func (s *Server) handleAdminV1Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(adminSessionCookie); err == nil {
		_ = s.adminAccounts.Revoke(r.Context(), cookie.Value)
	}
	secure := s.cfg.Env != "development"
	for _, cookie := range []*http.Cookie{
		{Name: adminSessionCookie, Path: "/admin-api/v1", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode},
		{Name: adminCSRFCookie, Path: "/", MaxAge: -1, Secure: secure, SameSite: http.SameSiteStrictMode},
		// Clear the path used by the initial Phase 3 build as well.
		{Name: adminCSRFCookie, Path: "/admin-api/v1", MaxAge: -1, Secure: secure, SameSite: http.SameSiteStrictMode},
	} {
		http.SetCookie(w, cookie)
	}
	v1Data(w, r, http.StatusOK, map[string]bool{"logged_out": true})
}

func (s *Server) handleAdminV1Audit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.adminAccounts.ListAudit(r.Context(), limit)
	if err != nil {
		v1Error(w, r, http.StatusInternalServerError, "AUDIT_QUERY_FAILED", "审计日志查询失败")
		return
	}
	v1Data(w, r, http.StatusOK, rows)
}

func (s *Server) handleAdminV1Admins(w http.ResponseWriter, r *http.Request) {
	rows, err := s.adminAccounts.ListAdmins(r.Context())
	if err != nil {
		v1Error(w, r, http.StatusInternalServerError, "ADMIN_QUERY_FAILED", "管理员查询失败")
		return
	}
	v1Data(w, r, http.StatusOK, rows)
}

func (s *Server) handleAdminV1Roles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.adminAccounts.ListRoles(r.Context())
	if err != nil {
		v1Error(w, r, http.StatusInternalServerError, "ROLE_QUERY_FAILED", "角色查询失败")
		return
	}
	v1Data(w, r, http.StatusOK, rows)
}

func (s *Server) handleAdminV1SetRoles(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AdminID int64    `json:"admin_id"`
		Roles   []string `json:"roles"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&request); err != nil || request.AdminID <= 0 {
		v1Error(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "管理员或角色无效")
		return
	}
	if err := s.adminAccounts.SetRoles(r.Context(), request.AdminID, request.Roles); err != nil {
		v1Error(w, r, http.StatusBadRequest, "ROLE_UPDATE_FAILED", "角色更新失败")
		return
	}
	principal := adminPrincipalFrom(r)
	_ = s.adminAccounts.RecordAudit(r.Context(), principal, "admin.roles.update", "admin_user", strconv.FormatInt(request.AdminID, 10), chimiddleware.GetReqID(r.Context()), clientIP(r), r.UserAgent(), http.StatusOK)
	v1Data(w, r, http.StatusOK, map[string]any{"admin_id": request.AdminID, "roles": request.Roles})
}

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (w *statusCapture) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (s *Server) adminV1Audit(action, resource string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		capture := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		next(capture, r)
		if capture.status >= 200 && capture.status < 400 {
			principal := adminPrincipalFrom(r)
			resourceID := r.URL.Query().Get("id")
			if err := s.adminAccounts.RecordAudit(r.Context(), principal, action, resource, resourceID, chimiddleware.GetReqID(r.Context()), clientIP(r), r.UserAgent(), capture.status); err != nil {
				log.Printf("record admin audit failed: %v", err)
			}
		}
	}
}

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header), status: 200}
}
func (w *bufferedResponse) Header() http.Header         { return w.header }
func (w *bufferedResponse) WriteHeader(status int)      { w.status = status }
func (w *bufferedResponse) Write(p []byte) (int, error) { return w.body.Write(p) }

// adminV1Legacy adapts the proven legacy business handler to the v1 envelope.
// Authentication, authorization, CSRF and audit are all enforced before it runs.
func adminV1Legacy(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		buffer := newBufferedResponse()
		handler(buffer, r)
		var legacy Envelope
		if err := json.Unmarshal(buffer.body.Bytes(), &legacy); err != nil {
			v1Error(w, r, http.StatusInternalServerError, "LEGACY_ADAPTER_ERROR", "业务响应转换失败")
			return
		}
		if buffer.status >= 400 || !legacy.OK {
			code := "BUSINESS_ERROR"
			if buffer.status == http.StatusBadRequest {
				code = "VALIDATION_ERROR"
			}
			if buffer.status == http.StatusNotFound {
				code = "NOT_FOUND"
			}
			v1Error(w, r, buffer.status, code, legacy.Message)
			return
		}
		v1Data(w, r, buffer.status, legacy.Data)
	}
}

func adminAction(method, path string) string {
	return strings.ToLower(method) + ":" + path
}

func adminResource(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return "admin"
	}
	return parts[0]
}

func (s *Server) wrapAdminV1(method, path string, handler http.HandlerFunc) http.Handler {
	adapted := adminV1Legacy(handler)
	if method != http.MethodGet {
		adapted = s.adminV1Audit(adminAction(method, path), adminResource(path), adapted)
	}
	return adapted
}

func adminV1NotImplemented(w http.ResponseWriter, r *http.Request) {
	v1Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", fmt.Sprintf("%s 尚未在当前阶段实现", r.URL.Path))
}
