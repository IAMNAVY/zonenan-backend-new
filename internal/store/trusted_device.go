package store

import (
	"context"
	"time"

	"zonenan-backend/internal/db"
)

// TrustedDevice 是某学号 hash 的一台可信设备(TOFU)。
type TrustedDevice struct {
	ID          int64     `json:"id"`
	DeviceFP    string    `json:"device_fingerprint"`
	DeviceName  string    `json:"device_name"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// TrustedDeviceStore 管理可信设备(TOFU 首次登录锁设备)。
type TrustedDeviceStore struct{ pool *db.Pool }

func NewTrustedDeviceStore(p *db.Pool) *TrustedDeviceStore { return &TrustedDeviceStore{pool: p} }

// Count 返回某学号 hash 已绑定的可信设备数。
func (s *TrustedDeviceStore) Count(ctx context.Context, studentHash string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM trusted_devices WHERE student_hash=$1`, studentHash).Scan(&n)
	return n, err
}

// IsTrusted 判断某设备是否已是该学号的可信设备。
func (s *TrustedDeviceStore) IsTrusted(ctx context.Context, studentHash, deviceFP string) (bool, error) {
	if deviceFP == "" {
		return false, nil
	}
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE student_hash=$1 AND device_fingerprint=$2)`,
		studentHash, deviceFP).Scan(&exists)
	return exists, err
}

// Trust 记录/更新一台可信设备(幂等:已存在则刷新 last_seen 与机型名)。
func (s *TrustedDeviceStore) Trust(ctx context.Context, studentHash, deviceFP, deviceName string) error {
	if deviceFP == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO trusted_devices(student_hash, device_fingerprint, device_name)
		 VALUES ($1,$2,$3)
		 ON CONFLICT (student_hash, device_fingerprint)
		 DO UPDATE SET last_seen_at=NOW(),
		   device_name=CASE WHEN EXCLUDED.device_name<>'' THEN EXCLUDED.device_name
		                    ELSE trusted_devices.device_name END`,
		studentHash, deviceFP, deviceName)
	return err
}

// List 列出某学号的可信设备(用户自管界面用)。
func (s *TrustedDeviceStore) List(ctx context.Context, studentHash string) ([]TrustedDevice, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, device_fingerprint, device_name, first_seen_at, last_seen_at
		   FROM trusted_devices WHERE student_hash=$1 ORDER BY last_seen_at DESC`, studentHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrustedDevice{}
	for rows.Next() {
		var d TrustedDevice
		if err := rows.Scan(&d.ID, &d.DeviceFP, &d.DeviceName, &d.FirstSeenAt, &d.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// Revoke 撤销某学号名下的一台可信设备(按 id,校验归属)。
func (s *TrustedDeviceStore) Revoke(ctx context.Context, studentHash string, id int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM trusted_devices WHERE id=$1 AND student_hash=$2`, id, studentHash)
	return err
}
