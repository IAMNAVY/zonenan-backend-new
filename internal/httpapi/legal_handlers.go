package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"zonenan-backend/internal/store"
)

// handleLegalDoc 取某类型协议文档的当前版本。公开(无需登录)。
// GET /legal/{docType}  docType ∈ user_agreement|privacy_policy|grade_consent
func (s *Server) handleLegalDoc(w http.ResponseWriter, r *http.Request) {
	docType := chi.URLParam(r, "docType")
	doc, err := s.legal.GetLatest(r.Context(), docType)
	if errors.Is(err, store.ErrNotFound) {
		Fail(w, http.StatusNotFound, "文档不存在")
		return
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取文档失败")
		return
	}
	OK(w, doc)
}

// handleLegalVersions 返回各协议当前版本号,供 App 检测更新。公开。
func (s *Server) handleLegalVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.legal.LatestVersions(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取版本失败")
		return
	}
	OK(w, versions)
}
