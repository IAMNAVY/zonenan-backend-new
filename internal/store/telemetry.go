package store

import (
	"context"
	"time"

	"zonenan-backend/internal/db"
)

// TelemetryStore 记录行为事件、崩溃上报,并支撑风控分析。
type TelemetryStore struct{ pool *db.Pool }

func NewTelemetryStore(p *db.Pool) *TelemetryStore { return &TelemetryStore{pool: p} }

// RecordEvent 落一条行为事件(给分/图书馆等)。detail 为 JSON 字符串。
func (s *TelemetryStore) RecordEvent(ctx context.Context, userID *int64, deviceFP, ip, category, action, detailJSON string) error {
	if detailJSON == "" {
		detailJSON = "{}"
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO telemetry_events(user_id, device_fingerprint, ip, category, action, detail)
		 VALUES ($1,$2,$3,$4,$5,$6::jsonb)`,
		userID, deviceFP, ip, category, action, detailJSON)
	return err
}

// CrashReport 是一条崩溃/错误上报。
type CrashReport struct {
	UserID      *int64
	AppVersion  string
	Platform    string
	DeviceModel string
	ErrorType   string
	Message     string
	Stack       string
}

// RecordCrash 落一条崩溃上报。
func (s *TelemetryStore) RecordCrash(ctx context.Context, c CrashReport) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO crash_reports(user_id, app_version, platform, device_model, error_type, message, stack)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		c.UserID, c.AppVersion, c.Platform, c.DeviceModel, c.ErrorType, c.Message, c.Stack)
	return err
}

// --- 风控 ---

// IsDeviceBlocked 判断设备是否在黑名单。
func (s *TelemetryStore) IsDeviceBlocked(ctx context.Context, deviceFP string) bool {
	if deviceFP == "" {
		return false
	}
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM device_blocklist WHERE device_fingerprint=$1)`, deviceFP).Scan(&exists)
	return err == nil && exists
}

// BlockDevice 将设备加入黑名单。
func (s *TelemetryStore) BlockDevice(ctx context.Context, deviceFP, reason string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO device_blocklist(device_fingerprint, reason) VALUES($1,$2)
		 ON CONFLICT (device_fingerprint) DO UPDATE SET reason=EXCLUDED.reason, blocked_at=NOW()`,
		deviceFP, reason)
	return err
}

// UnblockDevice 移出黑名单。
func (s *TelemetryStore) UnblockDevice(ctx context.Context, deviceFP string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM device_blocklist WHERE device_fingerprint=$1`, deviceFP)
	return err
}

// AddRiskFlag 记录一条风控命中。
func (s *TelemetryStore) AddRiskFlag(ctx context.Context, userID *int64, deviceFP, rule string, score int, detail string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO risk_flags(user_id, device_fingerprint, rule, score, detail)
		 VALUES ($1,$2,$3,$4,$5)`,
		userID, deviceFP, rule, score, detail)
	return err
}

// UserRiskScore 返回某用户近 window 时间内未处置风控命中的累计分。
func (s *TelemetryStore) UserRiskScore(ctx context.Context, userID int64, window time.Duration) (int, error) {
	var total int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(score),0) FROM risk_flags
		  WHERE user_id=$1 AND resolved=FALSE AND created_at >= NOW() - $2::interval`,
		userID, window.String()).Scan(&total)
	return total, err
}

// RiskFlag 是风控看板一条记录。
type RiskFlag struct {
	ID        int64  `json:"id"`
	UserID    *int64 `json:"user_id,omitempty"`
	DeviceFP  string `json:"device_fingerprint"`
	Rule      string `json:"rule"`
	Score     int    `json:"score"`
	Detail    string `json:"detail"`
	Resolved  bool   `json:"resolved"`
	CreatedAt string `json:"created_at"`
}

// ListRiskFlags 列出未处置风控命中(最新在前)。
func (s *TelemetryStore) ListRiskFlags(ctx context.Context, page, pageSize int) ([]RiskFlag, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, device_fingerprint, rule, score, detail, resolved, created_at::text
		   FROM risk_flags ORDER BY resolved ASC, created_at DESC LIMIT $1 OFFSET $2`,
		pageSize, page*pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RiskFlag{}
	for rows.Next() {
		var f RiskFlag
		if err := rows.Scan(&f.ID, &f.UserID, &f.DeviceFP, &f.Rule, &f.Score, &f.Detail, &f.Resolved, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// ResolveRiskFlag 标记某条风控命中为已处置。
func (s *TelemetryStore) ResolveRiskFlag(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE risk_flags SET resolved=TRUE WHERE id=$1`, id)
	return err
}

// CountRecentEventsByDevice 统计某设备近 window 内某 category/action 的事件数(风控规则用)。
func (s *TelemetryStore) CountRecentEventsByDevice(ctx context.Context, deviceFP, category, action string, window time.Duration) (int, error) {
	if deviceFP == "" {
		return 0, nil
	}
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM telemetry_events
		  WHERE device_fingerprint=$1 AND category=$2 AND action=$3
		    AND created_at >= NOW() - $4::interval`,
		deviceFP, category, action, window.String()).Scan(&n)
	return n, err
}

// CountDistinctUsersByDevice 统计某设备近 window 内关联的不同账号数(多账号同设备 → 刷号信号)。
func (s *TelemetryStore) CountDistinctUsersByDevice(ctx context.Context, deviceFP string, window time.Duration) (int, error) {
	if deviceFP == "" {
		return 0, nil
	}
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT user_id) FROM telemetry_events
		  WHERE device_fingerprint=$1 AND user_id IS NOT NULL
		    AND created_at >= NOW() - $2::interval`,
		deviceFP, window.String()).Scan(&n)
	return n, err
}
