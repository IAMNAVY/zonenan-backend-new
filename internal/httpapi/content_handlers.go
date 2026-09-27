package httpapi

import (
	"encoding/json"
	"net/http"

	"zonenan-backend/internal/store"
)

func (s *Server) audienceViewer(r *http.Request) store.AudienceViewer {
	uid := userIDFrom(r)
	if uid <= 0 {
		return store.AudienceViewer{}
	}
	u, err := s.users.GetByID(r.Context(), uid)
	if err != nil || u == nil {
		return store.AudienceViewer{}
	}
	legacyBeta := s.beta.IsMember(r.Context(), uid)
	premium, err := s.membership.IsActive(r.Context(), uid, "premium")
	if err != nil {
		premium = false
	}
	return store.AudienceViewer{
		UserID:        uid,
		IsPremium:     premium,
		IsBeta:        legacyBeta,
		IsReleaseBeta: s.releaseBeta.IsMember(r.Context(), uid),
		IsAdmin:       u.Role == "admin",
	}
}

// handleAnnouncements:公开返回当前生效且对当前身份可见的公告。
func (s *Server) handleAnnouncements(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	list, err := s.announce.ActiveByKind(r.Context(), kind, s.audienceViewer(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, list)
}

// handleHomeAds:公开返回当前生效的「我的」页广告位。
func (s *Server) handleHomeAds(w http.ResponseWriter, r *http.Request) {
	list, err := s.ads.Active(r.Context(), s.audienceViewer(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, list)
}

// --- 遥测上报(登录可选) ---

type telemetryEventReq struct {
	DeviceFP string          `json:"device_fingerprint"`
	Category string          `json:"category"` // grade | library
	Action   string          `json:"action"`
	Detail   json.RawMessage `json:"detail"`
}

// handleTelemetryEvent:接收一条行为埋点(给分/图书馆)。用于风控综合分析。
func (s *Server) handleTelemetryEvent(w http.ResponseWriter, r *http.Request) {
	var req telemetryEventReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Category == "" || req.Action == "" {
		Fail(w, http.StatusBadRequest, "缺少事件类型")
		return
	}
	var uidPtr *int64
	if uid := userIDFrom(r); uid > 0 {
		uidPtr = &uid
	}
	deviceFP := req.DeviceFP
	if deviceFP == "" {
		deviceFP = r.Header.Get("X-Device-Fingerprint")
	}
	detail := string(req.Detail)
	_ = s.telemetry.RecordEvent(r.Context(), uidPtr, deviceFP, clientIP(r), req.Category, req.Action, detail)
	// 埋点后同步跑一次轻量风控评估(不阻塞结果)。
	s.evaluateRisk(r, uidPtr, deviceFP, req.Category, req.Action)
	OK(w, map[string]bool{"ok": true})
}

type crashReportReq struct {
	AppVersion  string `json:"app_version"`
	Platform    string `json:"platform"`
	DeviceModel string `json:"device_model"`
	ErrorType   string `json:"error_type"`
	Message     string `json:"message"`
	Stack       string `json:"stack"`
}

// handleCrashReport:接收崩溃/错误上报(不含 PII)。
func (s *Server) handleCrashReport(w http.ResponseWriter, r *http.Request) {
	var req crashReportReq
	if !decodeJSON(w, r, &req) {
		return
	}
	var uidPtr *int64
	if uid := userIDFrom(r); uid > 0 {
		uidPtr = &uid
	}
	// 截断超长字段,防滥用撑爆存储。
	const maxLen = 8000
	if len(req.Stack) > maxLen {
		req.Stack = req.Stack[:maxLen]
	}
	if len(req.Message) > 2000 {
		req.Message = req.Message[:2000]
	}
	if err := s.telemetry.RecordCrash(r.Context(), store.CrashReport{
		UserID:      uidPtr,
		AppVersion:  req.AppVersion,
		Platform:    req.Platform,
		DeviceModel: req.DeviceModel,
		ErrorType:   req.ErrorType,
		Message:     req.Message,
		Stack:       req.Stack,
	}); err != nil {
		Fail(w, http.StatusInternalServerError, "上报保存失败")
		return
	}
	OK(w, map[string]bool{"ok": true})
}
