-- 键值配置(版本信息、抓取节流窗口等运行时可调项)。
CREATE TABLE IF NOT EXISTS app_settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 默认值(存在则不覆盖)。
INSERT INTO app_settings(key, value) VALUES
  ('grade_sync_throttle_hours', '24'),
  ('grade_manual_refresh_cooldown_minutes', '30'),
  ('app_latest_version', ''),
  ('app_min_supported_version', ''),
  ('app_apk_url', ''),
  ('app_changelog', ''),
  ('app_force_update', 'false')
ON CONFLICT (key) DO NOTHING;

