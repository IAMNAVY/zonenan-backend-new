package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"zonenan-backend/internal/db"
)

// Use only an explicitly configured test database. Each test migrates an isolated
// schema and removes it afterwards; DATABASE_URL is deliberately not used.
func analyticsTestStore(t *testing.T) *AnalyticsStore {
	t.Helper()
	url := os.Getenv("ANALYTICS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set ANALYTICS_TEST_DATABASE_URL to run PostgreSQL analytics integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("analytics_test_%x", suffix)
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("clean up analytics test schema: %v", err)
		}
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	wrapped := &db.Pool{Pool: pool}
	if err := wrapped.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return NewAnalyticsStore(wrapped)
}

func TestAnalyticsAppSessions(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, loc)
	type event struct {
		actor, session int
		typeName       string
		hour           int
		anonymous      bool
	}
	type daily struct {
		appDAU, widgetDAU, pau, sessions int64
	}
	tests := []struct {
		name     string
		events   []event
		excluded int
		want     [2]daily
		ratio    float64
	}{
		{
			name: "empty range",
		},
		{
			name: "widget only persistent session",
			events: []event{
				{1, 10, "widget_render", 1, false},
				{1, 10, "widget_refresh", 2, false},
				{1, 10, "widget_click_open_app", 25, false},
			},
			want: [2]daily{{0, 1, 1, 0}, {0, 1, 1, 0}},
		},
		{
			name: "mixed events exclude widget numerator and denominator",
			events: []event{
				{1, 1, "session_start", 1, false},
				{1, 1, "screen_view", 2, false},
				{1, 1, "screen_click", 3, false},
				{1, 2, "screen_view", 4, false},
				{1, 10, "widget_render", 5, false},
				{2, 20, "widget_refresh", 6, false},
			},
			want:  [2]daily{{1, 2, 2, 2}, {}},
			ratio: 2,
		},
		{
			name: "cross midnight session counted once each Shanghai day",
			events: []event{
				{1, 1, "session_start", 23, false},
				{1, 1, "screen_view", 24, false},
				{1, 1, "screen_click", 25, false},
			},
			want:  [2]daily{{1, 0, 1, 1}, {1, 0, 1, 1}},
			ratio: 1,
		},
		{
			name: "ratio weights daily active users rather than interval UV",
			events: []event{
				{1, 1, "screen_view", 1, false},
				{1, 2, "screen_view", 2, false},
				{1, 3, "screen_view", 3, false},
				{1, 4, "screen_view", 25, false},
				{2, 5, "screen_view", 26, false},
				{2, 20, "widget_render", 26, false},
				{3, 30, "widget_render", 27, false},
			},
			want:  [2]daily{{1, 0, 1, 3}, {2, 2, 3, 2}},
			ratio: 5.0 / 3.0,
		},
		{
			name: "excluded user including linked anonymous events",
			events: []event{
				{1, 1, "screen_view", 1, false},
				{2, 2, "screen_view", 2, false},
				{2, 3, "screen_view", 3, true},
				{2, 20, "widget_render", 4, true},
			},
			excluded: 2,
			want:     [2]daily{{1, 0, 1, 1}, {}},
			ratio:    1,
		},
		{
			name: "linked anonymous and authenticated identity merge",
			events: []event{
				{1, 1, "screen_view", 1, true},
				{1, 2, "screen_view", 2, false},
				{1, 10, "widget_render", 3, true},
			},
			want:  [2]daily{{1, 1, 1, 2}, {}},
			ratio: 2,
		},
		{
			name: "range boundaries exclude preceding and following sessions",
			events: []event{
				{1, 1, "screen_view", -1, false},
				{1, 2, "screen_view", 0, false},
				{1, 3, "screen_view", 47, false},
				{1, 4, "screen_view", 48, false},
			},
			want:  [2]daily{{1, 0, 1, 1}, {1, 0, 1, 1}},
			ratio: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := analyticsTestStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for i, fixture := range tt.events {
				e := AnalyticsEvent{
					EventID:          fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1),
					SessionID:        fmt.Sprintf("00000000-0000-4000-8000-%012d", fixture.session),
					EventType:        fixture.typeName,
					InstallationHash: bytes.Repeat([]byte{byte(fixture.actor + 100)}, 32),
					OccurredAt:       from.Add(time.Duration(fixture.hour) * time.Hour),
					Platform:         "android",
					AppVersion:       "1.2.3",
				}
				if !fixture.anonymous {
					e.UserHash = bytes.Repeat([]byte{byte(fixture.actor)}, 32)
				}
				screen, reason, source := "timetable", "cold_start", "bottom_nav"
				kind, size, mode, action := "today", "small", "auto", "open_app"
				switch fixture.typeName {
				case "session_start":
					e.Reason = &reason
				case "screen_view":
					e.Screen = &screen
				case "screen_click":
					e.Screen, e.Source = &screen, &source
				default:
					e.WidgetKind, e.WidgetSize, e.WidgetRefreshMode = &kind, &size, &mode
					if fixture.typeName == "widget_click_open_app" {
						e.WidgetAction = &action
					}
				}
				if _, err := s.InsertBatch(ctx, []AnalyticsEvent{e}); err != nil {
					t.Fatal(err)
				}
			}
			if tt.excluded != 0 {
				if _, err := s.pool.Exec(ctx, "INSERT INTO analytics_exclusions(actor_hash) VALUES($1)", bytes.Repeat([]byte{byte(tt.excluded)}, 32)); err != nil {
					t.Fatal(err)
				}
			}
			to := from.AddDate(0, 0, 1)
			summary, err := s.Summary(ctx, from, to, 2)
			if err != nil {
				t.Fatal(err)
			}
			if got := summary.Overview.AppSessionsPerAppDAU; math.Abs(got-tt.ratio) > 1e-9 {
				t.Errorf("App Sessions / App DAU = %v, want %v", got, tt.ratio)
			}
			last := tt.want[1]
			if summary.Overview.AppDAU != last.appDAU || summary.Overview.WidgetDAU != last.widgetDAU || summary.Overview.PAU != last.pau {
				t.Errorf("overview active users = %+v, want %+v", summary.Overview, last)
			}
			assertAnalyticsJSONAliases(t, summary.Overview, "app_sessions_per_app_dau", "sessions_per_dau", tt.ratio)
			trend, err := s.Trends(ctx, from, to)
			if err != nil {
				t.Fatal(err)
			}
			if len(trend) != len(tt.want) {
				t.Fatalf("trend has %d days, want %d", len(trend), len(tt.want))
			}
			for i, point := range trend {
				want := tt.want[i]
				if point.Date != from.AddDate(0, 0, i).Format("2006-01-02") || point.AppDAU != want.appDAU || point.WidgetDAU != want.widgetDAU || point.PAU != want.pau || point.AppSessions != want.sessions {
					t.Errorf("day %d = %+v, want %+v", i, point, want)
				}
				assertAnalyticsJSONAliases(t, point, "app_sessions", "sessions", float64(want.sessions))
			}
		})
	}
}

func TestAnalyticsRetention(t *testing.T) {
	s := analyticsTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(loc)
	to := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	from := to.AddDate(0, 0, -10)
	type fixture struct {
		actor, day int
		eventType  string
	}
	fixtures := []fixture{
		{1, 0, "session_start"},
		{1, 1, "screen_view"},
		{1, 7, "screen_view"},
		{2, 1, "session_start"},
		{2, 2, "screen_view"},
		{3, 8, "session_start"},
	}
	for i, fixture := range fixtures {
		e := AnalyticsEvent{
			EventID:          fmt.Sprintf("10000000-0000-4000-8000-%012d", i+1),
			SessionID:        fmt.Sprintf("20000000-0000-4000-8000-%012d", i+1),
			EventType:        fixture.eventType,
			InstallationHash: bytes.Repeat([]byte{byte(fixture.actor + 100)}, 32),
			UserHash:         bytes.Repeat([]byte{byte(fixture.actor)}, 32),
			OccurredAt:       from.AddDate(0, 0, fixture.day).Add(12 * time.Hour),
			Platform:         "android",
			AppVersion:       "1.2.3",
		}
		if fixture.eventType == "session_start" {
			reason := "cold_start"
			e.Reason = &reason
		} else {
			screen := "timetable"
			e.Screen = &screen
		}
		if _, err := s.InsertBatch(ctx, []AnalyticsEvent{e}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.retention(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got.Day1Base != 3 || math.Abs(got.Day1Rate-200.0/3) > 1e-9 {
		t.Errorf("D1 retention = %.6f%%/%d, want %.6f%%/3", got.Day1Rate, got.Day1Base, 200.0/3)
	}
	if got.Day7Base != 2 || math.Abs(got.Day7Rate-50) > 1e-9 {
		t.Errorf("D7 retention = %.6f%%/%d, want 50%%/2", got.Day7Rate, got.Day7Base)
	}
}

func assertAnalyticsJSONAliases(t *testing.T, value any, field, alias string, want float64) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{field, alias} {
		got, ok := decoded[key].(float64)
		if !ok || math.Abs(got-want) > 1e-9 {
			t.Errorf("JSON %s = %v, want %v", key, decoded[key], want)
		}
	}
}
