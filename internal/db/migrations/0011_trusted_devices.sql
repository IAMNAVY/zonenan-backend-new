-- 可信设备(TOFU:首次使用即信任):某学号 hash 首次 CAS 登录时绑定当前设备指纹。
-- 之后同一学号从新设备登录需邮箱验证(默认 学号@csu.edu.cn)或被拒。用户可自行撤销设备。
CREATE TABLE IF NOT EXISTS trusted_devices (
  id                 BIGSERIAL PRIMARY KEY,
  student_hash       TEXT NOT NULL,                       -- sha256(学号|pepper),与 cas identity 一致
  device_fingerprint TEXT NOT NULL,                       -- 客户端设备指纹
  device_name        TEXT NOT NULL DEFAULT '',            -- 机型等可读名(展示用)
  first_seen_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (student_hash, device_fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_trusted_devices_hash ON trusted_devices(student_hash);

