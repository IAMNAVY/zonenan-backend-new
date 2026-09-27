-- Zonenan backend initial schema.
-- 用户系统:一个 zonenan_users + 多登录方式 zonenan_identities(email/cas/...)。

CREATE TABLE IF NOT EXISTS zonenan_users (
  id          BIGSERIAL PRIMARY KEY,
  nickname    TEXT NOT NULL DEFAULT '',
  avatar_url  TEXT NOT NULL DEFAULT '',
  is_banned   BOOLEAN NOT NULL DEFAULT FALSE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- provider ∈ email|cas|github|google|passkey。
--   email: provider_uid=规范化邮箱, secret=bcrypt(密码), verified=邮箱验证态。
--   cas:   provider_uid=student_hash(学号 hash,不存明文), secret 空。
CREATE TABLE IF NOT EXISTS zonenan_identities (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  provider     TEXT NOT NULL,
  provider_uid TEXT NOT NULL,
  secret       TEXT NOT NULL DEFAULT '',
  verified     BOOLEAN NOT NULL DEFAULT FALSE,
  display_name TEXT NOT NULL DEFAULT '',
  bound_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (provider, provider_uid)
);
CREATE INDEX IF NOT EXISTS idx_zonenan_identities_user ON zonenan_identities(user_id);

-- 邮箱验证码(注册/找回密码)。
CREATE TABLE IF NOT EXISTS email_codes (
  email      TEXT PRIMARY KEY,
  code       TEXT NOT NULL,
  scope      TEXT NOT NULL DEFAULT 'register',
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

