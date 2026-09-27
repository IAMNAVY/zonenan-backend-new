package store

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"zonenan-backend/internal/db"
)

// SettingStore 读写 app_settings 键值配置。
type SettingStore struct{ pool *db.Pool }

func NewSettingStore(p *db.Pool) *SettingStore { return &SettingStore{pool: p} }

// GetStr 取字符串配置,不存在返回 def。
func (s *SettingStore) GetStr(ctx context.Context, key, def string) string {
	var v string
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return def
	}
	return v
}

// GetInt 取整数配置,不存在或非法返回 def。
func (s *SettingStore) GetInt(ctx context.Context, key string, def int) int {
	raw := s.GetStr(ctx, key, "")
	if raw == "" {
		return def
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return def
}

// GetBool 取布尔配置。
func (s *SettingStore) GetBool(ctx context.Context, key string, def bool) bool {
	raw := s.GetStr(ctx, key, "")
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// Set 写入配置。
func (s *SettingStore) Set(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO app_settings(key, value, updated_at) VALUES ($1,$2,NOW())
		 ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		key, value)
	return err
}
