-- 免费查询配额追踪:未绑定 CAS 的用户按账号+设备指纹限制查询次数。
CREATE TABLE IF NOT EXISTS grade_free_queries (
  id                BIGSERIAL PRIMARY KEY,
  user_id           BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  device_fingerprint TEXT NOT NULL DEFAULT '',
  queried_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_grade_free_queries_user ON grade_free_queries(user_id);
CREATE INDEX IF NOT EXISTS idx_grade_free_queries_device ON grade_free_queries(device_fingerprint);

