CREATE TABLE IF NOT EXISTS web_sessions (
    id TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
    refresh_token_hash BYTEA NOT NULL UNIQUE,
    device_name TEXT NOT NULL DEFAULT 'Web 浏览器',
    remember_me BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_web_sessions_user ON web_sessions(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_web_sessions_expiry ON web_sessions(expires_at) WHERE revoked_at IS NULL;

