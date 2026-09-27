CREATE TABLE IF NOT EXISTS merchants (
  id BIGSERIAL PRIMARY KEY,
  name VARCHAR(160) NOT NULL,
  merchant_type VARCHAR(40) NOT NULL DEFAULT 'restaurant' CHECK (merchant_type IN ('restaurant','beverage','printing','life_service','commercial_apartment','partner','other')),
  status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','suspended')),
  contact_name VARCHAR(120) NOT NULL DEFAULT '', contact_phone VARCHAR(40) NOT NULL DEFAULT '',
  certification_note TEXT NOT NULL DEFAULT '', review_note TEXT NOT NULL DEFAULT '',
  reviewed_by BIGINT REFERENCES admin_users(id), reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS merchant_users (
  id BIGSERIAL PRIMARY KEY, merchant_id BIGINT REFERENCES merchants(id) ON DELETE SET NULL,
  email VARCHAR(320), phone VARCHAR(40), password_hash TEXT NOT NULL, display_name VARCHAR(120) NOT NULL,
  role VARCHAR(30) NOT NULL DEFAULT 'owner' CHECK (role IN ('owner','manager','editor','analyst')),
  status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','rejected','disabled')),
  registration_source VARCHAR(20) NOT NULL DEFAULT 'self' CHECK (registration_source IN ('self','admin')),
  last_login_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (email IS NOT NULL OR phone IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS merchant_users_email_unique ON merchant_users(lower(email)) WHERE email IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS merchant_users_phone_unique ON merchant_users(phone) WHERE phone IS NOT NULL;

CREATE TABLE IF NOT EXISTS merchant_sessions (
  id VARCHAR(64) PRIMARY KEY, merchant_user_id BIGINT NOT NULL REFERENCES merchant_users(id) ON DELETE CASCADE,
  token_hash BYTEA NOT NULL UNIQUE, csrf_hash BYTEA NOT NULL, ip INET, user_agent TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NOT NULL, revoked_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS merchant_stores (
  id BIGSERIAL PRIMARY KEY, merchant_id BIGINT NOT NULL UNIQUE REFERENCES merchants(id) ON DELETE CASCADE,
  name VARCHAR(160) NOT NULL, category VARCHAR(40) NOT NULL DEFAULT 'other', tags TEXT[] NOT NULL DEFAULT '{}',
  address VARCHAR(500) NOT NULL DEFAULT '', poi_id BIGINT, latitude DOUBLE PRECISION, longitude DOUBLE PRECISION,
  contact_phone VARCHAR(40) NOT NULL DEFAULT '', contact_wechat VARCHAR(120) NOT NULL DEFAULT '',
  business_hours JSONB NOT NULL DEFAULT '[]', average_price INTEGER CHECK (average_price IS NULL OR average_price >= 0),
  description TEXT NOT NULL DEFAULT '', logo_url TEXT NOT NULL DEFAULT '', cover_urls TEXT[] NOT NULL DEFAULT '{}',
  status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending','published','rejected','suspended')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS merchant_menu_categories (
  id BIGSERIAL PRIMARY KEY, merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
  name VARCHAR(120) NOT NULL, sort INTEGER NOT NULL DEFAULT 0, active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS merchant_menu_items (
  id BIGSERIAL PRIMARY KEY, merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
  category_id BIGINT REFERENCES merchant_menu_categories(id) ON DELETE SET NULL, name VARCHAR(160) NOT NULL,
  description TEXT NOT NULL DEFAULT '', price_cents INTEGER NOT NULL CHECK (price_cents >= 0), image_url TEXT NOT NULL DEFAULT '',
  recommended BOOLEAN NOT NULL DEFAULT FALSE, is_new BOOLEAN NOT NULL DEFAULT FALSE, available BOOLEAN NOT NULL DEFAULT TRUE,
  sort INTEGER NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS merchant_promotions (
  id BIGSERIAL PRIMARY KEY, merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
  promotion_type VARCHAR(30) NOT NULL CHECK (promotion_type IN ('discount','new_store','student','event')),
  title VARCHAR(160) NOT NULL, description TEXT NOT NULL DEFAULT '', starts_at TIMESTAMPTZ, ends_at TIMESTAMPTZ,
  status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending','approved','rejected','disabled')),
  review_note TEXT NOT NULL DEFAULT '', reviewed_by BIGINT REFERENCES admin_users(id), reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS merchant_apartments (
  id BIGSERIAL PRIMARY KEY, merchant_id BIGINT NOT NULL UNIQUE REFERENCES merchants(id) ON DELETE CASCADE,
  name VARCHAR(160) NOT NULL, description TEXT NOT NULL DEFAULT '', poi_id BIGINT, address VARCHAR(500) NOT NULL DEFAULT '',
  latitude DOUBLE PRECISION, longitude DOUBLE PRECISION, contact_phone VARCHAR(40) NOT NULL DEFAULT '',
  status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending','published','rejected','suspended')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS merchant_apartment_rooms (
  id BIGSERIAL PRIMARY KEY, merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
  apartment_id BIGINT NOT NULL REFERENCES merchant_apartments(id) ON DELETE CASCADE, name VARCHAR(160) NOT NULL,
  rent_cents INTEGER NOT NULL CHECK (rent_cents >= 0), area_sqm NUMERIC(8,2), payment_terms VARCHAR(80) NOT NULL DEFAULT '',
  available_from DATE, status VARCHAR(20) NOT NULL DEFAULT 'available' CHECK (status IN ('available','reserved','unavailable')),
  image_urls TEXT[] NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS merchant_claims (
  id BIGSERIAL PRIMARY KEY, merchant_user_id BIGINT NOT NULL REFERENCES merchant_users(id) ON DELETE CASCADE,
  merchant_id BIGINT REFERENCES merchants(id) ON DELETE CASCADE, claimed_name VARCHAR(160) NOT NULL,
  evidence TEXT NOT NULL DEFAULT '', status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
  review_note TEXT NOT NULL DEFAULT '', reviewed_by BIGINT REFERENCES admin_users(id), reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS merchant_metrics_daily (
  merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE, date DATE NOT NULL,
  views BIGINT NOT NULL DEFAULT 0, favorites BIGINT NOT NULL DEFAULT 0, contacts BIGINT NOT NULL DEFAULT 0,
  map_impressions BIGINT NOT NULL DEFAULT 0, search_impressions BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY(merchant_id,date)
);
CREATE TABLE IF NOT EXISTS merchant_audit_logs (
  id BIGSERIAL PRIMARY KEY, actor_type VARCHAR(20) NOT NULL, actor_id BIGINT, merchant_id BIGINT,
  action VARCHAR(120) NOT NULL, resource_type VARCHAR(80) NOT NULL, resource_id VARCHAR(120) NOT NULL DEFAULT '',
  metadata JSONB NOT NULL DEFAULT '{}', ip INET, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO app_settings(key,value) VALUES
  ('merchant_self_registration_enabled','false'),('merchant_image_storage_driver','local'),
  ('merchant_image_max_bytes','5242880') ON CONFLICT(key) DO NOTHING;

INSERT INTO admin_permissions(permission_key,description) VALUES
  ('merchant.read','查看商户与商户账号'),('merchant.manage','创建及修改商户'),('merchant.review','审核商户、认领与活动')
ON CONFLICT(permission_key) DO NOTHING;
INSERT INTO admin_role_permissions(role_key,permission_key)
SELECT 'super_admin',permission_key FROM admin_permissions WHERE permission_key LIKE 'merchant.%'
ON CONFLICT DO NOTHING;
INSERT INTO admin_role_permissions(role_key,permission_key) VALUES
  ('merchant_operator','merchant.read'),('merchant_operator','merchant.manage'),('merchant_operator','merchant.review')
ON CONFLICT DO NOTHING;

CREATE INDEX IF NOT EXISTS merchant_users_merchant_idx ON merchant_users(merchant_id);
CREATE INDEX IF NOT EXISTS merchant_items_owner_idx ON merchant_menu_items(merchant_id);
CREATE INDEX IF NOT EXISTS merchant_promotions_owner_idx ON merchant_promotions(merchant_id,status);
