package httpapi

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func strptr(v string) *string { return &v }
func intptr(v int) *int       { return &v }

func validAnalyticsRequest(eventType string, now time.Time) analyticsEventRequest {
	return analyticsEventRequest{
		EventID:        "018f3f45-6e88-7d50-8d6a-41e4d19b8123",
		InstallationID: "018f3f45-6e88-7d50-8d6a-41e4d19b8456",
		SessionID:      "018f3f45-6e88-7d50-8d6a-41e4d19b8789",
		EventType:      eventType,
		OccurredAt:     now.Format(time.RFC3339Nano),
		Platform:       "android",
		AppVersion:     "1.2.3+42",
	}
}

func TestValidateAnalyticsDevice(t *testing.T) {
	brand := "Xiaomi"
	model := "2312DRA50C"
	os := "Android 15"
	device, err := validateAnalyticsDevice(&analyticsDeviceRequest{
		Platform: "android", OSVersion: &os, Brand: &brand, Model: &model,
	})
	if err != nil || device == nil || *device.Brand != brand || *device.Model != model {
		t.Fatalf("valid device rejected: %#v, %v", device, err)
	}
	tooLong := string(make([]byte, 65))
	if _, err := validateAnalyticsDevice(&analyticsDeviceRequest{
		Platform: "android", Brand: &tooLong,
	}); err == nil {
		t.Fatal("overlong device metadata accepted")
	}
	if _, err := validateAnalyticsDevice(&analyticsDeviceRequest{Platform: "plan9"}); err == nil {
		t.Fatal("unknown device platform accepted")
	}
}

func TestValidateAnalyticsEventShapes(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		e    analyticsEventRequest
	}{
		{"session start", func() analyticsEventRequest {
			e := validAnalyticsRequest("session_start", now)
			e.Reason = strptr("cold_start")
			return e
		}()},
		{"screen click", func() analyticsEventRequest {
			e := validAnalyticsRequest("screen_click", now)
			e.Screen = strptr("status")
			e.Source = strptr("bottom_nav")
			return e
		}()},
		{"screen view", func() analyticsEventRequest {
			e := validAnalyticsRequest("screen_view", now)
			e.Screen = strptr("news")
			e.Source = strptr("deep_link")
			return e
		}()},
		{"screen duration", func() analyticsEventRequest {
			e := validAnalyticsRequest("screen_duration", now)
			e.Screen = strptr("grades")
			e.DurationSeconds = intptr(0)
			return e
		}()},
		{"feature open", func() analyticsEventRequest {
			e := validAnalyticsRequest("feature_open", now)
			e.Feature = strptr("library")
			e.Source = strptr("functions_grid")
			return e
		}()},
		{"campus map feature open", func() analyticsEventRequest {
			e := validAnalyticsRequest("feature_open", now)
			e.Feature = strptr("campus_map")
			e.Source = strptr("mine_grid")
			return e
		}()},
		{"academic calendar duration", func() analyticsEventRequest {
			e := validAnalyticsRequest("screen_duration", now)
			e.Screen = strptr("academic_calendar")
			e.DurationSeconds = intptr(30)
			return e
		}()},
		{"feature success", func() analyticsEventRequest {
			e := validAnalyticsRequest("feature_result", now)
			e.Feature = strptr("library")
			e.Result = strptr("success")
			return e
		}()},
		{"feature error", func() analyticsEventRequest {
			e := validAnalyticsRequest("feature_result", now)
			e.Feature = strptr("library")
			e.Result = strptr("error")
			e.ErrorCategory = strptr("network")
			return e
		}()},
		{"ad impression", func() analyticsEventRequest {
			e := validAnalyticsRequest("ad_impression", now)
			e.PlacementID = strptr("home.hero")
			e.CampaignID = strptr("fall-2026")
			e.CreativeID = strptr("creative_1")
			return e
		}()},
		{"ad click", func() analyticsEventRequest {
			e := validAnalyticsRequest("ad_click", now)
			e.PlacementID = strptr("home.hero")
			e.CampaignID = strptr("fall-2026")
			e.CreativeID = strptr("creative_1")
			return e
		}()},
		{"widget render", func() analyticsEventRequest {
			e := validAnalyticsRequest("widget_render", now)
			e.WidgetKind = strptr("today")
			e.WidgetSize = strptr("large")
			e.WidgetRefreshMode = strptr("auto")
			return e
		}()},
		{"widget click", func() analyticsEventRequest {
			e := validAnalyticsRequest("widget_click", now)
			e.WidgetKind = strptr("weekly")
			e.WidgetSize = strptr("small")
			e.WidgetRefreshMode = strptr("manual")
			e.WidgetAction = strptr("next")
			return e
		}()},
		{"widget refresh failure", func() analyticsEventRequest {
			e := validAnalyticsRequest("widget_refresh_failed", now)
			e.WidgetKind = strptr("weekly")
			e.WidgetSize = strptr("medium")
			e.WidgetRefreshMode = strptr("auto")
			e.ErrorCategory = strptr("unknown")
			return e
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := validateAnalyticsEvent(tt.e, now); err != nil {
				t.Fatalf("valid event rejected: %v", err)
			}
		})
	}
}

func TestValidateAnalyticsEventRejectsInvalidData(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	base := func() analyticsEventRequest {
		e := validAnalyticsRequest("feature_result", now)
		e.Feature = strptr("library")
		e.Result = strptr("success")
		return e
	}
	tests := []struct {
		name   string
		mutate func(*analyticsEventRequest)
	}{
		{"unknown event", func(e *analyticsEventRequest) { e.EventType = "custom" }},
		{"noncanonical UUID", func(e *analyticsEventRequest) { e.EventID = "018F3F45-6E88-7D50-8D6A-41E4D19B8123" }},
		{"non UTC time", func(e *analyticsEventRequest) { e.OccurredAt = "2026-09-08T20:00:00+08:00" }},
		{"unknown feature", func(e *analyticsEventRequest) { e.Feature = strptr("free_text") }},
		{"error missing category", func(e *analyticsEventRequest) { e.Result = strptr("error") }},
		{"category on success", func(e *analyticsEventRequest) { e.ErrorCategory = strptr("network") }},
		{"non-applicable field", func(e *analyticsEventRequest) { e.Screen = strptr("status") }},
		{"duration too large", func(e *analyticsEventRequest) {
			e.EventType = "screen_duration"
			e.Feature = nil
			e.Result = nil
			e.Screen = strptr("status")
			e.DurationSeconds = intptr(86401)
		}},
		{"unsafe ad id", func(e *analyticsEventRequest) {
			e.EventType = "ad_click"
			e.Feature = nil
			e.Result = nil
			e.PlacementID = strptr("home hero")
			e.CampaignID = strptr("campaign")
			e.CreativeID = strptr("creative")
		}},
		{"widget missing dimension", func(e *analyticsEventRequest) {
			e.EventType = "widget_render"
			e.Feature = nil
			e.Result = nil
			e.WidgetKind = strptr("today")
			e.WidgetSize = nil
			e.WidgetRefreshMode = strptr("auto")
		}},
		{"widget invalid action", func(e *analyticsEventRequest) {
			e.EventType = "widget_click"
			e.Feature = nil
			e.Result = nil
			e.WidgetKind = strptr("weekly")
			e.WidgetSize = strptr("medium")
			e.WidgetRefreshMode = strptr("manual")
			e.WidgetAction = strptr("delete")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := base()
			tt.mutate(&e)
			if _, err := validateAnalyticsEvent(e, now); err == nil {
				t.Fatal("invalid event was accepted")
			}
		})
	}
}

func TestDecodeStrictAnalyticsJSONRejectsUnknownField(t *testing.T) {
	r := httptest.NewRequest("POST", "/analytics/events", bytes.NewBufferString(`{"protocol_version":1,"events":[],"details":"must not be accepted"}`))
	w := httptest.NewRecorder()
	var req analyticsBatchRequest
	if err := decodeStrictAnalyticsJSON(w, r, &req); err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestAnalyticsHMACIsDeterministicAndDomainSeparated(t *testing.T) {
	a := analyticsHMAC("pepper", "user", "42")
	b := analyticsHMAC("pepper", "user", "42")
	c := analyticsHMAC("pepper", "installation", "42")
	if hex.EncodeToString(a) != hex.EncodeToString(b) {
		t.Fatal("same input produced different hashes")
	}
	if hex.EncodeToString(a) == hex.EncodeToString(c) {
		t.Fatal("hash domains are not separated")
	}
	if len(a) != 32 {
		t.Fatalf("hash length = %d, want 32", len(a))
	}
}

func TestLocationOrFixedFallsBackWithoutZoneInfo(t *testing.T) {
	loc := locationOrFixed("ZoneNaN/Definitely-Missing", 8*60*60)
	if loc == nil {
		t.Fatal("location fallback returned nil")
	}
	_, offset := time.Date(2026, 9, 27, 0, 0, 0, 0, loc).Zone()
	if offset != 8*60*60 {
		t.Fatalf("fallback offset = %d, want %d", offset, 8*60*60)
	}
}

func TestAnalyticsRangeUsesShanghaiDay(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/admin/analytics/summary?days=30", nil)
	from, to, days := analyticsRange(r)
	if days != 30 {
		t.Fatalf("days = %d, want 30", days)
	}
	if to.Location() == nil || from.Location() == nil {
		t.Fatal("analytics range returned a nil location")
	}
	if to.Hour() != 0 || to.Minute() != 0 || to.Second() != 0 {
		t.Fatalf("range end = %v, want local midnight", to)
	}
	if want := 29 * 24 * time.Hour; to.Sub(from) != want {
		t.Fatalf("range duration = %v, want %v", to.Sub(from), want)
	}
}
