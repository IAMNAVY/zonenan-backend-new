package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleAdminPushMessages(w http.ResponseWriter, r *http.Request) {
	messages, err := s.push.ListMessages(r.Context(), 100)
	if err != nil {
		v1Error(w, r, http.StatusInternalServerError, "PUSH_LIST_FAILED", "读取推送队列失败")
		return
	}
	v1Data(w, r, http.StatusOK, messages)
}

func (s *Server) handleAdminDeletePushMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "messageID"), 10, 64)
	if err != nil || s.push.DeleteMessage(r.Context(), id) != nil {
		v1Error(w, r, http.StatusBadRequest, "INVALID_PUSH_MESSAGE", "推送消息不存在")
		return
	}
	v1Data(w, r, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleRegisterPushDevice(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InstallationID string          `json:"installation_id"`
		Platform       string          `json:"platform"`
		Provider       string          `json:"provider"`
		Token          string          `json:"token"`
		PollSecret     string          `json:"poll_secret"`
		AppVersion     string          `json:"app_version"`
		Topics         map[string]bool `json:"topics"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.push.RegisterDevice(r.Context(), userIDFrom(r), request.InstallationID, request.Platform, request.Provider, request.Token, request.PollSecret, request.AppVersion, request.Topics); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	OK(w, map[string]bool{"registered": true})
}

func (s *Server) handlePollPushMessages(w http.ResponseWriter, r *http.Request) {
	secret := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	messages, err := s.push.Poll(r.Context(), r.URL.Query().Get("installation_id"), secret, 20)
	if err != nil {
		Fail(w, http.StatusUnauthorized, "推送轮询凭证无效")
		return
	}
	OK(w, map[string]any{"messages": messages})
}

func (s *Server) handleAcknowledgePushMessages(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InstallationID string  `json:"installation_id"`
		MessageIDs     []int64 `json:"message_ids"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	secret := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err := s.push.AcknowledgePolled(r.Context(), request.InstallationID, secret, request.MessageIDs); err != nil {
		Fail(w, http.StatusUnauthorized, "推送轮询凭证无效")
		return
	}
	OK(w, map[string]bool{"acknowledged": true})
}

func (s *Server) handlePushPreferences(w http.ResponseWriter, r *http.Request) {
	installationID := strings.TrimSpace(r.URL.Query().Get("installation_id"))
	preferences, err := s.push.Preferences(r.Context(), userIDFrom(r), installationID)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取推送设置失败")
		return
	}
	OK(w, map[string]any{"topics": preferences})
}

func (s *Server) handleDeletePushDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.push.RemoveDevice(r.Context(), userIDFrom(r), chi.URLParam(r, "installationID")); err != nil {
		Fail(w, http.StatusInternalServerError, "移除推送设备失败")
		return
	}
	OK(w, map[string]bool{"removed": true})
}

func (s *Server) handleAdminPushMessage(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Topic   string         `json:"topic"`
		Title   string         `json:"title"`
		Body    string         `json:"body"`
		Payload map[string]any `json:"payload"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	id, err := s.push.Enqueue(r.Context(), request.Topic, request.Title, request.Body, "", request.Payload)
	if err != nil {
		v1Error(w, r, http.StatusBadRequest, "INVALID_PUSH_MESSAGE", err.Error())
		return
	}
	s.pushDispatcher.Kick()
	v1Data(w, r, http.StatusCreated, map[string]any{"id": id, "status": "queued"})
}
