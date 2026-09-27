package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
	"zonenan-backend/internal/adminauth"
)

func adminManagementError(w http.ResponseWriter, r *http.Request, err error) {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, adminauth.ErrManagementDenied):
		v1Error(w, r, 403, "PERMISSION_DENIED", "没有管理管理员与权限的权限")
	case errors.Is(err, adminauth.ErrLastSuperAdmin):
		v1Error(w, r, 400, "LAST_SUPER_ADMIN", "至少保留一位启用的超级管理员")
	case errors.Is(err, adminauth.ErrManagementValidation):
		v1Error(w, r, 400, "VALIDATION_ERROR", "请检查邮箱、名称、密码长度及角色和权限；super_admin 为受保护角色")
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		v1Error(w, r, 409, "ALREADY_EXISTS", "邮箱或角色键名已存在")
	default:
		v1Error(w, r, 500, "ADMIN_WRITE_FAILED", "保存失败，请稍后重试")
	}
}

func (s *Server) handleAdminV1CreateAdmin(w http.ResponseWriter, r *http.Request) {
	var input adminauth.NewAdmin
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input); err != nil {
		v1Error(w, r, 400, "INVALID_JSON", "请求格式无效")
		return
	}
	id, err := s.adminAccounts.CreateAdmin(r.Context(), adminPrincipalFrom(r).ID, input)
	if err != nil {
		adminManagementError(w, r, err)
		return
	}
	v1Data(w, r, http.StatusCreated, map[string]any{"id": id})
}
func (s *Server) handleAdminV1Permissions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.adminAccounts.ListPermissions(r.Context())
	if err != nil {
		adminManagementError(w, r, err)
		return
	}
	v1Data(w, r, 200, rows)
}
func (s *Server) handleAdminV1SaveRole(w http.ResponseWriter, r *http.Request) {
	var input adminauth.RoleInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input); err != nil {
		v1Error(w, r, 400, "INVALID_JSON", "请求格式无效")
		return
	}
	if err := s.adminAccounts.SaveRole(r.Context(), adminPrincipalFrom(r).ID, input, r.Method == http.MethodPost); err != nil {
		adminManagementError(w, r, err)
		return
	}
	v1Data(w, r, 200, map[string]any{"key": input.Key})
}
