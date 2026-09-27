package httpapi

import (
	"net/http"
	"strings"
	"time"

	"zonenan-backend/internal/store"
)

type classroomArtifactRequest struct {
	TermID      string `json:"term_id"`
	DisplayName string `json:"display_name"`
	FirstMonday string `json:"first_monday"`
	TotalWeeks  int    `json:"total_weeks"`
	URL         string `json:"url"`
}

func (s *Server) handleClassroomManifest(w http.ResponseWriter, r *http.Request) {
	items, err := s.classroomData.Active(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询空教室数据失败")
		return
	}
	terms := make([]map[string]any, 0, len(items))
	for _, item := range items {
		firstMonday := dateOnly(item.FirstMonday)
		end := item.FirstMonday.AddDate(0, 0, item.TotalWeeks*7-1)
		terms = append(terms, map[string]any{
			"id":            item.ID,
			"term_id":       item.TermID,
			"display_name":  item.DisplayName,
			"first_monday":  firstMonday,
			"term_end_date": dateOnly(end),
			"total_weeks":   item.TotalWeeks,
			"url":           item.SourceURL,
			"sha256":        item.SHA256,
			"size_bytes":    item.SizeBytes,
			"verified_at":   item.VerifiedAt,
			"updated_at":    item.UpdatedAt,
		})
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	OK(w, map[string]any{
		"schema_version": 1,
		"generated_at":   time.Now().UTC(),
		"terms":          terms,
	})
}

func (s *Server) handleAdminClassroomData(w http.ResponseWriter, r *http.Request) {
	items, err := s.classroomData.All(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询空教室数据失败")
		return
	}
	OK(w, items)
}

func (s *Server) handleAdminSaveClassroomData(w http.ResponseWriter, r *http.Request) {
	var request classroomArtifactRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	firstMonday, err := time.Parse("2006-01-02", strings.TrimSpace(request.FirstMonday))
	if err != nil {
		Fail(w, http.StatusBadRequest, "第一周周一格式应为 YYYY-MM-DD")
		return
	}
	artifact := store.ClassroomArtifact{
		TermID:      strings.TrimSpace(request.TermID),
		DisplayName: strings.TrimSpace(request.DisplayName),
		FirstMonday: firstMonday,
		TotalWeeks:  request.TotalWeeks,
		SourceURL:   strings.TrimSpace(request.URL),
	}
	if err := store.ValidateClassroomArtifact(artifact); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	verified, err := s.classroomArtifact.Verify(r.Context(), artifact.SourceURL)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	artifact.SourceURL = verified.SourceURL
	artifact.SHA256 = verified.SHA256
	artifact.SizeBytes = verified.SizeBytes
	artifact.VerifiedAt = verified.VerifiedAt
	published, err := s.classroomData.Publish(r.Context(), artifact)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "发布空教室数据失败")
		return
	}
	OK(w, published)
}

func (s *Server) handleAdminArchiveClassroomData(w http.ResponseWriter, r *http.Request) {
	termID := strings.TrimSpace(r.URL.Query().Get("term_id"))
	if err := s.classroomData.Archive(r.Context(), termID); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	OK(w, map[string]string{"term_id": termID, "state": "archived"})
}

func dateOnly(value time.Time) string {
	return value.Format("2006-01-02")
}
