package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"zonenan-backend/internal/afdian"
	"zonenan-backend/internal/store"
)

func (s *Server) handleAfdianWebhook(w http.ResponseWriter, r *http.Request) {
	secret := chi.URLParam(r, "secret")
	configured := s.cfg.AfdianWebhookPathSecret
	if configured == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(configured)) != 1 {
		http.NotFound(w, r)
		return
	}
	var payload afdian.Webhook
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAfdianResponse(w, http.StatusBadRequest, 400001, "invalid json")
		return
	}
	if payload.EC != 200 || payload.Data.Type != "order" || payload.Data.Order.OutTradeNo == "" {
		writeAfdianResponse(w, http.StatusBadRequest, 400001, "invalid order payload")
		return
	}
	// Afdian's newer Webhook payloads include an RSA signature. Keep the
	// verification fail-closed when a key is configured, while allowing older
	// deployments to use the URL path secret until they obtain the public key.
	if publicKey := strings.TrimSpace(s.cfg.AfdianWebhookPublicKey); publicKey != "" {
		if err := afdian.VerifyWebhookSignature(payload.Data.Order, payload.Data.Sign, publicKey); err != nil {
			writeAfdianResponse(w, http.StatusBadRequest, 400005, "invalid signature")
			return
		}
	}
	if _, err := s.membership.ProcessOrder(r.Context(), payload.Data.Order, "afdian_webhook"); err != nil {
		log.Printf("afdian webhook order processing failed: %v", err)
		writeAfdianResponse(w, http.StatusInternalServerError, 500000, "temporary server error")
		return
	}
	// The event is durably recorded even when it is unmatched; the admin panel
	// can review it and the API sync can retry it later.
	writeAfdianResponse(w, http.StatusOK, 200, "")
}

func writeAfdianResponse(w http.ResponseWriter, status, ec int, em string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ec": ec, "em": em})
}

func (s *Server) handleAdminAfdianSync(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AfdianAPIUserID == "" || s.cfg.AfdianAPIToken == "" {
		Fail(w, http.StatusConflict, "爱发电 API 尚未配置")
		return
	}
	pages := 0
	processed := 0
	unmatched := 0
	ignored := 0
	maxPages := 1000
	for page := 1; page <= maxPages; page++ {
		orders, totalPage, err := s.afdian.QueryOrders(r.Context(), page)
		if err != nil {
			Fail(w, http.StatusBadGateway, "爱发电 API 请求失败")
			return
		}
		pages = page
		for _, order := range orders {
			result, err := s.membership.ProcessOrder(r.Context(), order, "afdian_sync")
			if err != nil {
				Fail(w, http.StatusInternalServerError, "订单处理失败")
				return
			}
			switch result.Status {
			case "processed":
				processed++
			case "unmatched":
				unmatched++
			default:
				ignored++
			}
		}
		if totalPage <= page || len(orders) == 0 {
			break
		}
	}
	OK(w, map[string]any{"pages": pages, "processed": processed, "unmatched": unmatched, "ignored": ignored})
}

func (s *Server) handleMembershipMe(w http.ResponseWriter, r *http.Request) {
	status, err := s.membership.Status(r.Context(), userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询会员状态失败")
		return
	}
	OK(w, status)
}

func (s *Server) handleMembershipActivate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len([]rune(strings.TrimSpace(req.Code))) < 10 || len([]rune(req.Code)) > 80 {
		Fail(w, http.StatusBadRequest, "激活码格式无效")
		return
	}
	if err := s.membership.RedeemActivation(r.Context(), userIDFrom(r), req.Code); err != nil {
		Fail(w, http.StatusBadRequest, "激活码无效或已使用")
		return
	}
	status, err := s.membership.Status(r.Context(), userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询会员状态失败")
		return
	}
	OK(w, status)
}

func (s *Server) handleAdminMembershipOverview(w http.ResponseWriter, r *http.Request) {
	data, err := s.membership.Overview(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, data)
}

func (s *Server) handleAdminMembershipTypes(w http.ResponseWriter, r *http.Request) {
	items, err := s.membership.ListTypes(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, items)
}

func (s *Server) handleAdminSaveMembershipType(w http.ResponseWriter, r *http.Request) {
	var item store.MembershipType
	if !decodeJSON(w, r, &item) {
		return
	}
	if err := s.membership.UpsertType(r.Context(), item); err != nil {
		Fail(w, http.StatusBadRequest, "会员类型参数无效")
		return
	}
	OK(w, map[string]bool{"saved": true})
}

func (s *Server) handleAdminMembershipMembers(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.membership.ListMembers(r.Context(), limit)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, items)
}

func (s *Server) handleAdminMembershipEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.membership.ListEvents(r.Context(), limit)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, items)
}

func (s *Server) handleAdminMembershipActivationCodes(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	codes, err := s.membership.ListActivationCodes(r.Context(), strings.TrimSpace(r.URL.Query().Get("state")), limit)
	if err != nil {
		Fail(w, http.StatusBadRequest, "查询激活码失败")
		return
	}
	OK(w, codes)
}

func (s *Server) handleAdminDisableActivationCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64  `json:"id"`
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.membership.DisableActivationCode(r.Context(), req.ID, "admin", req.Reason); err != nil {
		Fail(w, http.StatusBadRequest, "禁用激活码失败")
		return
	}
	OK(w, map[string]any{"id": req.ID, "disabled": true})
}

func (s *Server) handleAdminDeleteActivationCode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil {
		Fail(w, http.StatusBadRequest, "激活码 ID 无效")
		return
	}
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if reason == "" {
		reason = "管理员软删除激活码"
	}
	if err := s.membership.DisableActivationCode(r.Context(), id, "admin", reason); err != nil {
		Fail(w, http.StatusBadRequest, "删除激活码失败")
		return
	}
	OK(w, map[string]any{"id": id, "deleted": true, "soft_deleted": true})
}

func (s *Server) handleAdminMembershipGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID    int64  `json:"user_id"`
		TypeKey   string `json:"type_key"`
		Reason    string `json:"reason"`
		ExpiresAt string `json:"expires_at"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ExpiresAt == "" {
		req.ExpiresAt = store.LongTermExpiry().Format(time.RFC3339)
	}
	expires, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err != nil || !expires.After(time.Now()) {
		Fail(w, http.StatusBadRequest, "有效期无效")
		return
	}
	if _, err := s.users.GetByID(r.Context(), req.UserID); err != nil {
		Fail(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err := s.membership.GrantManual(r.Context(), req.UserID, req.TypeKey, "admin", req.Reason, expires); err != nil {
		Fail(w, http.StatusBadRequest, "授权失败")
		return
	}
	OK(w, map[string]any{"user_id": req.UserID, "type_key": req.TypeKey, "expires_at": expires})
}

func (s *Server) handleAdminMembershipRevoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID  int64  `json:"user_id"`
		TypeKey string `json:"type_key"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.membership.Revoke(r.Context(), req.UserID, req.TypeKey, "admin", req.Reason); err != nil {
		Fail(w, http.StatusBadRequest, "撤销失败")
		return
	}
	OK(w, map[string]any{"user_id": req.UserID, "type_key": req.TypeKey})
}

func (s *Server) handleAdminMembershipGrantExpiry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GrantID   int64  `json:"grant_id"`
		ExpiresAt string `json:"expires_at"`
		Reason    string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	expires, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt))
	if err != nil {
		Fail(w, http.StatusBadRequest, "有效期格式无效")
		return
	}
	if err := s.membership.UpdateGrantExpiry(r.Context(), req.GrantID, expires, "admin", req.Reason); err != nil {
		Fail(w, http.StatusBadRequest, "修改会员有效期失败")
		return
	}
	OK(w, map[string]any{"grant_id": req.GrantID, "expires_at": expires})
}

func (s *Server) handleAdminRevokeMembershipGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GrantID int64  `json:"grant_id"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.membership.RevokeGrant(r.Context(), req.GrantID, "admin", req.Reason); err != nil {
		Fail(w, http.StatusBadRequest, "撤销会员记录失败")
		return
	}
	OK(w, map[string]any{"grant_id": req.GrantID, "revoked": true})
}

func (s *Server) handleAdminGenerateActivationCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TypeKey   string `json:"type_key"`
		OrderID   string `json:"order_id"`
		ExpiresAt string `json:"expires_at"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var expires *time.Time
	if strings.TrimSpace(req.ExpiresAt) != "" {
		value, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil || !value.After(time.Now()) {
			Fail(w, http.StatusBadRequest, "激活码有效期无效")
			return
		}
		expires = &value
	}
	code, err := s.membership.GenerateActivationCode(r.Context(), req.TypeKey, req.OrderID, "admin", expires)
	if err != nil {
		Fail(w, http.StatusBadRequest, "生成激活码失败")
		return
	}
	OK(w, map[string]string{"code": code})
}
