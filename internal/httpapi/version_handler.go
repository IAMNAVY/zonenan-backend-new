package httpapi

import (
	"context"
	"net/http"
	"net/url"

	"zonenan-backend/internal/store"
)

var appFeatureDefaults = map[string]string{
	"grade":               "enabled",
	"calendar_sync":       "disabled",
	"permission_center":   "disabled",
	"windows_reminder":    "disabled",
	"membership_center":   "disabled",
	"post_upgrade_guide":  "disabled",
	"personalization_lab": "disabled",
}

func (s *Server) configuredFeatureState(r *http.Request, key string) string {
	def := appFeatureDefaults[key]
	return normalizeReleaseState(s.setting.GetStr(r.Context(), "feature_"+key+"_state", def))
}

func normalizeReleaseState(state string) string {
	switch state {
	case "beta", "disabled", "admin":
		return state
	default:
		return "enabled"
	}
}

func normalizeUpdateMode(mode string) string {
	if mode == store.UpdateSilent {
		return store.UpdateSilent
	}
	return store.UpdatePopup
}

// Client query parameters, including APP_CHANNEL, are deliberately ignored.
func releaseChannelsForViewer(viewer store.AudienceViewer) []string {
	if viewer.IsAdmin {
		return []string{store.ReleaseStable, store.ReleaseBeta, store.ReleaseRC, store.ReleaseInternal}
	}
	if viewer.IsReleaseBeta {
		return []string{store.ReleaseStable, store.ReleaseBeta, store.ReleaseRC}
	}
	return []string{store.ReleaseStable}
}

func releaseUpdateVisible(viewer store.AudienceViewer, state string) bool {
	switch state {
	case "enabled":
		return true
	case "beta":
		return viewer.IsReleaseBeta || viewer.IsAdmin
	case "admin":
		return viewer.IsAdmin
	default:
		return false
	}
}

type appVersionSettings interface {
	GetStr(context.Context, string, string) string
	GetBool(context.Context, string, bool) bool
}

type appVersionReleases interface {
	Latest(context.Context, string, []string) (*store.AppRelease, error)
}

// handleAppVersion:热更新版本检查 + 应用配置。公开(无需登录)。
func (s *Server) handleAppVersion(w http.ResponseWriter, r *http.Request) {
	response := buildAppVersionResponse(
		r.Context(), r.URL.Query(), s.audienceViewer(r), s.setting, s.releases,
	)
	OK(w, response)
}

func buildAppVersionResponse(
	ctx context.Context,
	query url.Values,
	viewer store.AudienceViewer,
	settings appVersionSettings,
	releases appVersionReleases,
) map[string]interface{} {
	updateState := normalizeReleaseState(settings.GetStr(ctx, "update_release_state", "enabled"))
	updateVisible := viewer.CanSee(updateState)
	response := map[string]interface{}{
		"latest":        settings.GetStr(ctx, "app_latest_version", ""),
		"min_supported": settings.GetStr(ctx, "app_min_supported_version", ""),
		"apk_url":       settings.GetStr(ctx, "app_apk_url", ""),
		"apk_sha256":    settings.GetStr(ctx, "app_apk_sha256", ""),
		"changelog":     settings.GetStr(ctx, "app_changelog", ""),
		"force":         settings.GetBool(ctx, "app_force_update", false),
		"feedback_url":  settings.GetStr(ctx, "app_feedback_url", ""),
		"update_state":  updateState,
	}

	if !viewer.CanSee(updateState) {
		response["latest"] = ""
		response["min_supported"] = ""
		response["apk_url"] = ""
		response["apk_sha256"] = ""
		response["changelog"] = ""
		response["force"] = false
	}

	// The presence of any version query parameter identifies the new client.
	// Legacy callers (including Website) commonly send no query at all and must
	// receive only the original field set, while still reading current releases.
	newClient := query.Has("platform") || query.Has("version") || query.Has("build_number")
	if newClient {
		response["update_mode"] = normalizeUpdateMode(settings.GetStr(ctx, "app_update_mode", store.UpdatePopup))
	}
	lookupPlatform := query.Get("platform")
	if lookupPlatform == "" {
		// Legacy response fields contain the Android download URL, so Android is
		// the useful default when no platform was supplied.
		lookupPlatform = "android"
	}

	if updateVisible && releaseUpdateVisible(viewer, updateState) && releases != nil {
		channels := []string{store.ReleaseStable}
		if newClient {
			channels = releaseChannelsForViewer(viewer)
		}
		if release, err := releases.Latest(ctx, lookupPlatform, channels); err == nil {
			if newClient {
				applyReleaseVersion(response, release, lookupPlatform)
			} else {
				applyLegacyReleaseVersion(response, release)
			}
		} else if newClient {
			// Keep this compatibility hint only for clients that understand the
			// Release Record response. Legacy callers must not see new fields.
			response["target_channel"] = store.ReleaseStable
		}
	}

	features := map[string]map[string]interface{}{}
	for _, key := range []string{"grade", "calendar_sync", "permission_center", "windows_reminder", "membership_center", "post_upgrade_guide", "personalization_lab"} {
		state := normalizeReleaseState(settings.GetStr(ctx, "feature_"+key+"_state", appFeatureDefaults[key]))
		features[key] = map[string]interface{}{
			"state": state, "experimental": key != "grade", "visible": viewer.CanSee(state),
		}
	}
	gradeState := normalizeReleaseState(settings.GetStr(ctx, "feature_grade_state", appFeatureDefaults["grade"]))
	features["grade"]["visible"] = viewer.CanSee(gradeState) && settings.GetBool(ctx, "grade_enabled", true)
	response["grade_enabled"] = features["grade"]["visible"]
	response["features"] = features
	return response
}

func applyLegacyReleaseVersion(response map[string]interface{}, release *store.AppRelease) {
	response["latest"] = release.Version
	response["min_supported"] = release.MinSupportedVersion
	response["apk_url"] = release.AndroidURL
	response["apk_sha256"] = release.AndroidSHA256
	response["changelog"] = release.Changelog
	response["force"] = release.ForceUpdate
}

func applyReleaseVersion(response map[string]interface{}, release *store.AppRelease, platform string) {
	response["latest"] = release.Version
	response["min_supported"] = release.MinSupportedVersion
	response["changelog"] = release.Changelog
	response["force"] = release.ForceUpdate
	response["update_mode"] = normalizeUpdateMode(release.UpdateMode)
	response["latest_build_number"] = release.BuildNumber
	response["min_supported_build_number"] = release.MinSupportedBuildNumber
	response["target_channel"] = release.Channel
	response["channel"] = release.Channel // compatibility alias for early clients
	response["release_id"] = release.ID
	response["android"] = map[string]interface{}{"url": release.AndroidURL, "sha256": release.AndroidSHA256}
	response["windows"] = map[string]interface{}{"url": release.WindowsURL, "sha256": release.WindowsSHA256}
	response["ios"] = map[string]interface{}{"url": release.IOSURL}
	switch platform {
	case "android":
		response["apk_url"] = release.AndroidURL
		response["apk_sha256"] = release.AndroidSHA256
	case "windows":
		response["download_url"] = release.WindowsURL
		response["download_sha256"] = release.WindowsSHA256
	case "ios":
		response["download_url"] = release.IOSURL
	}
}

// handleAppChangelog returns history without download metadata. The channel
// parameter is a filter inside the viewer's authorized set, never an upgrade.
func (s *Server) handleAppChangelog(w http.ResponseWriter, r *http.Request) {
	viewer := s.audienceViewer(r)
	requested := r.URL.Query().Get("channel")
	channels := releaseChannelsForViewer(viewer)
	allowed := map[string]bool{}
	for _, channel := range channels {
		allowed[channel] = true
	}
	if requested != "" && allowed[requested] {
		channels = []string{requested}
	} else if requested != "" {
		if viewer.IsReleaseBeta || viewer.IsAdmin {
			// A known-but-unauthorized preview channel returns no records rather
			// than silently substituting a different audience's release.
			channels = []string{}
		} else {
			// Public clients remain on stable even when they send a channel query.
			requested = store.ReleaseStable
			channels = []string{store.ReleaseStable}
		}
	}
	list, err := s.releases.List(r.Context(), requested, channels)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, map[string]interface{}{"history": releaseChangelogHistory(list)})
}

func releaseChangelogHistory(list []store.AppRelease) []map[string]interface{} {
	history := make([]map[string]interface{}, 0, len(list))
	for _, release := range list {
		history = append(history, map[string]interface{}{
			"id": release.ID, "version": release.Version, "build_number": release.BuildNumber,
			"channel": release.Channel, "changelog": release.Changelog,
			"whats_new": release.WhatsNew,
			"active":    release.Active, "archived_at": release.ArchivedAt,
			"created_at": release.CreatedAt,
		})
	}
	return history
}
