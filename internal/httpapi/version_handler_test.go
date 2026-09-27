package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"zonenan-backend/internal/store"
)

type fakeVersionSettings struct {
	strings map[string]string
	bools   map[string]bool
}

func (f fakeVersionSettings) GetStr(_ context.Context, key, def string) string {
	if value, ok := f.strings[key]; ok {
		return value
	}
	return def
}

func (f fakeVersionSettings) GetBool(_ context.Context, key string, def bool) bool {
	if value, ok := f.bools[key]; ok {
		return value
	}
	return def
}

type fakeVersionReleases struct {
	release  *store.AppRelease
	err      error
	platform string
	channels []string
}

func (f *fakeVersionReleases) Latest(_ context.Context, platform string, channels []string) (*store.AppRelease, error) {
	f.platform = platform
	f.channels = append([]string(nil), channels...)
	return f.release, f.err
}

func testVersionHandler(settings appVersionSettings, releases appVersionReleases) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		OK(w, buildAppVersionResponse(r.Context(), r.URL.Query(), store.AudienceViewer{}, settings, releases))
	})
}

func decodeVersionResponse(t *testing.T, handler http.Handler, target string) map[string]interface{} {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	result := struct {
		OK   bool                   `json:"ok"`
		Data map[string]interface{} `json:"data"`
	}{}
	if err := json.NewDecoder(recorder.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("version response is not successful")
	}
	return result.Data
}

func TestAppVersionWithoutQueryUsesLatestStableLegacyFields(t *testing.T) {
	settings := fakeVersionSettings{strings: map[string]string{
		"app_latest_version": "2.2.1",
		"app_apk_url":        "https://old.example/app.apk",
		"app_apk_sha256":     "old-sha",
		"app_changelog":      "old",
		"app_feedback_url":   "https://example/feedback",
	}, bools: map[string]bool{"app_force_update": false}}
	releases := &fakeVersionReleases{release: &store.AppRelease{
		Version: "2.3.0", BuildNumber: 2015, Channel: store.ReleaseStable,
		MinSupportedVersion: "2.1.0", AndroidURL: "https://new.example/app.apk",
		AndroidSHA256: "new-sha", Changelog: "new", ForceUpdate: true,
	}}

	response := decodeVersionResponse(t, testVersionHandler(settings, releases), "/app/version")
	for key, want := range map[string]interface{}{
		"latest":        "2.3.0",
		"min_supported": "2.1.0",
		"apk_url":       "https://new.example/app.apk",
		"apk_sha256":    "new-sha",
		"changelog":     "new",
		"force":         true,
		"feedback_url":  "https://example/feedback",
	} {
		if response[key] != want {
			t.Fatalf("response[%q] = %#v, want %#v", key, response[key], want)
		}
	}
	for _, key := range []string{"latest_build_number", "min_supported_build_number", "target_channel", "channel", "release_id", "android"} {
		if _, ok := response[key]; ok {
			t.Fatalf("legacy response unexpectedly contains new field %q", key)
		}
	}
	if releases.platform != "android" || !reflect.DeepEqual(releases.channels, []string{store.ReleaseStable}) {
		t.Fatalf("release lookup = platform %q channels %#v, want android/stable", releases.platform, releases.channels)
	}
}

func TestAppVersionWithVersionQueryUsesNewReleaseFields(t *testing.T) {
	settings := fakeVersionSettings{strings: map[string]string{
		"app_latest_version": "2.2.1",
	}}
	releases := &fakeVersionReleases{release: &store.AppRelease{
		ID: 42, Version: "2.3.0", BuildNumber: 2015, Channel: store.ReleaseStable,
		MinSupportedVersion: "2.1.0", MinSupportedBuildNumber: 100,
		AndroidURL: "https://new.example/app.apk", AndroidSHA256: "new-sha",
	}}

	response := decodeVersionResponse(t, testVersionHandler(settings, releases), "/app/version?platform=android&version=2.2.2&build_number=14")
	for _, key := range []string{"latest_build_number", "min_supported_build_number", "target_channel", "channel", "release_id", "android"} {
		if _, ok := response[key]; !ok {
			t.Fatalf("new response missing field %q: %#v", key, response)
		}
	}
	if response["latest_build_number"] != float64(2015) || response["target_channel"] != store.ReleaseStable {
		t.Fatalf("unexpected new response: %#v", response)
	}
	if releases.platform != "android" || !reflect.DeepEqual(releases.channels, []string{store.ReleaseStable}) {
		t.Fatalf("release lookup = platform %q channels %#v, want android/stable", releases.platform, releases.channels)
	}
}

func TestAppVersionFallsBackToSettingsWhenReleasesEmpty(t *testing.T) {
	settings := fakeVersionSettings{strings: map[string]string{
		"app_latest_version":        "2.2.1",
		"app_min_supported_version": "2.0.0",
		"app_apk_url":               "https://old.example/app.apk",
		"app_apk_sha256":            "old-sha",
		"app_changelog":             "legacy",
		"app_feedback_url":          "https://example/feedback",
	}, bools: map[string]bool{"app_force_update": true}}
	releases := &fakeVersionReleases{err: pgx.ErrNoRows}

	response := decodeVersionResponse(t, testVersionHandler(settings, releases), "/app/version")
	for key, want := range map[string]interface{}{
		"latest":        "2.2.1",
		"min_supported": "2.0.0",
		"apk_url":       "https://old.example/app.apk",
		"apk_sha256":    "old-sha",
		"changelog":     "legacy",
		"force":         true,
		"feedback_url":  "https://example/feedback",
	} {
		if response[key] != want {
			t.Fatalf("fallback response[%q] = %#v, want %#v", key, response[key], want)
		}
	}
	if _, ok := response["latest_build_number"]; ok {
		t.Fatal("legacy fallback unexpectedly contains new release fields")
	}
}

func TestAppVersionUsesReleaseUpdateModeForNewClients(t *testing.T) {
	settings := fakeVersionSettings{}
	releases := &fakeVersionReleases{release: &store.AppRelease{
		ID: 7, Version: "2.3.0", BuildNumber: 2300, Channel: store.ReleaseStable,
		AndroidURL: "https://new.example/app.apk", UpdateMode: store.UpdateSilent,
	}}

	response := decodeVersionResponse(t, testVersionHandler(settings, releases), "/app/version?platform=android&version=2.2.2&build_number=14")
	if response["update_mode"] != store.UpdateSilent {
		t.Fatalf("update_mode = %#v, want %q", response["update_mode"], store.UpdateSilent)
	}
}

func TestAppVersionNormalizesUnknownConfiguredUpdateMode(t *testing.T) {
	settings := fakeVersionSettings{strings: map[string]string{"app_update_mode": "banner"}}
	response := decodeVersionResponse(t, testVersionHandler(settings, &fakeVersionReleases{err: pgx.ErrNoRows}), "/app/version?platform=android")
	if response["update_mode"] != store.UpdatePopup {
		t.Fatalf("fallback update_mode = %#v, want %q", response["update_mode"], store.UpdatePopup)
	}
}

func TestAppVersionPostUpgradeGuideDefaultsDisabledAndCanBeEnabled(t *testing.T) {
	settings := fakeVersionSettings{}
	response := decodeVersionResponse(t, testVersionHandler(settings, &fakeVersionReleases{err: pgx.ErrNoRows}), "/app/version?platform=android")
	features, ok := response["features"].(map[string]interface{})
	if !ok {
		t.Fatalf("features = %#v", response["features"])
	}
	guide, ok := features["post_upgrade_guide"].(map[string]interface{})
	if !ok || guide["state"] != "disabled" || guide["visible"] != false {
		t.Fatalf("default post_upgrade_guide = %#v, want disabled and hidden", features["post_upgrade_guide"])
	}

	settings.strings = map[string]string{"feature_post_upgrade_guide_state": "enabled"}
	response = decodeVersionResponse(t, testVersionHandler(settings, &fakeVersionReleases{err: pgx.ErrNoRows}), "/app/version?platform=android")
	features = response["features"].(map[string]interface{})
	guide = features["post_upgrade_guide"].(map[string]interface{})
	if guide["state"] != "enabled" || guide["visible"] != true {
		t.Fatalf("enabled post_upgrade_guide = %#v, want enabled and visible", guide)
	}
}
