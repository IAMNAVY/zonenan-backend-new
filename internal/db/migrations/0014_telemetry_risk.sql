-- 遥测 + 风控子系统。
-- 行为事件:给分/图书馆等关键动作埋点,供风控综合分析(防伪造、防违规刷接口)。
CREATE TABLE IF NOT EXISTS telemetry_events (
  id                 BIGSERIAL PRIMARY KEY,
  user_id            BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  device_fingerprint TEXT NOT NULL DEFAULT '',
  ip                 TEXT NOT NULL DEFAULT '',
  category           TEXT NOT NULL DEFAULT '',   -- grade | library | ...
  action             TEXT NOT NULL DEFAULT '',   -- search | detail | sync | seat_book | ...
  detail             JSONB NOT NULL DEFAULT '{}',
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_telemetry_events_user ON telemetry_events(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_telemetry_events_device ON telemetry_events(device_fingerprint, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_telemetry_events_cat ON telemetry_events(category, action, created_at DESC);

-- 崩溃/错误上报(不含 PII:仅错误栈 + 版本 + 机型)。
CREATE TABLE IF NOT EXISTS crash_reports (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  app_version  TEXT NOT NULL DEFAULT '',
  platform     TEXT NOT NULL DEFAULT '',   -- android | ios
  device_model TEXT NOT NULL DEFAULT '',
  error_type   TEXT NOT NULL DEFAULT '',
  message      TEXT NOT NULL DEFAULT '',
  stack        TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_crash_reports_time ON crash_reports(created_at DESC);

-- 设备黑名单:风控命中或人工封禁的设备,登录/敏感操作直接拒绝。
CREATE TABLE IF NOT EXISTS device_blocklist (
  device_fingerprint TEXT PRIMARY KEY,
  reason             TEXT NOT NULL DEFAULT '',
  blocked_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 风控评分/标记:综合分析后对账号或设备打分,供后台看板 + 自动/人工处置。
CREATE TABLE IF NOT EXISTS risk_flags (
  id                 BIGSERIAL PRIMARY KEY,
  user_id            BIGINT REFERENCES zonenan_users(id) ON DELETE CASCADE,
  device_fingerprint TEXT NOT NULL DEFAULT '',
  rule               TEXT NOT NULL DEFAULT '',   -- 命中的规则名
  score              INT NOT NULL DEFAULT 0,      -- 风险分
  detail             TEXT NOT NULL DEFAULT '',
  resolved           BOOLEAN NOT NULL DEFAULT FALSE,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_risk_flags_user ON risk_flags(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_risk_flags_unresolved ON risk_flags(resolved, created_at DESC);

