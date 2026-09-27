CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS pois (
  id BIGSERIAL PRIMARY KEY,
  type VARCHAR(50) NOT NULL,
  name VARCHAR(180) NOT NULL,
  latitude DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
  longitude DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
  campus_id VARCHAR(50),
  address VARCHAR(500) NOT NULL DEFAULT '',
  building_id BIGINT REFERENCES pois(id) ON DELETE SET NULL,
  parent_poi_id BIGINT REFERENCES pois(id) ON DELETE SET NULL,
  category VARCHAR(50) NOT NULL DEFAULT 'other', aliases TEXT[] NOT NULL DEFAULT '{}',
  coordinate_system VARCHAR(30) NOT NULL DEFAULT 'CGCS2000',
  floor VARCHAR(40) NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('draft','pending','active','rejected','disabled')),
  source VARCHAR(30) NOT NULL DEFAULT 'admin' CHECK (source IN ('admin','campus_map','user','merchant','migration')),
  source_user_id BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  legacy_place_id BIGINT UNIQUE REFERENCES campus_map_places(id) ON DELETE SET NULL,
  verified_level SMALLINT NOT NULL DEFAULT 0 CHECK (verified_level BETWEEN 0 AND 3),
  verified_at TIMESTAMPTZ,
  sort_order INTEGER NOT NULL DEFAULT 0, deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_merchant_type_check;
ALTER TABLE merchants ADD CONSTRAINT merchants_merchant_type_check CHECK (merchant_type IN ('restaurant','beverage','printing','life_service','commercial_apartment','individual_landlord','partner','other'));
ALTER TABLE pois ADD COLUMN IF NOT EXISTS location geography(Point,4326)
  GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(longitude,latitude),4326)::geography) STORED;
CREATE INDEX IF NOT EXISTS pois_location_gix ON pois USING GIST(location);
CREATE INDEX IF NOT EXISTS pois_campus_type_status_idx ON pois(campus_id,type,status);
CREATE INDEX IF NOT EXISTS pois_coordinates_idx ON pois(latitude,longitude);

INSERT INTO pois(type,category,name,aliases,latitude,longitude,campus_id,address,floor,description,coordinate_system,sort_order,status,source,legacy_place_id,verified_level,verified_at,created_at,updated_at)
SELECT COALESCE(NULLIF(place_types[1],''),place_type),category,name,aliases,latitude,longitude,campus_id,address,'',description,coordinate_system,sort,
       CASE WHEN active THEN 'active' ELSE 'disabled' END,'campus_map',id,2,updated_at,created_at,updated_at
FROM campus_map_places ON CONFLICT(legacy_place_id) DO NOTHING;

ALTER TABLE merchant_stores ADD COLUMN IF NOT EXISTS unified_poi_id BIGINT REFERENCES pois(id) ON DELETE SET NULL;
ALTER TABLE merchant_apartments ADD COLUMN IF NOT EXISTS unified_poi_id BIGINT REFERENCES pois(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS poi_metadata (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton), revision BIGINT NOT NULL DEFAULT 1,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO poi_metadata(singleton) VALUES(TRUE) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS poi_contributions (
  id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  proposed_data JSONB NOT NULL, status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')),
  review_note TEXT NOT NULL DEFAULT '', reviewed_by BIGINT REFERENCES admin_users(id), reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE OR REPLACE FUNCTION bump_poi_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN UPDATE poi_metadata SET revision=revision+1,updated_at=NOW() WHERE singleton=TRUE; RETURN COALESCE(NEW,OLD); END $$;
DROP TRIGGER IF EXISTS pois_revision_trigger ON pois;
CREATE TRIGGER pois_revision_trigger AFTER INSERT OR UPDATE OR DELETE ON pois FOR EACH STATEMENT EXECUTE FUNCTION bump_poi_revision();

CREATE TABLE IF NOT EXISTS rental_listings (
  id BIGSERIAL PRIMARY KEY,
  owner_user_id BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  merchant_id BIGINT REFERENCES merchants(id) ON DELETE SET NULL,
  listing_type VARCHAR(30) NOT NULL CHECK (listing_type IN ('student_sublet','student_wanted','individual_landlord','commercial_apartment')),
  title VARCHAR(180) NOT NULL, description TEXT NOT NULL DEFAULT '', campus_id VARCHAR(50) NOT NULL,
  poi_id BIGINT REFERENCES pois(id) ON DELETE SET NULL, address VARCHAR(500) NOT NULL DEFAULT '',
  latitude DOUBLE PRECISION, longitude DOUBLE PRECISION,
  rent_cents INTEGER CHECK (rent_cents IS NULL OR rent_cents >= 0), area_sqm NUMERIC(8,2),
  payment_terms VARCHAR(80) NOT NULL DEFAULT '', available_from DATE,
  contact_name VARCHAR(120) NOT NULL DEFAULT '', contact_value VARCHAR(200) NOT NULL DEFAULT '',
  image_urls TEXT[] NOT NULL DEFAULT '{}', attributes JSONB NOT NULL DEFAULT '{}',
  status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('draft','pending','published','rejected','off_shelf','expired','removed')),
  review_note TEXT NOT NULL DEFAULT '', reviewed_by BIGINT REFERENCES admin_users(id), reviewed_at TIMESTAMPTZ,
  submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), published_at TIMESTAMPTZ, expires_at TIMESTAMPTZ,
  off_shelf_at TIMESTAMPTZ, removed_at TIMESTAMPTZ, removal_reason TEXT NOT NULL DEFAULT '',
  view_count BIGINT NOT NULL DEFAULT 0, contact_count BIGINT NOT NULL DEFAULT 0, favorite_count BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (owner_user_id IS NOT NULL OR merchant_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS rental_listings_public_idx ON rental_listings(status,campus_id,listing_type,expires_at);
ALTER TABLE rental_listings ADD COLUMN IF NOT EXISTS location geography(Point,4326)
  GENERATED ALWAYS AS (CASE WHEN longitude IS NULL OR latitude IS NULL THEN NULL ELSE ST_SetSRID(ST_MakePoint(longitude,latitude),4326)::geography END) STORED;
CREATE INDEX IF NOT EXISTS rental_listings_location_gix ON rental_listings USING GIST(location);
CREATE INDEX IF NOT EXISTS rental_listings_owner_idx ON rental_listings(owner_user_id,created_at DESC);
CREATE TABLE IF NOT EXISTS rental_favorites (
  user_id BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  listing_id BIGINT NOT NULL REFERENCES rental_listings(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(user_id,listing_id)
);
CREATE TABLE IF NOT EXISTS rental_reports (
  id BIGSERIAL PRIMARY KEY, listing_id BIGINT NOT NULL REFERENCES rental_listings(id) ON DELETE CASCADE,
  reporter_user_id BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  reason VARCHAR(80) NOT NULL, detail TEXT NOT NULL DEFAULT '',
  status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processed','rejected')),
  handled_by BIGINT REFERENCES admin_users(id), handled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS rental_contacts (
  id BIGSERIAL PRIMARY KEY, listing_id BIGINT NOT NULL REFERENCES rental_listings(id) ON DELETE CASCADE,
  user_id BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS merchant_favorites (
  user_id BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(user_id,merchant_id)
);
CREATE TABLE IF NOT EXISTS merchant_events (
  id BIGSERIAL PRIMARY KEY, merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
  user_id BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  installation_hash BYTEA, event_type VARCHAR(30) NOT NULL CHECK(event_type IN ('impression','view','favorite','contact','map_impression','search_impression')),
  source VARCHAR(30) NOT NULL DEFAULT 'app', occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  dedupe_bucket TIMESTAMPTZ NOT NULL DEFAULT date_trunc('hour',NOW())
);
CREATE UNIQUE INDEX IF NOT EXISTS merchant_events_user_dedupe_idx ON merchant_events(merchant_id,user_id,event_type,dedupe_bucket) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS merchant_events_metrics_idx ON merchant_events(merchant_id,occurred_at,event_type);

CREATE OR REPLACE FUNCTION merchant_is_open(hours JSONB, at_time TIMESTAMPTZ DEFAULT NOW()) RETURNS BOOLEAN
LANGUAGE plpgsql STABLE AS $$
DECLARE local_time TIMESTAMP := at_time AT TIME ZONE 'Asia/Shanghai'; item JSONB; open_t TIME; close_t TIME; weekday INT;
BEGIN
  IF hours IS NULL OR jsonb_typeof(hours)<>'array' OR jsonb_array_length(hours)=0 THEN RETURN FALSE; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(hours) x WHERE x->>'temporary_closed'='true') THEN RETURN FALSE; END IF;
  weekday := EXTRACT(ISODOW FROM local_time)::INT;
  FOR item IN SELECT value FROM jsonb_array_elements(hours) LOOP
    IF COALESCE((item->>'day')::INT,0)=weekday AND item ? 'open' AND item ? 'close' THEN
      open_t := (item->>'open')::TIME; close_t := (item->>'close')::TIME;
      IF (close_t>open_t AND local_time::TIME>=open_t AND local_time::TIME<close_t)
         OR (close_t<=open_t AND (local_time::TIME>=open_t OR local_time::TIME<close_t)) THEN RETURN TRUE; END IF;
    END IF;
  END LOOP;
  RETURN FALSE;
EXCEPTION WHEN OTHERS THEN RETURN FALSE;
END $$;

INSERT INTO admin_permissions(permission_key,description) VALUES
 ('poi.read','查看统一 POI'),('poi.manage','管理统一 POI'),
 ('poi.review','审核 POI'),('rental.read','查看租房'),('rental.review','审核租房'),('rental.manage','管理租房'),
 ('merchant.analytics','查看商户推荐统计')
ON CONFLICT(permission_key) DO UPDATE SET description=EXCLUDED.description;
INSERT INTO admin_role_permissions(role_key,permission_key)
SELECT r.role_key,p.permission_key FROM admin_roles r CROSS JOIN admin_permissions p
WHERE r.role_key='super_admin' AND p.permission_key IN ('poi.read','poi.manage','poi.review','rental.read','rental.review','rental.manage','merchant.analytics')
ON CONFLICT DO NOTHING;
INSERT INTO admin_role_permissions(role_key,permission_key)
SELECT r.role_key,p.permission_key FROM admin_roles r CROSS JOIN admin_permissions p
WHERE (r.role_key='map_editor' AND p.permission_key IN ('poi.read','poi.manage','poi.review')) OR
      (r.role_key='rental_reviewer' AND p.permission_key IN ('rental.read','rental.review','rental.manage'))
ON CONFLICT DO NOTHING;
