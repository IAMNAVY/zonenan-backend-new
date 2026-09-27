-- Extensible memberships, Afdian order records, public binding codes and one-time activations.
CREATE TABLE IF NOT EXISTS membership_types (
  type_key       TEXT PRIMARY KEY CHECK (type_key ~ '^[a-z][a-z0-9_]{0,63}$'),
  title          TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  features       JSONB NOT NULL DEFAULT '[]'::jsonb,
  afdian_plan_id TEXT UNIQUE,
  enabled        BOOLEAN NOT NULL DEFAULT TRUE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO membership_types(type_key, title, description, features)
VALUES (
  'premium',
  'Premium',
  'ZoneNaN Premium 会员',
  '[{"key":"premium_center","title":"Premium 会员权益","description":"查看并使用 Premium 会员功能"}]'::jsonb
)
ON CONFLICT (type_key) DO NOTHING;

CREATE TABLE IF NOT EXISTS membership_binding_codes (
  user_id           BIGINT PRIMARY KEY REFERENCES zonenan_users(id) ON DELETE CASCADE,
  binding_code      TEXT NOT NULL UNIQUE,
  algorithm_version INTEGER NOT NULL DEFAULT 1 CHECK (algorithm_version > 0),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS membership_grants (
  id                  BIGSERIAL PRIMARY KEY,
  user_id             BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  membership_type_key TEXT NOT NULL REFERENCES membership_types(type_key),
  source              TEXT NOT NULL CHECK (source IN ('afdian_webhook', 'afdian_sync', 'activation', 'admin', 'legacy')),
  external_ref        TEXT NOT NULL DEFAULT '',
  starts_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at          TIMESTAMPTZ NOT NULL,
  revoked_at          TIMESTAMPTZ,
  metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_membership_grants_user
  ON membership_grants(user_id, membership_type_key, expires_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_membership_grants_external
  ON membership_grants(source, external_ref, membership_type_key)
  WHERE external_ref <> '';

CREATE TABLE IF NOT EXISTS membership_events (
  id                  BIGSERIAL PRIMARY KEY,
  provider            TEXT NOT NULL DEFAULT 'afdian',
  event_type          TEXT NOT NULL DEFAULT 'order',
  external_order_id   TEXT NOT NULL,
  afdian_user_id      TEXT NOT NULL DEFAULT '',
  afdian_private_id   TEXT NOT NULL DEFAULT '',
  plan_id             TEXT NOT NULL DEFAULT '',
  status              INTEGER NOT NULL DEFAULT 0,
  remark              TEXT NOT NULL DEFAULT '',
  payload             JSONB NOT NULL DEFAULT '{}'::jsonb,
  process_status      TEXT NOT NULL CHECK (process_status IN ('processed', 'ignored', 'unmatched', 'failed')),
  matched_user_id     BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  error_message       TEXT NOT NULL DEFAULT '',
  received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  processed_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_membership_events_order
  ON membership_events(provider, event_type, external_order_id);
CREATE INDEX IF NOT EXISTS idx_membership_events_status
  ON membership_events(process_status, received_at DESC);

CREATE TABLE IF NOT EXISTS membership_activation_codes (
  id                  BIGSERIAL PRIMARY KEY,
  code_hash           TEXT NOT NULL UNIQUE,
  membership_type_key TEXT NOT NULL REFERENCES membership_types(type_key),
  source_order_id     TEXT NOT NULL DEFAULT '',
  expires_at          TIMESTAMPTZ,
  used_at             TIMESTAMPTZ,
  used_by_user_id     BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  disabled_at         TIMESTAMPTZ,
  created_by          TEXT NOT NULL DEFAULT 'admin',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_membership_activation_codes_state
  ON membership_activation_codes(membership_type_key, used_at, disabled_at);

CREATE TABLE IF NOT EXISTS membership_audit_logs (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  action      TEXT NOT NULL CHECK (action IN ('grant', 'revoke', 'generate_code', 'disable_code', 'sync')),
  source      TEXT NOT NULL DEFAULT 'admin',
  external_ref TEXT NOT NULL DEFAULT '',
  actor       TEXT NOT NULL DEFAULT '',
  reason      TEXT NOT NULL DEFAULT '',
  metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_membership_audit_user
  ON membership_audit_logs(user_id, created_at DESC);

