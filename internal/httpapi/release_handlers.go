package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"zonenan-backend/internal/store"
)

func (s *Server) handleAdminReleases(w http.ResponseWriter, r *http.Request) {
	includeDeleted := r.URL.Query().Get("include_deleted") == "true"
	list, err := s.releases.All(r.Context(), includeDeleted)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, list)
}

func (s *Server) handleAdminInspectAPK(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !decodeJSON(w, r, &req) || strings.TrimSpace(req.URL) == "" {
		Fail(w, http.StatusBadRequest, "缺少 APK URL")
		return
	}
	artifact, err := s.artifact.Verify(r.Context(), req.URL)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	OK(w, artifact)
}

func (s *Server) handleAdminSaveRelease(w http.ResponseWriter, r *http.Request) {
	var release store.AppRelease
	if !decodeJSON(w, r, &release) {
		return
	}
	var artifact *store.APKArtifact
	if strings.TrimSpace(release.AndroidURL) != "" {
		verified, err := s.artifact.Verify(r.Context(), release.AndroidURL)
		if err != nil {
			Fail(w, http.StatusBadRequest, err.Error())
			return
		}
		// The downloaded artifact is canonical. Never compare or accept
		// administrator-supplied version/build/hash values.
		release.AndroidURL = verified.SourceURL
		release.Version = verified.VersionName
		release.BuildNumber = verified.VersionCode
		release.AndroidVersionName = verified.VersionName
		release.AndroidVersionCode = verified.VersionCode
		release.AndroidSizeBytes = verified.SizeBytes
		release.AndroidSHA256 = verified.SHA256
		release.AndroidVerifiedAt = &verified.VerifiedAt
		artifact = &verified
	}
	if err := store.ValidateAppRelease(release); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.releases.Save(r.Context(), release)
	if errors.Is(err, store.ErrBuildNumberNotIncreasing) {
		Fail(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, store.ErrArtifactMetadataRequired) {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		Fail(w, http.StatusNotFound, "发布记录不存在")
		return
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	OK(w, map[string]interface{}{"id": id, "channel": release.Channel, "artifact": artifact})
}

func (s *Server) handleAdminSaveWhatsNew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       int64                  `json:"id"`
		WhatsNew *store.ReleaseWhatsNew `json:"whats_new"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ID <= 0 {
		Fail(w, http.StatusBadRequest, "缺少发布记录 id")
		return
	}
	if err := store.ValidateReleaseWhatsNew(req.WhatsNew); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.releases.UpdateWhatsNew(r.Context(), req.ID, req.WhatsNew); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(w, http.StatusNotFound, "发布记录不存在")
		} else {
			Fail(w, http.StatusInternalServerError, "保存本版亮点失败")
		}
		return
	}
	OK(w, map[string]interface{}{"id": req.ID, "whats_new": req.WhatsNew})
}

func (s *Server) handleAdminBackfillRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version                 string `json:"version"`
		BuildNumber             int    `json:"build_number"`
		Channel                 string `json:"channel"`
		Changelog               string `json:"changelog"`
		MinSupportedVersion     string `json:"min_supported"`
		MinSupportedBuildNumber int    `json:"min_supported_build_number"`
		ForceUpdate             bool   `json:"force"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	release := store.AppRelease{
		Version: req.Version, BuildNumber: req.BuildNumber, Channel: req.Channel,
		Changelog: req.Changelog, MinSupportedVersion: req.MinSupportedVersion,
		MinSupportedBuildNumber: req.MinSupportedBuildNumber, ForceUpdate: req.ForceUpdate,
	}
	if err := store.ValidateHistoricalAppRelease(release); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.releases.BackfillHistorical(r.Context(), release)
	if errors.Is(err, store.ErrReleaseAlreadyExists) || errors.Is(err, store.ErrBuildNumberAlreadyUsed) {
		Fail(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "补登失败")
		return
	}
	OK(w, map[string]interface{}{"id": id, "historical": true})
}

func (s *Server) handleAdminArchiveRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &req) || req.ID <= 0 {
		Fail(w, http.StatusBadRequest, "缺少 id")
		return
	}
	if err := s.releases.Archive(r.Context(), req.ID, true); err != nil {
		Fail(w, http.StatusInternalServerError, "归档失败")
		return
	}
	OK(w, map[string]interface{}{"id": req.ID, "archived": true})
}

func (s *Server) handleAdminRestoreRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &req) || req.ID <= 0 {
		Fail(w, http.StatusBadRequest, "缺少 id")
		return
	}
	if err := s.releases.Restore(r.Context(), req.ID); err != nil {
		Fail(w, http.StatusInternalServerError, "恢复失败")
		return
	}
	OK(w, map[string]interface{}{"id": req.ID, "archived": false})
}

func (s *Server) handleAdminDeleteRelease(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		Fail(w, http.StatusBadRequest, "缺少 id")
		return
	}
	if err := s.releases.Delete(r.Context(), id); err != nil {
		Fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	OK(w, map[string]interface{}{"id": id, "deleted": true})
}

func (s *Server) handleAdminReleaseBeta(w http.ResponseWriter, r *http.Request) {
	ids, err := s.releaseBeta.List(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, ids)
}

func (s *Server) handleAdminSetReleaseBeta(w http.ResponseWriter, r *http.Request) {
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
	if err := s.releaseBeta.SetMember(r.Context(), req.UserID, req.Member); err != nil {
		Fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	OK(w, map[string]interface{}{"user_id": req.UserID, "member": req.Member})
}

func (s *Server) handleAdminDeleteReleaseBeta(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || id <= 0 {
		Fail(w, http.StatusBadRequest, "缺少 user_id")
		return
	}
	if err := s.releaseBeta.SetMember(r.Context(), id, false); err != nil {
		Fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	OK(w, map[string]int64{"user_id": id})
}
