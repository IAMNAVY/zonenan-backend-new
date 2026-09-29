CREATE TABLE IF NOT EXISTS push_devices (
  id              BIGSERIAL PRIMARY KEY,
  user_id         BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  installation_id TEXT NOT NULL,
  platform        TEXT NOT NULL CHECK (platform IN ('android', 'ios')),
  provider        TEXT NOT NULL CHECK (provider IN ('fcm', 'apns', 'poll')),
  push_token      TEXT NOT NULL DEFAULT '',
  poll_secret_hash TEXT NOT NULL DEFAULT '',
  app_version     TEXT NOT NULL DEFAULT '',
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(user_id, installation_id, platform),
  UNIQUE(provider, push_token),
  CHECK (provider = 'poll' OR push_token <> ''),
  CHECK (provider <> 'poll' OR poll_secret_hash <> '')
);

CREATE TABLE IF NOT EXISTS push_subscriptions (
  device_id  BIGINT NOT NULL REFERENCES push_devices(id) ON DELETE CASCADE,
  topic      TEXT NOT NULL CHECK (topic IN ('campus_incidents', 'announcements', 'service_status', 'academic')),
  enabled    BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY(device_id, topic)
);

CREATE TABLE IF NOT EXISTS push_messages (
  id          BIGSERIAL PRIMARY KEY,
  topic       TEXT NOT NULL,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  payload     JSONB NOT NULL DEFAULT '{}'::JSONB,
  dedup_key   TEXT UNIQUE,
  status      TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'cancelled')),
  created_by  BIGINT REFERENCES admin_users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  sent_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_push_messages_status ON push_messages(status, created_at);
CREATE INDEX IF NOT EXISTS idx_push_devices_active ON push_devices(platform, provider) WHERE enabled;

CREATE TABLE IF NOT EXISTS push_deliveries (
  message_id   BIGINT NOT NULL REFERENCES push_messages(id) ON DELETE CASCADE,
  device_id    BIGINT NOT NULL REFERENCES push_devices(id) ON DELETE CASCADE,
  status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'invalid_token')),
  provider_ref TEXT NOT NULL DEFAULT '',
  error_code   TEXT NOT NULL DEFAULT '',
  attempted_at TIMESTAMPTZ,
  PRIMARY KEY(message_id, device_id)
);
