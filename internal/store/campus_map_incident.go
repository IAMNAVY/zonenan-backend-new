package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"zonenan-backend/internal/db"
)

const (
	IncidentTrafficEnforcement = "traffic_enforcement"
	IncidentRoadClosed         = "road_closed"
	IncidentCongestion         = "congestion"
	IncidentCat                = "cat"
)

var incidentTypes = []string{
	IncidentTrafficEnforcement,
	IncidentRoadClosed,
	IncidentCongestion,
	IncidentCat,
}

type CampusMapIncidentTypePolicy struct {
	Enabled        bool `json:"enabled"`
	TTLMinutes     int  `json:"ttl_minutes"`
	StaleMinutes   int  `json:"stale_minutes,omitempty"`
	RelightMinutes int  `json:"relight_minutes,omitempty"`
	MaxMinutes     int  `json:"max_minutes"`
}

type CampusMapIncidentPolicy struct {
	Enabled        bool                                   `json:"enabled"`
	Version        string                                 `json:"policy_version"`
	UpdatedAt      time.Time                              `json:"updated_at"`
	MergeRadiusM   int                                    `json:"merge_radius_m"`
	DisplayRadiusM int                                    `json:"display_radius_m"`
	Types          map[string]CampusMapIncidentTypePolicy `json:"types"`
}

type CampusMapIncident struct {
	ID                  int64      `json:"id"`
	Type                string     `json:"type"`
	Latitude            float64    `json:"latitude"`
	Longitude           float64    `json:"longitude"`
	Status              string     `json:"status"`
	CreatedAt           time.Time  `json:"created_at"`
	StaleAt             time.Time  `json:"stale_at"`
	ExpiresAt           time.Time  `json:"expires_at"`
	LastConfirmedAt     *time.Time `json:"last_confirmed_at,omitempty"`
	DisplayRadiusM      int        `json:"display_radius_m"`
	ReportCount         int        `json:"report_count"`
	ConfirmCount        int        `json:"confirm_count"`
	RejectCount         int        `json:"reject_count"`
	Confidence          float64    `json:"confidence"`
	ConfidenceLevel     string     `json:"confidence_level"`
	UserVote            int        `json:"user_vote,omitempty"`
	CanRelight          bool       `json:"can_relight"`
	ReporterTrustNotice bool       `json:"reporter_trust_notice"`
	MyReportID          *int64     `json:"my_report_id,omitempty"`
}

type CampusMapIncidentReport struct {
	ID          int64      `json:"id"`
	IncidentID  int64      `json:"incident_id"`
	Type        string     `json:"type"`
	Latitude    float64    `json:"latitude"`
	Longitude   float64    `json:"longitude"`
	CreatedAt   time.Time  `json:"created_at"`
	WithdrawnAt *time.Time `json:"withdrawn_at,omitempty"`
	Status      string     `json:"status"`
	ExpiresAt   time.Time  `json:"expires_at"`
}

type CampusMapIncidentStore struct{ pool *db.Pool }

func NewCampusMapIncidentStore(pool *db.Pool) *CampusMapIncidentStore {
	return &CampusMapIncidentStore{pool: pool}
}

func validIncidentType(value string) bool {
	for _, item := range incidentTypes {
		if value == item {
			return true
		}
	}
	return false
}

func incidentConfidence(reportWeight, positiveWeight, negativeWeight float64, confirmCount int) (float64, string) {
	score := (2 + reportWeight + positiveWeight) / (4 + reportWeight + positiveWeight + negativeWeight)
	switch {
	case score < 0.4:
		return score, "low"
	case confirmCount >= 2 && score >= 0.7:
		return score, "confirmed"
	default:
		return score, "pending"
	}
}

func (s *CampusMapIncidentStore) Policy(ctx context.Context) (CampusMapIncidentPolicy, error) {
	values := map[string]string{}
	var updatedAt time.Time
	rows, err := s.pool.Query(ctx, `
		SELECT key, value, updated_at FROM app_settings
		 WHERE key LIKE 'campus_incidents_%'`)
	if err != nil {
		return CampusMapIncidentPolicy{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		var changed time.Time
		if err := rows.Scan(&key, &value, &changed); err != nil {
			return CampusMapIncidentPolicy{}, err
		}
		values[key] = value
		if changed.After(updatedAt) {
			updatedAt = changed
		}
	}
	if err := rows.Err(); err != nil {
		return CampusMapIncidentPolicy{}, err
	}
	if updatedAt.IsZero() {
		updatedAt = time.Unix(0, 0).UTC()
	}
	boolValue := func(key string, fallback bool) bool {
		raw, ok := values[key]
		if !ok {
			return fallback
		}
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return fallback
		}
		return parsed
	}
	intValue := func(key string, fallback int) int {
		parsed, err := strconv.Atoi(values[key])
		if err != nil {
			return fallback
		}
		return parsed
	}
	policy := CampusMapIncidentPolicy{
		Enabled:        boolValue("campus_incidents_enabled", true),
		Version:        strconv.FormatInt(updatedAt.UnixNano(), 10),
		UpdatedAt:      updatedAt.UTC(),
		MergeRadiusM:   intValue("campus_incidents_merge_radius_m", 40),
		DisplayRadiusM: intValue("campus_incidents_merge_radius_m", 40),
		Types:          map[string]CampusMapIncidentTypePolicy{},
	}
	defaults := map[string]CampusMapIncidentTypePolicy{
		IncidentTrafficEnforcement: {Enabled: true, TTLMinutes: 120, StaleMinutes: 60, RelightMinutes: 30, MaxMinutes: 240},
		IncidentRoadClosed:         {Enabled: true, TTLMinutes: 120, MaxMinutes: 120},
		IncidentCongestion:         {Enabled: true, TTLMinutes: 30, MaxMinutes: 30},
		IncidentCat:                {Enabled: true, TTLMinutes: 120, MaxMinutes: 120},
	}
	for kind, fallback := range defaults {
		prefix := "campus_incidents_" + kind + "_"
		item := CampusMapIncidentTypePolicy{
			Enabled:        boolValue(prefix+"enabled", fallback.Enabled),
			TTLMinutes:     intValue(prefix+"ttl_minutes", fallback.TTLMinutes),
			StaleMinutes:   intValue(prefix+"stale_minutes", fallback.StaleMinutes),
			RelightMinutes: intValue(prefix+"relight_minutes", fallback.RelightMinutes),
			MaxMinutes:     intValue(prefix+"max_minutes", fallback.MaxMinutes),
		}
		if item.MaxMinutes < item.TTLMinutes {
			item.MaxMinutes = item.TTLMinutes
		}
		policy.Types[kind] = item
	}
	return policy, nil
}

func (s *CampusMapIncidentStore) SetPolicyValue(ctx context.Context, key, value string) error {
	allowed := map[string][2]int{
		"campus_incidents_merge_radius_m":                      {10, 200},
		"campus_incidents_traffic_enforcement_ttl_minutes":     {10, 240},
		"campus_incidents_traffic_enforcement_stale_minutes":   {10, 180},
		"campus_incidents_traffic_enforcement_relight_minutes": {5, 120},
		"campus_incidents_traffic_enforcement_max_minutes":     {30, 720},
		"campus_incidents_road_closed_ttl_minutes":             {10, 360},
		"campus_incidents_congestion_ttl_minutes":              {5, 120},
		"campus_incidents_cat_ttl_minutes":                     {10, 360},
	}
	boolKeys := map[string]bool{"campus_incidents_enabled": true}
	for _, kind := range incidentTypes {
		boolKeys["campus_incidents_"+kind+"_enabled"] = true
	}
	if boolKeys[key] {
		if value != "true" && value != "false" {
			return errors.New("开关值只能是 true 或 false")
		}
	} else if bounds, ok := allowed[key]; ok {
		n, err := strconv.Atoi(value)
		if err != nil || n < bounds[0] || n > bounds[1] {
			return fmt.Errorf("配置值必须在 %d 到 %d 之间", bounds[0], bounds[1])
		}
	} else {
		return errors.New("不支持的突发事件配置项")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_settings(key,value,updated_at) VALUES($1,$2,NOW())
		ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, key, value)
	return err
}

func (s *CampusMapIncidentStore) userEvidenceWeight(ctx context.Context, userID int64) (float64, bool, error) {
	// Community votes describe the current event, not the reporter's long-term
	// trustworthiness. Absence reports have a stronger participation incentive
	// than confirmations and an event may legitimately end between observations.
	// Account sanctions therefore require an explicit administrative decision.
	// Keep the stored weight fields at a neutral value for rolling compatibility.
	_ = ctx
	_ = userID
	return 1, false, nil
}

func (s *CampusMapIncidentStore) Submit(ctx context.Context, userID int64, kind string, latitude, longitude float64) (CampusMapIncidentReport, error) {
	kind = strings.TrimSpace(kind)
	if userID <= 0 || !validIncidentType(kind) || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return CampusMapIncidentReport{}, errors.New("上报参数无效")
	}
	policy, err := s.Policy(ctx)
	if err != nil {
		return CampusMapIncidentReport{}, err
	}
	typePolicy := policy.Types[kind]
	if !policy.Enabled || !typePolicy.Enabled {
		return CampusMapIncidentReport{}, errors.New("该突发事件类型当前未开放")
	}
	weight, lowTrust, err := s.userEvidenceWeight(ctx, userID)
	if err != nil {
		return CampusMapIncidentReport{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CampusMapIncidentReport{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "campus-incident:"+kind); err != nil {
		return CampusMapIncidentReport{}, err
	}
	var recent bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM campus_map_incident_reports r
		 JOIN campus_map_incidents i ON i.id=r.incident_id
		 WHERE r.user_id=$1 AND r.withdrawn_at IS NULL AND i.incident_type=$2
		   AND r.created_at>NOW()-INTERVAL '30 seconds')`, userID, kind).Scan(&recent); err != nil {
		return CampusMapIncidentReport{}, err
	}
	if recent {
		return CampusMapIncidentReport{}, errors.New("上报过于频繁，请稍后再试")
	}
	var incidentID int64
	var eventCreated, eventBaseExpires, eventExpires, eventMaxExpires time.Time
	createdNew := false
	err = tx.QueryRow(ctx, `
		SELECT id, created_at, base_expires_at, expires_at, max_expires_at
		  FROM campus_map_incidents
		 WHERE status='active' AND expires_at>NOW() AND incident_type=$1
		   AND 2*6371000*asin(sqrt(
		     power(sin(radians(latitude-$2)/2),2)+
		     cos(radians($2))*cos(radians(latitude))*power(sin(radians(longitude-$3)/2),2)
		   )) <= $4
		 ORDER BY 2*6371000*asin(sqrt(
		     power(sin(radians(latitude-$2)/2),2)+
		     cos(radians($2))*cos(radians(latitude))*power(sin(radians(longitude-$3)/2),2)
		   ))
		 LIMIT 1 FOR UPDATE`, kind, latitude, longitude, policy.MergeRadiusM).Scan(&incidentID, &eventCreated, &eventBaseExpires, &eventExpires, &eventMaxExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		createdNew = true
		err = tx.QueryRow(ctx, `
			INSERT INTO campus_map_incidents(incident_type,latitude,longitude,base_expires_at,expires_at,max_expires_at)
			VALUES($1,$2,$3,NOW()+make_interval(mins=>$4),NOW()+make_interval(mins=>$4),NOW()+make_interval(mins=>$5))
			RETURNING id,created_at,base_expires_at,expires_at,max_expires_at`, kind, latitude, longitude, typePolicy.TTLMinutes, typePolicy.MaxMinutes).
			Scan(&incidentID, &eventCreated, &eventBaseExpires, &eventExpires, &eventMaxExpires)
	}
	if err != nil {
		return CampusMapIncidentReport{}, err
	}
	var alreadyReported bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM campus_map_incident_reports
		 WHERE incident_id=$1 AND user_id=$2 AND withdrawn_at IS NULL)`, incidentID, userID).Scan(&alreadyReported); err != nil {
		return CampusMapIncidentReport{}, err
	}
	if alreadyReported {
		return CampusMapIncidentReport{}, errors.New("你已经上报过附近的同类事件")
	}
	var report CampusMapIncidentReport
	err = tx.QueryRow(ctx, `
		INSERT INTO campus_map_incident_reports(incident_id,user_id,latitude,longitude,evidence_weight,trust_notice)
		VALUES($1,$2,$3,$4,$5,$6)
		RETURNING id,created_at`, incidentID, userID, latitude, longitude, weight, lowTrust).
		Scan(&report.ID, &report.CreatedAt)
	if err != nil {
		return CampusMapIncidentReport{}, err
	}
	if !createdNew && kind == IncidentTrafficEnforcement && !lowTrust {
		target := time.Now().Add(time.Duration(typePolicy.RelightMinutes) * time.Minute)
		if target.After(eventMaxExpires) {
			target = eventMaxExpires
		}
		if target.After(eventBaseExpires) && target.After(eventExpires) {
			eventExpires = target
			if _, err := tx.Exec(ctx, `UPDATE campus_map_incidents SET expires_at=$2,updated_at=NOW() WHERE id=$1`, incidentID, target); err != nil {
				return CampusMapIncidentReport{}, err
			}
		}
	}
	// Keep the displayed center inside the 40 m grouping circle while making
	// multiple independent reports converge on their average exact position.
	_, err = tx.Exec(ctx, `
		UPDATE campus_map_incidents i SET
		 latitude=x.latitude, longitude=x.longitude, updated_at=NOW()
		FROM (SELECT AVG(latitude) latitude, AVG(longitude) longitude
		        FROM campus_map_incident_reports WHERE incident_id=$1 AND withdrawn_at IS NULL) x
		WHERE i.id=$1`, incidentID)
	if err != nil {
		return CampusMapIncidentReport{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CampusMapIncidentReport{}, err
	}
	report.IncidentID = incidentID
	report.Type = kind
	report.Latitude = latitude
	report.Longitude = longitude
	report.Status = "active"
	report.ExpiresAt = eventExpires
	return report, nil
}

func (s *CampusMapIncidentStore) List(ctx context.Context, userID int64, minLat, maxLat, minLon, maxLon float64, typeFilter []string, includeInactive bool) ([]CampusMapIncident, error) {
	policy, err := s.Policy(ctx)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled && !includeInactive {
		return []CampusMapIncident{}, nil
	}
	for _, kind := range typeFilter {
		if !validIncidentType(kind) {
			return nil, errors.New("突发事件类型无效")
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id,i.incident_type,i.latitude,i.longitude,i.status,i.created_at,i.expires_at,
		       CASE WHEN rs.report_count>1 THEN GREATEST(vs.last_confirmed_at,rs.last_reported_at)
		            ELSE vs.last_confirmed_at END,
		       COALESCE(rs.report_count,0),COALESCE(vs.confirm_count,0),COALESCE(vs.reject_count,0),
		       COALESCE(rs.report_weight,0),COALESCE(vs.positive_weight,0),COALESCE(vs.negative_weight,0),
		       COALESCE(vs.user_vote,0),rs.my_report_id,COALESCE(rs.trust_notice,FALSE)
		  FROM campus_map_incidents i
		  LEFT JOIN LATERAL (
		    SELECT COUNT(*) report_count,SUM(evidence_weight) report_weight,BOOL_OR(trust_notice) trust_notice,
		           MAX(created_at) last_reported_at,
		           MIN(id) FILTER (WHERE user_id=$1) my_report_id
		      FROM campus_map_incident_reports
		     WHERE incident_id=i.id AND withdrawn_at IS NULL
		  ) rs ON TRUE
		  LEFT JOIN LATERAL (
		    SELECT COUNT(*) FILTER (WHERE value=1) confirm_count,
		           COUNT(*) FILTER (WHERE value=-1) reject_count,
		           SUM(evidence_weight) FILTER (WHERE value=1) positive_weight,
		           SUM(evidence_weight) FILTER (WHERE value=-1) negative_weight,
		           MAX(updated_at) FILTER (WHERE value=1) last_confirmed_at,
		           MAX(value) FILTER (WHERE user_id=$1) user_vote
		      FROM campus_map_incident_votes WHERE incident_id=i.id
		  ) vs ON TRUE
		 WHERE ($2 OR (i.status='active' AND i.expires_at>NOW()))
		   AND i.latitude BETWEEN $3 AND $4 AND i.longitude BETWEEN $5 AND $6
		   AND (cardinality($7::text[])=0 OR i.incident_type=ANY($7))
		 ORDER BY i.created_at DESC
		 LIMIT 500`, userID, includeInactive, minLat, maxLat, minLon, maxLon, typeFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CampusMapIncident{}
	for rows.Next() {
		var item CampusMapIncident
		var reportWeight, positiveWeight, negativeWeight float64
		if err := rows.Scan(&item.ID, &item.Type, &item.Latitude, &item.Longitude, &item.Status,
			&item.CreatedAt, &item.ExpiresAt, &item.LastConfirmedAt, &item.ReportCount,
			&item.ConfirmCount, &item.RejectCount, &reportWeight, &positiveWeight,
			&negativeWeight, &item.UserVote, &item.MyReportID, &item.ReporterTrustNotice); err != nil {
			return nil, err
		}
		typePolicy := policy.Types[item.Type]
		if item.Status == "active" && !item.ExpiresAt.After(time.Now()) {
			item.Status = "expired"
		}
		item.DisplayRadiusM = policy.DisplayRadiusM
		item.StaleAt = item.CreatedAt.Add(time.Duration(typePolicy.StaleMinutes) * time.Minute)
		item.Confidence, item.ConfidenceLevel = incidentConfidence(reportWeight, positiveWeight, negativeWeight, item.ConfirmCount)
		item.ReporterTrustNotice = (item.ReporterTrustNotice || item.ConfidenceLevel == "low") && item.ConfidenceLevel != "confirmed"
		item.CanRelight = userID > 0 && item.MyReportID == nil && item.Type == IncidentTrafficEnforcement && time.Now().Before(item.ExpiresAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *CampusMapIncidentStore) Vote(ctx context.Context, userID, incidentID int64, value int) error {
	if userID <= 0 || incidentID <= 0 || (value != -1 && value != 1) {
		return errors.New("确认参数无效")
	}
	weight, lowTrust, err := s.userEvidenceWeight(ctx, userID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var kind, status string
	var baseExpires, maxExpires time.Time
	err = tx.QueryRow(ctx, `SELECT incident_type,status,base_expires_at,max_expires_at FROM campus_map_incidents WHERE id=$1 FOR UPDATE`, incidentID).
		Scan(&kind, &status, &baseExpires, &maxExpires)
	if err != nil {
		return errors.New("事件不存在")
	}
	if status != "active" || time.Now().After(maxExpires) {
		return errors.New("事件已经结束")
	}
	var owns bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campus_map_incident_reports WHERE incident_id=$1 AND user_id=$2 AND withdrawn_at IS NULL)`, incidentID, userID).Scan(&owns); err != nil {
		return err
	}
	if owns && value == 1 {
		return errors.New("不能点亮自己上报的事件")
	}
	var previousValue int
	var previousUpdated time.Time
	err = tx.QueryRow(ctx, `SELECT value,updated_at FROM campus_map_incident_votes WHERE incident_id=$1 AND user_id=$2`, incidentID, userID).
		Scan(&previousValue, &previousUpdated)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && previousValue == value && time.Since(previousUpdated) < 10*time.Minute {
		return nil
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO campus_map_incident_votes(incident_id,user_id,value,evidence_weight)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(incident_id,user_id) DO UPDATE
		SET value=EXCLUDED.value,evidence_weight=EXCLUDED.evidence_weight,updated_at=NOW()`, incidentID, userID, value, weight)
	if err != nil {
		return err
	}
	if value == 1 && kind == IncidentTrafficEnforcement && !lowTrust {
		policy, err := s.Policy(ctx)
		if err != nil {
			return err
		}
		window := time.Duration(policy.Types[kind].RelightMinutes) * time.Minute
		target := time.Now().Add(window)
		if target.After(maxExpires) {
			target = maxExpires
		}
		if target.After(baseExpires) {
			_, err = tx.Exec(ctx, `UPDATE campus_map_incidents SET expires_at=GREATEST(expires_at,$2),updated_at=NOW() WHERE id=$1`, incidentID, target)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *CampusMapIncidentStore) MyReports(ctx context.Context, userID int64) ([]CampusMapIncidentReport, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id,r.incident_id,i.incident_type,r.latitude,r.longitude,r.created_at,r.withdrawn_at,
		       CASE WHEN r.withdrawn_at IS NOT NULL THEN 'withdrawn'
		            WHEN i.status<>'active' THEN i.status
		            WHEN i.expires_at<=NOW() THEN 'expired' ELSE 'active' END,
		       i.expires_at
		  FROM campus_map_incident_reports r JOIN campus_map_incidents i ON i.id=r.incident_id
		 WHERE r.user_id=$1 ORDER BY r.created_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CampusMapIncidentReport{}
	for rows.Next() {
		var item CampusMapIncidentReport
		if err := rows.Scan(&item.ID, &item.IncidentID, &item.Type, &item.Latitude, &item.Longitude,
			&item.CreatedAt, &item.WithdrawnAt, &item.Status, &item.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *CampusMapIncidentStore) Withdraw(ctx context.Context, userID, reportID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var incidentID int64
	err = tx.QueryRow(ctx, `
		UPDATE campus_map_incident_reports SET withdrawn_at=NOW()
		 WHERE id=$1 AND user_id=$2 AND withdrawn_at IS NULL
		 RETURNING incident_id`, reportID, userID).Scan(&incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("上报不存在或已经撤销")
	}
	if err != nil {
		return err
	}
	var remaining int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM campus_map_incident_reports WHERE incident_id=$1 AND withdrawn_at IS NULL`, incidentID).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		_, err = tx.Exec(ctx, `UPDATE campus_map_incidents SET status='withdrawn',updated_at=NOW() WHERE id=$1`, incidentID)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE campus_map_incidents i SET latitude=x.latitude,longitude=x.longitude,updated_at=NOW()
			FROM (SELECT AVG(latitude) latitude,AVG(longitude) longitude FROM campus_map_incident_reports WHERE incident_id=$1 AND withdrawn_at IS NULL) x
			WHERE i.id=$1`, incidentID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *CampusMapIncidentStore) AdminRemove(ctx context.Context, incidentID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if incidentID <= 0 || reason == "" || len([]rune(reason)) > 500 {
		return errors.New("请填写有效的撤销原因")
	}
	command, err := s.pool.Exec(ctx, `UPDATE campus_map_incidents SET status='admin_removed',removed_reason=$2,updated_at=NOW() WHERE id=$1 AND status='active'`, incidentID, reason)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("事件不存在或已经结束")
	}
	return nil
}
