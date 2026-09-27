package httpapi

import (
	"net/http"
	"strconv"

	"zonenan-backend/internal/store"
)

// --- 公告管理 ---

func (s *Server) handleAdminAnnouncements(w http.ResponseWriter, r *http.Request) {
	list, err := s.announce.All(r.Context())
	if err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	OK(w, list)
}

func (s *Server) handleAdminSaveAnnouncement(w http.ResponseWriter, r *http.Request) {
	var a store.Announcement
	if !decodeJSON(w, r, &a) {
		return
	}
	if a.Kind == "" {
		a.Kind = "status_card"
	}
	if a.Level == "" {
		a.Level = "info"
	}
	if a.ReleaseState != "enabled" && a.ReleaseState != "beta" && a.ReleaseState != "disabled" && a.ReleaseState != "admin" {
		Fail(w, http.StatusBadRequest, "发布状态无效")
		return
	}
	id, err := s.announce.Upsert(r.Context(), a)
	if err != nil {
		Fail(w, 500, "保存失败")
		return
	}
	OK(w, map[string]int64{"id": id})
}

func (s *Server) handleAdminDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id == 0 {
		Fail(w, 400, "缺少 id")
		return
	}
	if err := s.announce.Delete(r.Context(), id); err != nil {
		Fail(w, 500, "删除失败")
		return
	}
	OK(w, map[string]int64{"deleted": id})
}

// --- 广告位管理 ---

func (s *Server) handleAdminAds(w http.ResponseWriter, r *http.Request) {
	list, err := s.ads.All(r.Context())
	if err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	OK(w, list)
}

func (s *Server) handleAdminSaveAd(w http.ResponseWriter, r *http.Request) {
	var a store.HomeAd
	if !decodeJSON(w, r, &a) {
		return
	}
	if a.OpenMode == "" {
		a.OpenMode = "in_app"
	}
	if a.ReleaseState != "enabled" && a.ReleaseState != "beta" && a.ReleaseState != "disabled" && a.ReleaseState != "admin" {
		Fail(w, http.StatusBadRequest, "发布状态无效")
		return
	}
	id, err := s.ads.Upsert(r.Context(), a)
	if err != nil {
		Fail(w, 500, "保存失败")
		return
	}
	OK(w, map[string]int64{"id": id})
}

func (s *Server) handleAdminDeleteAd(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id == 0 {
		Fail(w, 400, "缺少 id")
		return
	}
	if err := s.ads.Delete(r.Context(), id); err != nil {
		Fail(w, 500, "删除失败")
		return
	}
	OK(w, map[string]int64{"deleted": id})
}

// --- Beta 名单 ---

func (s *Server) handleAdminBeta(w http.ResponseWriter, r *http.Request) {
	ids, err := s.beta.List(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, ids)
}

func (s *Server) handleAdminSetBeta(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
		Member bool  `json:"member"`
	}
	if !decodeJSON(w, r, &req) || req.UserID <= 0 {
		Fail(w, http.StatusBadRequest, "缺少 user_id")
		return
	}
	if _, err := s.users.GetByID(r.Context(), req.UserID); err != nil {
		Fail(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err := s.beta.SetMember(r.Context(), req.UserID, req.Member); err != nil {
		Fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	OK(w, map[string]interface{}{"user_id": req.UserID, "member": req.Member})
}

func (s *Server) handleAdminDeleteBeta(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || id <= 0 {
		Fail(w, http.StatusBadRequest, "缺少 user_id")
		return
	}
	if err := s.beta.SetMember(r.Context(), id, false); err != nil {
		Fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	OK(w, map[string]int64{"user_id": id})
}

// --- 评教审核 ---

func (s *Server) handleAdminEvaluations(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	list, err := s.grades.ListEvaluationsForAdmin(r.Context(), page, 30)
	if err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	OK(w, list)
}

func (s *Server) handleAdminHideEvaluation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EvaluationID int64 `json:"evaluation_id"`
		Hidden       bool  `json:"hidden"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.EvaluationID == 0 {
		Fail(w, 400, "缺少 evaluation_id")
		return
	}
	if err := s.grades.SetEvaluationHidden(r.Context(), req.EvaluationID, req.Hidden); err != nil {
		Fail(w, 500, "操作失败")
		return
	}
	OK(w, map[string]interface{}{"evaluation_id": req.EvaluationID, "hidden": req.Hidden})
}

// --- 用户角色/白名单 ---

func (s *Server) handleAdminSetUserRole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID        int64  `json:"user_id"`
		Role          string `json:"role"`           // user | admin(可选)
		IsWhitelisted *bool  `json:"is_whitelisted"` // 可选
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.UserID == 0 {
		Fail(w, 400, "缺少 user_id")
		return
	}
	if req.Role == "user" || req.Role == "admin" {
		if err := s.users.SetRole(r.Context(), req.UserID, req.Role); err != nil {
			Fail(w, 500, "操作失败")
			return
		}
	}
	if req.IsWhitelisted != nil {
		if err := s.users.SetWhitelisted(r.Context(), req.UserID, *req.IsWhitelisted); err != nil {
			Fail(w, 500, "操作失败")
			return
		}
	}
	OK(w, map[string]bool{"ok": true})
}

// --- 风控看板 ---

func (s *Server) handleAdminRiskFlags(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	list, err := s.telemetry.ListRiskFlags(r.Context(), page, 50)
	if err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	OK(w, list)
}

func (s *Server) handleAdminResolveRiskFlag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.telemetry.ResolveRiskFlag(r.Context(), req.ID); err != nil {
		Fail(w, 500, "操作失败")
		return
	}
	OK(w, map[string]bool{"ok": true})
}

func (s *Server) handleAdminBlockDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceFP string `json:"device_fingerprint"`
		Block    bool   `json:"block"`
		Reason   string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DeviceFP == "" {
		Fail(w, 400, "缺少设备指纹")
		return
	}
	var err error
	if req.Block {
		err = s.telemetry.BlockDevice(r.Context(), req.DeviceFP, req.Reason)
	} else {
		err = s.telemetry.UnblockDevice(r.Context(), req.DeviceFP)
	}
	if err != nil {
		Fail(w, 500, "操作失败")
		return
	}
	OK(w, map[string]bool{"ok": true})
}
