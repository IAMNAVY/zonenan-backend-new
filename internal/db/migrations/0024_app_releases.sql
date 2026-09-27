-- Published application releases. The channel here is authoritative for
-- server-side visibility; it is never inferred from a client-declared label.
CREATE TABLE IF NOT EXISTS app_releases (
  id                         BIGSERIAL PRIMARY KEY,
  version                    VARCHAR(32) NOT NULL CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
  version_major              INTEGER NOT NULL CHECK (version_major >= 0),
  version_minor              INTEGER NOT NULL CHECK (version_minor >= 0),
  version_patch              INTEGER NOT NULL CHECK (version_patch >= 0),
  build_number               INTEGER NOT NULL CHECK (build_number > 0),
  channel                    TEXT NOT NULL CHECK (channel IN ('stable', 'beta', 'rc', 'internal')),
  changelog                  TEXT NOT NULL DEFAULT '',
  min_supported_version      VARCHAR(32) NOT NULL DEFAULT '' CHECK (min_supported_version = '' OR min_supported_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
  min_supported_build_number INTEGER NOT NULL DEFAULT 0 CHECK (min_supported_build_number >= 0),
  force_update               BOOLEAN NOT NULL DEFAULT FALSE,
  android_url                TEXT NOT NULL DEFAULT '',
  android_sha256             VARCHAR(64) NOT NULL DEFAULT '' CHECK (android_sha256 = '' OR android_sha256 ~ '^[0-9a-fA-F]{64}$'),
  windows_url                TEXT NOT NULL DEFAULT '',
  windows_sha256             VARCHAR(64) NOT NULL DEFAULT '' CHECK (windows_sha256 = '' OR windows_sha256 ~ '^[0-9a-fA-F]{64}$'),
  ios_url                    TEXT NOT NULL DEFAULT '',
  active                     BOOLEAN NOT NULL DEFAULT TRUE,
  archived_at                TIMESTAMPTZ,
  deleted_at                 TIMESTAMPTZ,
  created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_app_releases_lookup
  ON app_releases(channel, active, version_major DESC, version_minor DESC, version_patch DESC, build_number DESC);
CREATE INDEX IF NOT EXISTS idx_app_releases_history
  ON app_releases(channel, deleted_at, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_app_releases_build_unique
  ON app_releases(build_number);

-- Release-beta access is intentionally separate from the legacy beta_memberships
-- table, which is the existing premium entitlement / content audience.
CREATE TABLE IF NOT EXISTS release_beta_memberships (
  user_id    BIGINT PRIMARY KEY REFERENCES zonenan_users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_release_beta_memberships_user
  ON release_beta_memberships(user_id);

