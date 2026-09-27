-- Latest App version observed for each authenticated ZoneNaN user.
CREATE TABLE IF NOT EXISTS analytics_user_versions (
  user_id      BIGINT PRIMARY KEY REFERENCES zonenan_users(id) ON DELETE CASCADE,
  app_version  VARCHAR(32) NOT NULL CHECK (app_version ~ '^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$'),
  platform     TEXT NOT NULL CHECK (platform IN ('android', 'ios', 'windows', 'macos', 'linux', 'web')),
  last_seen_at TIMESTAMPTZ NOT NULL,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_analytics_user_versions_version
  ON analytics_user_versions(app_version, last_seen_at DESC);

