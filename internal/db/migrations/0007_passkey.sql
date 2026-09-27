-- Passkey (WebAuthn) 凭证存储。
CREATE TABLE IF NOT EXISTS passkey_credentials (
  id              BIGSERIAL PRIMARY KEY,
  user_id         BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  credential_id   BYTEA NOT NULL UNIQUE,
  public_key      BYTEA NOT NULL,
  attestation_type TEXT NOT NULL DEFAULT '',
  aaguid          BYTEA NOT NULL DEFAULT '',
  sign_count      BIGINT NOT NULL DEFAULT 0,
  transports      TEXT[] NOT NULL DEFAULT '{}',
  name            TEXT NOT NULL DEFAULT 'Passkey',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_used_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_passkey_creds_user ON passkey_credentials(user_id);

