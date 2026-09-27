package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"

	"zonenan-backend/internal/db"
)

// AnalyticsStore persists only validated, normalized analytics fields.
type AnalyticsStore struct{ pool *db.Pool }

func NewAnalyticsStore(p *db.Pool) *AnalyticsStore { return &AnalyticsStore{pool: p} }

type AnalyticsEvent struct {
	EventID, SessionID, EventType, Platform, AppVersion     string
	UserID                                                  int64
	InstallationHash, UserHash                              []byte
	OccurredAt                                              time.Time
	Screen, Feature, Reason, Source, Result                 *string
	ErrorCategory                                           *string
	DurationSeconds                                         *int
	PlacementID, CampaignID, CreativeID                     *string
	WidgetKind, WidgetSize, WidgetRefreshMode, WidgetAction *string
}

type AnalyticsDeviceInfo struct {
	Platform  string
	OSVersion *string
	Brand     *string
	Model     *string
}

func (s *AnalyticsStore) InsertBatch(ctx context.Context, events []AnalyticsEvent, devices ...*AnalyticsDeviceInfo) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var device *AnalyticsDeviceInfo
	if len(devices) > 0 {
		device = devices[0]
	}

	linked := make(map[string]bool)
	for _, e := range events {
		if len(e.UserHash) == 0 || linked[string(e.InstallationHash)] {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO analytics_installations(installation_hash, user_hash, linked_at)
			VALUES($1,$2,NOW())
			ON CONFLICT (installation_hash, user_hash) DO UPDATE SET linked_at=NOW()`,
			e.InstallationHash, e.UserHash); err != nil {
			return 0, err
		}
		linked[string(e.InstallationHash)] = true
	}

	for _, e := range events {
		if e.UserID == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO analytics_user_versions(user_id, app_version, platform, last_seen_at, os_version, device_brand, device_model)
			VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (user_id) DO UPDATE SET
			  app_version=EXCLUDED.app_version, platform=EXCLUDED.platform,
			  last_seen_at=EXCLUDED.last_seen_at, updated_at=NOW(),
				  os_version=COALESCE(EXCLUDED.os_version, analytics_user_versions.os_version),
				  device_brand=COALESCE(EXCLUDED.device_brand, analytics_user_versions.device_brand),
				  device_model=COALESCE(EXCLUDED.device_model, analytics_user_versions.device_model)
			WHERE EXCLUDED.last_seen_at >= analytics_user_versions.last_seen_at`,
			e.UserID, e.AppVersion, e.Platform, e.OccurredAt,
			deviceString(device, func(d *AnalyticsDeviceInfo) *string { return d.OSVersion }),
			deviceString(device, func(d *AnalyticsDeviceInfo) *string { return d.Brand }),
			deviceString(device, func(d *AnalyticsDeviceInfo) *string { return d.Model })); err != nil {
			return 0, err
		}
	}

	var inserted int64
	for _, e := range events {
		tag, err := tx.Exec(ctx, `
			INSERT INTO analytics_events(
				event_id, installation_hash, user_hash, session_id, event_type, occurred_at,
				platform, app_version, screen, feature, reason, source, result, error_category,
				duration_seconds, placement_id, campaign_id, creative_id,
				widget_kind, widget_size, widget_refresh_mode, widget_action)
			VALUES($1::uuid,$2,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
			ON CONFLICT (event_id) DO NOTHING`,
			e.EventID, e.InstallationHash, nullableBytes(e.UserHash), e.SessionID, e.EventType,
			e.OccurredAt, e.Platform, e.AppVersion, e.Screen, e.Feature, e.Reason, e.Source,
			e.Result, e.ErrorCategory, e.DurationSeconds, e.PlacementID, e.CampaignID, e.CreativeID, e.WidgetKind, e.WidgetSize, e.WidgetRefreshMode, e.WidgetAction)
		if err != nil {
			return 0, err
		}
		inserted += tag.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return inserted, nil
}

func nullableBytes(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func deviceString(device *AnalyticsDeviceInfo, get func(*AnalyticsDeviceInfo) *string) any {
	if device == nil {
		return nil
	}
	return get(device)
}

type AnalyticsOverview struct {
	DAU                  int64   `json:"dau"`
	AppDAU               int64   `json:"app_dau"`
	WidgetDAU            int64   `json:"widget_dau"`
	PAU                  int64   `json:"pau"`
	MAU                  int64   `json:"mau"`
	AppSessionsPerAppDAU float64 `json:"app_sessions_per_app_dau"`
	// SessionsPerDAU is a compatibility alias for AppSessionsPerAppDAU.
	SessionsPerDAU float64 `json:"sessions_per_dau"`
}

type AnalyticsRetention struct {
	Day1Rate float64 `json:"d1_rate"`
	Day1Base int64   `json:"d1_base"`
	Day7Rate float64 `json:"d7_rate"`
	Day7Base int64   `json:"d7_base"`
}

type AnalyticsBreakdown struct {
	Name   string `json:"name"`
	Events int64  `json:"events"`
	Users  int64  `json:"users"`
}

type AnalyticsWidgetBreakdown struct {
	EventType   string `json:"event_type"`
	Kind        string `json:"kind"`
	Size        string `json:"size"`
	RefreshMode string `json:"refresh_mode"`
	Action      string `json:"action,omitempty"`
	Events      int64  `json:"events"`
	Users       int64  `json:"users"`
}

type AnalyticsDurationBreakdown struct {
	Name       string  `json:"name"`
	Views      int64   `json:"views"`
	Users      int64   `json:"users"`
	AvgSeconds float64 `json:"avg_seconds"`
}

type AnalyticsSummary struct {
	Days            int                          `json:"days"`
	Overview        AnalyticsOverview            `json:"overview"`
	Retention       AnalyticsRetention           `json:"retention"`
	Features        []AnalyticsBreakdown         `json:"features"`
	FeatureResults  []AnalyticsBreakdown         `json:"feature_results"`
	Screens         []AnalyticsBreakdown         `json:"screens"`
	ScreenDurations []AnalyticsDurationBreakdown `json:"screen_durations"`
	Platforms       []AnalyticsBreakdown         `json:"platforms"`
	Versions        []AnalyticsBreakdown         `json:"versions"`
	Widgets         []AnalyticsWidgetBreakdown   `json:"widgets"`
}

type AnalyticsTrendPoint struct {
	Date        string `json:"date"`
	DAU         int64  `json:"dau"`
	AppDAU      int64  `json:"app_dau"`
	WidgetDAU   int64  `json:"widget_dau"`
	PAU         int64  `json:"pau"`
	MAU         int64  `json:"mau"`
	AppSessions int64  `json:"app_sessions"`
	// Sessions is a compatibility alias for AppSessions.
	Sessions int64 `json:"sessions"`
}

const analyticsActorSQL = `COALESCE(e.user_hash, ai.user_hash, e.installation_hash)`
const analyticsExclusionSQL = `NOT EXISTS (
	SELECT 1 FROM analytics_exclusions x WHERE x.actor_hash = COALESCE(e.user_hash, ai.user_hash)
)`
const analyticsEventsSQL = `analytics_events e
	LEFT JOIN (
		SELECT installation_hash, (ARRAY_AGG(user_hash))[1] AS user_hash
		FROM analytics_installations
		GROUP BY installation_hash
		HAVING COUNT(*) = 1
	) ai ON ai.installation_hash = e.installation_hash`

func (s *AnalyticsStore) Summary(ctx context.Context, from, to time.Time, days int) (AnalyticsSummary, error) {
	out := AnalyticsSummary{
		Days: days, Features: []AnalyticsBreakdown{}, FeatureResults: []AnalyticsBreakdown{},
		Screens: []AnalyticsBreakdown{}, ScreenDurations: []AnalyticsDurationBreakdown{},
		Platforms: []AnalyticsBreakdown{}, Versions: []AnalyticsBreakdown{}, Widgets: []AnalyticsWidgetBreakdown{},
	}
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		q := fmt.Sprintf(`WITH base AS (
			SELECT %s actor_hash, e.session_id, e.event_type, e.occurred_at
			FROM %s
			WHERE e.occurred_at >= $1::timestamptz - INTERVAL '29 days'
			  AND e.occurred_at < $1::timestamptz + INTERVAL '1 day' AND %s
		)
		SELECT
			COUNT(DISTINCT actor_hash) FILTER (WHERE occurred_at >= $1 AND occurred_at < $1::timestamptz + INTERVAL '1 day'),
			COUNT(DISTINCT actor_hash) FILTER (WHERE occurred_at >= $1 AND occurred_at < $1::timestamptz + INTERVAL '1 day' AND event_type NOT LIKE 'widget_%%'),
			COUNT(DISTINCT actor_hash) FILTER (WHERE occurred_at >= $1 AND occurred_at < $1::timestamptz + INTERVAL '1 day' AND event_type LIKE 'widget_%%'),
			COUNT(DISTINCT actor_hash) FILTER (WHERE occurred_at >= $1 AND occurred_at < $1::timestamptz + INTERVAL '1 day' AND event_type IS NOT NULL),
			COUNT(DISTINCT actor_hash)
		FROM base`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL)
		return s.pool.QueryRow(groupCtx, q, to).Scan(
			&out.Overview.DAU,
			&out.Overview.AppDAU,
			&out.Overview.WidgetDAU,
			&out.Overview.PAU,
			&out.Overview.MAU,
		)
	})
	// The remaining aggregates are independent. Running them concurrently avoids
	// making admin latency the sum of every report query.
	group.Go(func() error {
		q := fmt.Sprintf(`WITH daily AS (
		SELECT (e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date AS event_date, COUNT(DISTINCT e.session_id) AS app_sessions,
		       COUNT(DISTINCT %s) AS app_dau
		FROM %s
		WHERE e.occurred_at >= $1 AND e.occurred_at < $2
		  AND e.event_type NOT LIKE 'widget_%%' AND %s
		GROUP BY (e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date
	)
	SELECT COALESCE(SUM(app_sessions)::double precision / NULLIF(SUM(app_dau), 0), 0) FROM daily`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL)
		return s.pool.QueryRow(groupCtx, q, from, to.AddDate(0, 0, 1)).Scan(&out.Overview.AppSessionsPerAppDAU)
	})
	group.Go(func() error {
		return s.scanBreakdown(groupCtx, &out.Features, fmt.Sprintf(`
		SELECT e.feature, COUNT(*), COUNT(DISTINCT %s)
		FROM %s
		WHERE e.occurred_at >= $1 AND e.occurred_at < $2 AND e.event_type = 'feature_open' AND %s
		GROUP BY e.feature ORDER BY COUNT(*) DESC, e.feature`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL), from, to.AddDate(0, 0, 1))
	})
	group.Go(func() error {
		return s.scanBreakdown(groupCtx, &out.FeatureResults, fmt.Sprintf(`
		SELECT e.feature || ':' || e.result, COUNT(*), COUNT(DISTINCT %s)
		FROM %s
		WHERE e.occurred_at >= $1 AND e.occurred_at < $2 AND e.event_type = 'feature_result' AND %s
		GROUP BY e.feature, e.result ORDER BY e.feature, e.result`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL), from, to.AddDate(0, 0, 1))
	})
	group.Go(func() error {
		return s.scanBreakdown(groupCtx, &out.Screens, fmt.Sprintf(`
			SELECT e.screen || ':' || e.event_type, COUNT(*), COUNT(DISTINCT %s)
			FROM %s
			WHERE e.occurred_at >= $1 AND e.occurred_at < $2
			  AND e.event_type IN ('screen_click', 'screen_view') AND %s
			GROUP BY e.screen, e.event_type ORDER BY e.screen, e.event_type`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL), from, to.AddDate(0, 0, 1))
	})
	group.Go(func() error {
		q := fmt.Sprintf(`
		SELECT e.screen, COUNT(*), COUNT(DISTINCT %s), COALESCE(AVG(e.duration_seconds), 0)
		FROM %s
		WHERE e.occurred_at >= $1 AND e.occurred_at < $2 AND e.event_type = 'screen_duration' AND %s
		GROUP BY e.screen ORDER BY e.screen`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL)
		return s.scanDurationBreakdown(groupCtx, &out.ScreenDurations, q, from, to.AddDate(0, 0, 1))
	})
	group.Go(func() error {
		return s.scanBreakdown(groupCtx, &out.Platforms, fmt.Sprintf(`
		SELECT e.platform, COUNT(*), COUNT(DISTINCT %s)
		FROM %s WHERE e.occurred_at >= $1 AND e.occurred_at < $2 AND %s
		GROUP BY e.platform ORDER BY COUNT(*) DESC, e.platform`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL), from, to.AddDate(0, 0, 1))
	})
	group.Go(func() error {
		return s.scanBreakdown(groupCtx, &out.Versions, fmt.Sprintf(`
			SELECT e.app_version, COUNT(*), COUNT(DISTINCT %s)
			FROM %s WHERE e.occurred_at >= $1 AND e.occurred_at < $2 AND %s
		GROUP BY e.app_version ORDER BY COUNT(*) DESC, e.app_version LIMIT 20`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL), from, to.AddDate(0, 0, 1))
	})
	group.Go(func() error {
		return s.scanWidgetBreakdown(groupCtx, &out.Widgets, fmt.Sprintf(`
			SELECT e.event_type, e.widget_kind, e.widget_size, e.widget_refresh_mode,
			       COALESCE(e.widget_action, ''), COUNT(*), COUNT(DISTINCT %s)
			FROM %s
			WHERE e.occurred_at >= $1 AND e.occurred_at < $2
			  AND e.event_type LIKE 'widget_%%' AND %s
			GROUP BY e.event_type, e.widget_kind, e.widget_size, e.widget_refresh_mode, e.widget_action
			ORDER BY e.event_type, e.widget_kind, e.widget_size, e.widget_refresh_mode, e.widget_action`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL), from, to.AddDate(0, 0, 1))
	})
	group.Go(func() error {
		retention, err := s.retention(groupCtx, from, to)
		if err == nil {
			out.Retention = retention
		}
		return err
	})
	if err := group.Wait(); err != nil {
		return out, err
	}
	out.Overview.SessionsPerDAU = out.Overview.AppSessionsPerAppDAU
	return out, nil
}

func (s *AnalyticsStore) scanBreakdown(ctx context.Context, dst *[]AnalyticsBreakdown, query string, args ...any) error {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v AnalyticsBreakdown
		if err := rows.Scan(&v.Name, &v.Events, &v.Users); err != nil {
			return err
		}
		*dst = append(*dst, v)
	}
	return rows.Err()
}

func (s *AnalyticsStore) scanWidgetBreakdown(ctx context.Context, dst *[]AnalyticsWidgetBreakdown, query string, args ...any) error {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v AnalyticsWidgetBreakdown
		if err := rows.Scan(&v.EventType, &v.Kind, &v.Size, &v.RefreshMode, &v.Action, &v.Events, &v.Users); err != nil {
			return err
		}
		*dst = append(*dst, v)
	}
	return rows.Err()
}

func (s *AnalyticsStore) scanDurationBreakdown(ctx context.Context, dst *[]AnalyticsDurationBreakdown, query string, args ...any) error {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v AnalyticsDurationBreakdown
		if err := rows.Scan(&v.Name, &v.Views, &v.Users, &v.AvgSeconds); err != nil {
			return err
		}
		*dst = append(*dst, v)
	}
	return rows.Err()
}

func (s *AnalyticsStore) retention(ctx context.Context, from, to time.Time) (AnalyticsRetention, error) {
	// First-seen needs historical session starts, but return activity only needs
	// the selected date range. Keeping those sets separate avoids loading every
	// historical event twice (once for D1 and once for D7).
	q := fmt.Sprintf(`WITH first_seen AS (
		SELECT %s actor_hash,
		       MIN((e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date) cohort_day
		FROM %s
		WHERE e.event_type = 'session_start'
		  AND e.occurred_at < $2::timestamptz + INTERVAL '1 day' AND %s
		GROUP BY actor_hash
	), activity AS (
		SELECT DISTINCT %s actor_hash,
		       (e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date event_day
		FROM %s
		WHERE e.occurred_at >= $1::timestamptz + INTERVAL '1 day'
		  AND e.occurred_at < $2::timestamptz + INTERVAL '1 day' AND %s
	), eligible AS (
		SELECT * FROM first_seen
		WHERE cohort_day >= ($1::timestamptz AT TIME ZONE 'Asia/Shanghai')::date
		  AND cohort_day <= LEAST(
			($2::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,
			(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date - 1
		  )
	)
	SELECT
		COALESCE(100.0 * COUNT(*) FILTER (WHERE EXISTS (
			SELECT 1 FROM activity a WHERE a.actor_hash = eligible.actor_hash AND a.event_day = eligible.cohort_day + 1
		)) / NULLIF(COUNT(*), 0), 0)::double precision,
		COUNT(*),
		COALESCE(100.0 * COUNT(*) FILTER (
			WHERE cohort_day <= LEAST(
				($2::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,
				(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date - 7
			) AND EXISTS (
				SELECT 1 FROM activity a WHERE a.actor_hash = eligible.actor_hash AND a.event_day = eligible.cohort_day + 7
			)
		) / NULLIF(COUNT(*) FILTER (WHERE cohort_day <= LEAST(
			($2::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,
			(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date - 7
		)), 0), 0)::double precision,
		COUNT(*) FILTER (WHERE cohort_day <= LEAST(
			($2::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,
			(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date - 7
		))
	FROM eligible`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL,
		analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL)
	var out AnalyticsRetention
	err := s.pool.QueryRow(ctx, q, from, to).Scan(
		&out.Day1Rate, &out.Day1Base, &out.Day7Rate, &out.Day7Base,
	)
	return out, err
}

func (s *AnalyticsStore) Trends(ctx context.Context, from, to time.Time) ([]AnalyticsTrendPoint, error) {
	q := fmt.Sprintf(`WITH days(event_date) AS (
		SELECT (($1::timestamptz AT TIME ZONE 'Asia/Shanghai')::date + gs.n)
			FROM generate_series(
				0,
				(($2::timestamptz AT TIME ZONE 'Asia/Shanghai')::date - ($1::timestamptz AT TIME ZONE 'Asia/Shanghai')::date)
			) AS gs(n)
	), base AS MATERIALIZED (
		SELECT %s actor_hash, e.session_id, e.event_type,
		       (e.occurred_at AT TIME ZONE 'Asia/Shanghai')::date event_day
		FROM %s
		WHERE e.occurred_at >= $1::timestamptz - INTERVAL '29 days' AND e.occurred_at < $2::timestamptz + INTERVAL '1 day' AND %s
	), user_days AS (
		SELECT actor_hash, event_day,
		       BOOL_OR(event_type NOT LIKE 'widget_%%') has_app,
		       BOOL_OR(event_type LIKE 'widget_%%') has_widget
		FROM base GROUP BY actor_hash, event_day
	), daily AS (
		SELECT event_day,
		       COUNT(*) pau,
		       COUNT(*) FILTER (WHERE has_app) app_dau,
		       COUNT(*) FILTER (WHERE has_widget) widget_dau
		FROM user_days GROUP BY event_day
	), sessions AS (
		SELECT event_day, COUNT(DISTINCT session_id) app_sessions
		FROM base WHERE event_type NOT LIKE 'widget_%%' GROUP BY event_day
	), rolling AS (
		SELECT d.event_date, COUNT(DISTINCT u.actor_hash) mau
		FROM days d
		LEFT JOIN user_days u ON u.event_day BETWEEN d.event_date - 29 AND d.event_date
		GROUP BY d.event_date
	)
	SELECT d.event_date::text,
		COALESCE(a.pau, 0), COALESCE(a.app_dau, 0), COALESCE(a.widget_dau, 0),
		COALESCE(a.pau, 0), COALESCE(r.mau, 0), COALESCE(s.app_sessions, 0)
	FROM days d
	LEFT JOIN daily a ON a.event_day = d.event_date
	LEFT JOIN rolling r ON r.event_date = d.event_date
	LEFT JOIN sessions s ON s.event_day = d.event_date
	ORDER BY d.event_date`, analyticsActorSQL, analyticsEventsSQL, analyticsExclusionSQL)
	rows, err := s.pool.Query(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnalyticsTrendPoint{}
	for rows.Next() {
		var p AnalyticsTrendPoint
		if err := rows.Scan(&p.Date, &p.DAU, &p.AppDAU, &p.WidgetDAU, &p.PAU, &p.MAU, &p.AppSessions); err != nil {
			return nil, err
		}
		p.Sessions = p.AppSessions
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *AnalyticsStore) SetUserExcluded(ctx context.Context, userID int64, actorHash []byte, excluded bool) error {
	if !excluded {
		_, err := s.pool.Exec(ctx, `DELETE FROM analytics_exclusions WHERE actor_hash=$1`, actorHash)
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO analytics_exclusions(actor_hash, user_id)
		SELECT $1, id FROM zonenan_users WHERE id=$2
		ON CONFLICT (actor_hash) DO UPDATE SET user_id=EXCLUDED.user_id`, actorHash, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *AnalyticsStore) ExcludedUsers(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM analytics_exclusions WHERE user_id IS NOT NULL ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
