-- One-time login approvals for a new device. The plaintext challenge secret is
-- returned once to that device and never persisted; only its SHA-256 digest is stored.
CREATE TABLE IF NOT EXISTS device_login_challenges (
  challenge_id              TEXT PRIMARY KEY,
  student_hash              TEXT NOT NULL,
  target_device_fingerprint TEXT NOT NULL,
  target_device_name        TEXT NOT NULL DEFAULT '',
  secret_hash               BYTEA NOT NULL CHECK (octet_length(secret_hash) = 32),
  status                    TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'approved', 'rejected', 'consumed')),
  expires_at                TIMESTAMPTZ NOT NULL,
  created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  approved_at               TIMESTAMPTZ,
  consumed_at               TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_device_login_challenges_pending
  ON device_login_challenges(student_hash, created_at DESC)
  WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_device_login_challenges_expiry
  ON device_login_challenges(expires_at);

