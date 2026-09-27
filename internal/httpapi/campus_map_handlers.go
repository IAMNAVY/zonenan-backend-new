package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"zonenan-backend/internal/store"
)

func (s *Server) handleCampusMapManifest(w http.ResponseWriter, r *http.Request) {
	manifest, err := s.campusMap.Manifest(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询校园地图版本失败")
		return
	}
	// Manifest 很小且必须及时反映管理员更新；允许中间缓存重验证，但不直接
	// 使用陈旧响应。完整地点列表则通过 data_version 查询参数自然换 URL。
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"`+manifest.DataVersion+`"`)
	OK(w, manifest)
}

func (s *Server) handleCampusMapPlaces(w http.ResponseWriter, r *http.Request) {
	// 先读版本再读地点。若两次查询之间发生更新，客户端最多下一次再拉一遍，
	// 不会把旧地点误标成新版本而永久漏掉更新。
	manifest, err := s.campusMap.Manifest(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询校园地图版本失败")
		return
	}
	campusID := strings.TrimSpace(r.URL.Query().Get("campus_id"))
	places, err := s.campusMap.Active(r.Context(), campusID)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询校园地点失败")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	OK(w, map[string]any{
		"schema_version": manifest.SchemaVersion,
		"data_version":   manifest.DataVersion,
		"generated_at":   time.Now().UTC(),
		"places":         places,
	})
}

func (s *Server) handleCampusMapContribution(w http.ResponseWriter, r *http.Request) {
	var contribution store.CampusMapContribution
	if !decodeJSON(w, r, &contribution) {
		return
	}
	saved, err := s.campusMapContrib.Submit(r.Context(), userIDFrom(r), contribution)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	OK(w, saved)
}

func (s *Server) handleAdminCampusMapPlaces(w http.ResponseWriter, r *http.Request) {
	places, err := s.campusMap.All(r.Context(), r.URL.Query().Get("campus_id"))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询校园地点失败")
		return
	}
	OK(w, places)
}

func (s *Server) handleAdminSaveCampusMapPlace(w http.ResponseWriter, r *http.Request) {
	var place store.CampusMapPlace
	if !decodeJSON(w, r, &place) {
		return
	}
	place = store.NormalizeCampusMapPlace(place)
	if err := store.ValidateCampusMapPlace(place); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.campusMap.Upsert(r.Context(), place)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "保存校园地点失败")
		return
	}
	OK(w, saved)
}

func (s *Server) handleAdminDeactivateCampusMapPlace(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		Fail(w, http.StatusBadRequest, "缺少有效地点 id")
		return
	}
	if err := s.campusMap.Deactivate(r.Context(), id); err != nil {
		Fail(w, http.StatusInternalServerError, "下线校园地点失败")
		return
	}
	OK(w, map[string]any{"id": id, "active": false})
}

func (s *Server) handleAdminCampusMapContributions(w http.ResponseWriter, r *http.Request) {
	items, err := s.campusMapContrib.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询地图上报失败")
		return
	}
	OK(w, items)
}

func (s *Server) handleAdminReviewCampusMapContribution(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID            int64                 `json:"id"`
		Decision      string                `json:"decision"`
		Note          string                `json:"review_note"`
		ProposedPlace *store.CampusMapPlace `json:"proposed_place,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.campusMapContrib.Review(r.Context(), request.ID, request.Decision, request.Note, request.ProposedPlace); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	OK(w, map[string]any{"id": request.ID, "status": request.Decision})
}
