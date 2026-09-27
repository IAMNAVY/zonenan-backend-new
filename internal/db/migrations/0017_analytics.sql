-- Privacy-preserving product analytics. Only normalized, allowlisted dimensions are stored.
CREATE TABLE IF NOT EXISTS analytics_events (
  event_id          UUID PRIMARY KEY,
  installation_hash BYTEA NOT NULL CHECK (octet_length(installation_hash) = 32),
  user_hash         BYTEA CHECK (user_hash IS NULL OR octet_length(user_hash) = 32),
  session_id        UUID NOT NULL,
  event_type        TEXT NOT NULL CHECK (event_type IN (
    'session_start', 'screen_click', 'screen_view', 'screen_duration',
    'feature_open', 'feature_result', 'ad_impression', 'ad_click'
  )),
  occurred_at       TIMESTAMPTZ NOT NULL,
  received_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  platform          TEXT NOT NULL CHECK (platform IN ('android', 'ios', 'windows', 'macos', 'linux', 'web')),
  app_version       VARCHAR(32) NOT NULL CHECK (app_version ~ '^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$'),
  screen            TEXT CHECK (screen IS NULL OR screen IN (
    'timetable', 'status', 'functions', 'news', 'campus_card', 'library',
    'grades', 'grade_rating', 'schedule', 'shuttle'
  )),
  feature           TEXT CHECK (feature IS NULL OR feature IN (
    'timetable', 'status', 'functions', 'news', 'campus_card', 'library',
    'grades', 'grade_rating', 'schedule', 'shuttle'
  )),
  reason            TEXT CHECK (reason IS NULL OR reason IN ('cold_start', 'background_15m')),
  source            TEXT CHECK (source IS NULL OR source IN (
    'bottom_nav', 'status_card', 'functions_grid', 'mine_grid', 'deep_link', 'system'
  )),
  result            TEXT CHECK (result IS NULL OR result IN ('success', 'empty', 'cancelled', 'error')),
  error_category    TEXT CHECK (error_category IS NULL OR error_category IN (
    'network', 'auth', 'permission', 'server', 'parse', 'unavailable', 'unknown'
  )),
  duration_seconds  INTEGER CHECK (duration_seconds IS NULL OR duration_seconds BETWEEN 0 AND 86400),
  placement_id      VARCHAR(64) CHECK (placement_id IS NULL OR placement_id ~ '^[0-9A-Za-z][0-9A-Za-z._:-]{0,63}$'),
  campaign_id       VARCHAR(64) CHECK (campaign_id IS NULL OR campaign_id ~ '^[0-9A-Za-z][0-9A-Za-z._:-]{0,63}$'),
  creative_id       VARCHAR(64) CHECK (creative_id IS NULL OR creative_id ~ '^[0-9A-Za-z][0-9A-Za-z._:-]{0,63}$'),
  CHECK (
    (event_type = 'session_start' AND reason IS NOT NULL AND screen IS NULL AND feature IS NULL AND source IS NULL AND result IS NULL AND error_category IS NULL AND duration_seconds IS NULL AND placement_id IS NULL AND campaign_id IS NULL AND creative_id IS NULL) OR
    (event_type = 'screen_click' AND screen IS NOT NULL AND source IS NOT NULL AND feature IS NULL AND reason IS NULL AND result IS NULL AND error_category IS NULL AND duration_seconds IS NULL AND placement_id IS NULL AND campaign_id IS NULL AND creative_id IS NULL) OR
    (event_type = 'screen_view' AND screen IS NOT NULL AND feature IS NULL AND reason IS NULL AND result IS NULL AND error_category IS NULL AND duration_seconds IS NULL AND placement_id IS NULL AND campaign_id IS NULL AND creative_id IS NULL) OR
    (event_type = 'screen_duration' AND screen IS NOT NULL AND duration_seconds IS NOT NULL AND feature IS NULL AND reason IS NULL AND source IS NULL AND result IS NULL AND error_category IS NULL AND placement_id IS NULL AND campaign_id IS NULL AND creative_id IS NULL) OR
    (event_type = 'feature_open' AND feature IS NOT NULL AND source IS NOT NULL AND screen IS NULL AND reason IS NULL AND result IS NULL AND error_category IS NULL AND duration_seconds IS NULL AND placement_id IS NULL AND campaign_id IS NULL AND creative_id IS NULL) OR
    (event_type = 'feature_result' AND feature IS NOT NULL AND result IS NOT NULL AND ((result = 'error' AND error_category IS NOT NULL) OR (result <> 'error' AND error_category IS NULL)) AND screen IS NULL AND reason IS NULL AND source IS NULL AND duration_seconds IS NULL AND placement_id IS NULL AND campaign_id IS NULL AND creative_id IS NULL) OR
    (event_type IN ('ad_impression', 'ad_click') AND placement_id IS NOT NULL AND campaign_id IS NOT NULL AND creative_id IS NOT NULL AND screen IS NULL AND feature IS NULL AND reason IS NULL AND source IS NULL AND result IS NULL AND error_category IS NULL AND duration_seconds IS NULL)
  )
);

CREATE TABLE IF NOT EXISTS analytics_installations (
  installation_hash BYTEA NOT NULL CHECK (octet_length(installation_hash) = 32),
  user_hash         BYTEA NOT NULL CHECK (octet_length(user_hash) = 32),
  linked_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (installation_hash, user_hash)
);
CREATE INDEX IF NOT EXISTS idx_analytics_installations_user ON analytics_installations(user_hash);

-- Exclusions retain the pseudonymous hash if an account is later deleted.
CREATE TABLE IF NOT EXISTS analytics_exclusions (
  actor_hash BYTEA PRIMARY KEY CHECK (octet_length(actor_hash) = 32),
  user_id    BIGINT UNIQUE REFERENCES zonenan_users(id) ON DELETE SET NULL,
  reason     VARCHAR(120) NOT NULL DEFAULT 'admin',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_analytics_events_occurred ON analytics_events(occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_analytics_events_install_time ON analytics_events(installation_hash, occurred_at);
CREATE INDEX IF NOT EXISTS idx_analytics_events_user_time ON analytics_events(user_hash, occurred_at) WHERE user_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_analytics_events_session ON analytics_events(session_id);
CREATE INDEX IF NOT EXISTS idx_analytics_events_type_time ON analytics_events(event_type, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_analytics_events_feature_time ON analytics_events(feature, occurred_at DESC) WHERE feature IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_analytics_events_received ON analytics_events(received_at);

-- Intended for a daily scheduler; the occurred_at index keeps the 12-month purge bounded.
CREATE OR REPLACE FUNCTION cleanup_analytics_events(before_time TIMESTAMPTZ DEFAULT NOW() - INTERVAL '12 months')
RETURNS BIGINT LANGUAGE plpgsql AS $$
DECLARE deleted_count BIGINT;
BEGIN
  DELETE FROM analytics_events WHERE occurred_at < before_time;
  GET DIAGNOSTICS deleted_count = ROW_COUNT;
  RETURN deleted_count;
END;
$$;

