package httpapi

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"zonenan-backend/internal/store"
)

func TestAdminSaveWhatsNewRejectsUnsupportedAction(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/releases/whats-new", strings.NewReader(`{"id":1,"whats_new":{"title":"新功能","items":[{"title":"提醒","body":"查看课表","platforms":["android"],"action_id":"open_any_url","action_label":"打开"}]}}`))
	resp := httptest.NewRecorder()
	(&Server{}).handleAdminSaveWhatsNew(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid action status = %d, want 400", resp.Code)
	}
}

func TestReleaseChangelogHistoryIncludesGuideWithoutArtifact(t *testing.T) {
	guide := &store.ReleaseWhatsNew{Title: "课表通知", Items: []store.ReleaseWhatsNewItem{{Title: "课前提醒", Body: "提前收到提醒"}}}
	history := releaseChangelogHistory([]store.AppRelease{{
		Version: "2.4.0", BuildNumber: 20, Channel: store.ReleaseStable,
		WhatsNew: guide, AndroidURL: "https://example.test/app.apk",
	}})
	if len(history) != 1 || history[0]["whats_new"] != guide {
		t.Fatalf("unexpected changelog history: %#v", history)
	}
	if _, leaked := history[0]["android_url"]; leaked {
		t.Fatal("public changelog must not expose download metadata")
	}
}

func TestReleaseChannelsForViewer(t *testing.T) {
	cases := []struct {
		name   string
		viewer store.AudienceViewer
		want   []string
	}{
		{"anonymous", store.AudienceViewer{}, []string{store.ReleaseStable}},
		{"release beta", store.AudienceViewer{IsReleaseBeta: true}, []string{store.ReleaseStable, store.ReleaseBeta, store.ReleaseRC}},
		{"premium does not grant release beta", store.AudienceViewer{IsPremium: true}, []string{store.ReleaseStable}},
		{"admin", store.AudienceViewer{IsAdmin: true}, []string{store.ReleaseStable, store.ReleaseBeta, store.ReleaseRC, store.ReleaseInternal}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseChannelsForViewer(tc.viewer); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("releaseChannelsForViewer() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestReleaseUpdateVisibleUsesReleaseMembership(t *testing.T) {
	if releaseUpdateVisible(store.AudienceViewer{IsPremium: true}, "beta") {
		t.Fatal("premium entitlement must not grant release beta access")
	}
	if !releaseUpdateVisible(store.AudienceViewer{IsReleaseBeta: true}, "beta") {
		t.Fatal("release beta membership should pass beta update gate")
	}
	if releaseUpdateVisible(store.AudienceViewer{}, "disabled") {
		t.Fatal("disabled update gate should hide releases")
	}
}
