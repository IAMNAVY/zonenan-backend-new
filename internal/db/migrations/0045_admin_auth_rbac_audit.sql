CREATE TABLE IF NOT EXISTS admin_users (
  id            BIGSERIAL PRIMARY KEY,
  email         TEXT NOT NULL,
  display_name  TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  active        BOOLEAN NOT NULL DEFAULT TRUE,
  last_login_at TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (email = LOWER(BTRIM(email)))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_admin_users_email_lower ON admin_users(LOWER(email));

CREATE TABLE IF NOT EXISTS admin_roles (
  role_key    TEXT PRIMARY KEY CHECK (role_key ~ '^[a-z][a-z0-9_]{1,63}$'),
  title       TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  system_role BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS admin_permissions (
  permission_key TEXT PRIMARY KEY CHECK (permission_key ~ '^[a-z][a-z0-9_.]{2,95}$'),
  description    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS admin_user_roles (
  admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
  role_key      TEXT NOT NULL REFERENCES admin_roles(role_key) ON DELETE CASCADE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (admin_user_id, role_key)
);

CREATE TABLE IF NOT EXISTS admin_role_permissions (
  role_key      TEXT NOT NULL REFERENCES admin_roles(role_key) ON DELETE CASCADE,
  permission_key TEXT NOT NULL REFERENCES admin_permissions(permission_key) ON DELETE CASCADE,
  PRIMARY KEY (role_key, permission_key)
);

CREATE TABLE IF NOT EXISTS admin_sessions (
  id           TEXT PRIMARY KEY,
  admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
  token_hash   BYTEA NOT NULL UNIQUE,
  csrf_hash    BYTEA NOT NULL,
  ip           TEXT NOT NULL DEFAULT '',
  user_agent   TEXT NOT NULL DEFAULT '',
  expires_at   TIMESTAMPTZ NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  revoked_at   TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_user ON admin_sessions(admin_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_active ON admin_sessions(expires_at) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS admin_login_attempts (
  id         BIGSERIAL PRIMARY KEY,
  email      TEXT NOT NULL DEFAULT '',
  ip         TEXT NOT NULL DEFAULT '',
  succeeded  BOOLEAN NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_admin_login_attempts_ip ON admin_login_attempts(ip, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_login_attempts_email ON admin_login_attempts(email, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
  id            BIGSERIAL PRIMARY KEY,
  actor_type    TEXT NOT NULL CHECK (actor_type IN ('admin', 'merchant', 'system')),
  actor_id      BIGINT,
  session_id    TEXT NOT NULL DEFAULT '',
  action        TEXT NOT NULL,
  resource_type TEXT NOT NULL DEFAULT '',
  resource_id   TEXT NOT NULL DEFAULT '',
  request_id    TEXT NOT NULL DEFAULT '',
  ip            TEXT NOT NULL DEFAULT '',
  user_agent    TEXT NOT NULL DEFAULT '',
  status_code   INTEGER NOT NULL DEFAULT 0,
  metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_time ON audit_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON audit_logs(actor_type, actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_resource ON audit_logs(resource_type, resource_id, created_at DESC);

INSERT INTO admin_roles(role_key, title, description) VALUES
  ('super_admin', '超级管理员', '拥有全部管理权限'),
  ('content_admin', '内容管理员', '公告、广告、协议与评教'),
  ('map_editor', '地图编辑', '地图与 POI 管理'),
  ('rental_reviewer', '租房审核员', '租房审核与举报'),
  ('merchant_operator', '商户运营', '商户审核与运营'),
  ('risk_admin', '风控管理员', '用户封禁与风险处置'),
  ('analyst', '数据分析员', '只读分析数据')
ON CONFLICT (role_key) DO UPDATE SET title=EXCLUDED.title, description=EXCLUDED.description;

INSERT INTO admin_permissions(permission_key, description) VALUES
  ('dashboard.read', '查看仪表盘'), ('analytics.read', '查看使用分析'),
  ('crash.read', '查看崩溃'), ('crash.delete', '删除崩溃记录'),
  ('user.read', '查看用户'), ('user.ban', '封禁用户'), ('user.delete', '删除用户'),
  ('premium.read', '查看会员'), ('premium.manage', '管理会员'),
  ('risk.read', '查看风险'), ('risk.manage', '处置风险'),
  ('content.read', '查看运营内容'), ('content.edit', '修改运营内容'),
  ('map.read', '查看地图数据'), ('map.edit', '修改地图数据'), ('map.review', '审核地图贡献'),
  ('classroom.read', '查看空教室数据'), ('classroom.manage', '管理空教室数据'),
  ('release.read', '查看发布记录'), ('release.manage', '管理发布记录'),
  ('config.read', '查看配置'), ('config.edit', '修改配置'),
  ('admin.read', '查看管理员'), ('admin.manage', '管理管理员与权限'),
  ('audit.read', '查看审计日志'),
  ('rental.read', '查看租房'), ('rental.review', '审核租房'),
  ('merchant.read', '查看商户'), ('merchant.review', '审核商户'), ('merchant.edit', '修改商户')
ON CONFLICT (permission_key) DO UPDATE SET description=EXCLUDED.description;

INSERT INTO admin_role_permissions(role_key, permission_key)
SELECT 'super_admin', permission_key FROM admin_permissions
ON CONFLICT DO NOTHING;

INSERT INTO admin_role_permissions(role_key, permission_key) VALUES
  ('content_admin','dashboard.read'), ('content_admin','content.read'), ('content_admin','content.edit'),
  ('map_editor','dashboard.read'), ('map_editor','map.read'), ('map_editor','map.edit'), ('map_editor','map.review'),
  ('risk_admin','dashboard.read'), ('risk_admin','user.read'), ('risk_admin','user.ban'), ('risk_admin','risk.read'), ('risk_admin','risk.manage'),
  ('analyst','dashboard.read'), ('analyst','analytics.read'), ('analyst','crash.read'),
  ('rental_reviewer','dashboard.read'), ('rental_reviewer','rental.read'), ('rental_reviewer','rental.review'),
  ('merchant_operator','dashboard.read'), ('merchant_operator','merchant.read'), ('merchant_operator','merchant.review'), ('merchant_operator','merchant.edit')
ON CONFLICT DO NOTHING;
