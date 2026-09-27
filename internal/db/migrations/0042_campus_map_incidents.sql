-- Short-lived, community reported campus-map incidents. Reports are kept
-- separately from their aggregated event so a user can withdraw only their
-- own evidence without removing confirmations from other users.
CREATE TABLE IF NOT EXISTS campus_map_incidents (
  id                  BIGSERIAL PRIMARY KEY,
  incident_type       TEXT NOT NULL CHECK (incident_type IN ('traffic_enforcement', 'road_closed', 'congestion', 'cat')),
  latitude            DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
  longitude           DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
  status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'withdrawn', 'admin_removed')),
  base_expires_at     TIMESTAMPTZ NOT NULL,
  expires_at          TIMESTAMPTZ NOT NULL,
  max_expires_at      TIMESTAMPTZ NOT NULL,
  removed_reason      TEXT NOT NULL DEFAULT '',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_campus_map_incidents_active
  ON campus_map_incidents(status, expires_at DESC);
CREATE INDEX IF NOT EXISTS idx_campus_map_incidents_position
  ON campus_map_incidents(latitude, longitude);

CREATE TABLE IF NOT EXISTS campus_map_incident_reports (
  id            BIGSERIAL PRIMARY KEY,
  incident_id   BIGINT NOT NULL REFERENCES campus_map_incidents(id) ON DELETE CASCADE,
  user_id       BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  latitude      DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
  longitude     DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
  evidence_weight DOUBLE PRECISION NOT NULL DEFAULT 1 CHECK (evidence_weight BETWEEN 0.25 AND 2),
  trust_notice  BOOLEAN NOT NULL DEFAULT FALSE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  withdrawn_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_campus_map_incident_reports_event
  ON campus_map_incident_reports(incident_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_campus_map_incident_reports_user
  ON campus_map_incident_reports(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS campus_map_incident_votes (
  incident_id   BIGINT NOT NULL REFERENCES campus_map_incidents(id) ON DELETE CASCADE,
  user_id       BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  value         SMALLINT NOT NULL CHECK (value IN (-1, 1)),
  evidence_weight DOUBLE PRECISION NOT NULL DEFAULT 1 CHECK (evidence_weight BETWEEN 0.25 AND 2),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (incident_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_campus_map_incident_votes_event
  ON campus_map_incident_votes(incident_id, value, updated_at DESC);

-- Public policy defaults. The server remains authoritative and the Flutter
-- client caches the public projection for 24 hours.
INSERT INTO app_settings(key, value) VALUES
  ('campus_incidents_enabled', 'true'),
  ('campus_incidents_merge_radius_m', '40'),
  ('campus_incidents_traffic_enforcement_enabled', 'true'),
  ('campus_incidents_traffic_enforcement_ttl_minutes', '120'),
  ('campus_incidents_traffic_enforcement_stale_minutes', '60'),
  ('campus_incidents_traffic_enforcement_relight_minutes', '30'),
  ('campus_incidents_traffic_enforcement_max_minutes', '240'),
  ('campus_incidents_road_closed_enabled', 'true'),
  ('campus_incidents_road_closed_ttl_minutes', '120'),
  ('campus_incidents_congestion_enabled', 'true'),
  ('campus_incidents_congestion_ttl_minutes', '30'),
  ('campus_incidents_cat_enabled', 'true'),
  ('campus_incidents_cat_ttl_minutes', '120')
ON CONFLICT (key) DO NOTHING;

