-- 用户角色 / 白名单:role ∈ user|admin。
-- admin 或白名单账号免风控、免免费查询额度限制,避免运营/自测账号被误封或受限。
ALTER TABLE zonenan_users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user';
ALTER TABLE zonenan_users ADD COLUMN IF NOT EXISTS is_whitelisted BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_zonenan_users_role ON zonenan_users(role);

