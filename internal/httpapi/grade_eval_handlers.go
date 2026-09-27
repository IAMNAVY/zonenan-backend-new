package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"zonenan-backend/internal/store"
)

type evaluateReq struct {
	ProfileID   int64   `json:"profile_id"`
	Rating      float64 `json:"rating"`
	Comment     string  `json:"comment"`
	IsAnonymous bool    `json:"is_anonymous"`
}

// handleGradeEvaluate:提交/编辑评教(每人每档 ≤3 次编辑)。需可查(贡献者)。
func (s *Server) handleGradeEvaluate(w http.ResponseWriter, r *http.Request) {
	if !s.checkCanQuery(w, r) {
		return
	}
	var req evaluateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ProfileID <= 0 {
		Fail(w, http.StatusBadRequest, "缺少课程")
		return
	}
	if req.Rating < 0 || req.Rating > 5 {
		Fail(w, http.StatusBadRequest, "评分需在 0-5 之间")
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if len([]rune(comment)) > 500 {
		Fail(w, http.StatusBadRequest, "评论最长 500 字")
		return
	}
	err := s.grades.Evaluate(r.Context(), req.ProfileID, userIDFrom(r), req.Rating, comment, req.IsAnonymous)
	if errors.Is(err, store.ErrEditLimit) {
		Fail(w, http.StatusForbidden, err.Error())
		return
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "提交失败")
		return
	}
	OK(w, map[string]bool{"ok": true})
}

type deleteEvalReq struct {
	EvaluationID int64 `json:"evaluation_id"`
}

// handleGradeEvaluateDelete:软删除自己的评教。
func (s *Server) handleGradeEvaluateDelete(w http.ResponseWriter, r *http.Request) {
	var req deleteEvalReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.grades.DeleteEvaluation(r.Context(), req.EvaluationID, userIDFrom(r)); err != nil {
		Fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	OK(w, map[string]bool{"ok": true})
}

type updateEvalReq struct {
	EvaluationID int64  `json:"evaluation_id"`
	Comment      string `json:"comment"`
}

// handleGradeEvaluateUpdate:编辑自己的评教内容。
func (s *Server) handleGradeEvaluateUpdate(w http.ResponseWriter, r *http.Request) {
	var req updateEvalReq
	if !decodeJSON(w, r, &req) {
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if len([]rune(comment)) > 500 {
		Fail(w, http.StatusBadRequest, "评论最长 500 字")
		return
	}
	if err := s.grades.UpdateEvaluation(r.Context(), req.EvaluationID, userIDFrom(r), comment); err != nil {
		Fail(w, http.StatusInternalServerError, "修改失败")
		return
	}
	OK(w, map[string]bool{"ok": true})
}
