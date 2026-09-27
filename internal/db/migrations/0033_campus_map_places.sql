-- Official campus map places. Campus metadata itself stays in the client so that
-- an empty/new backend can still switch campuses; only reviewed place data lives here.
CREATE TABLE IF NOT EXISTS campus_map_places (
  id                BIGSERIAL PRIMARY KEY,
  campus_id         VARCHAR(32) NOT NULL CHECK (campus_id ~ '^[a-z0-9_]+$'),
  name              VARCHAR(120) NOT NULL CHECK (BTRIM(name) <> ''),
  latitude          DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
  longitude         DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
  coordinate_system VARCHAR(16) NOT NULL DEFAULT 'CGCS2000'
                    CHECK (coordinate_system IN ('CGCS2000', 'WGS84')),
  place_type        VARCHAR(24) NOT NULL
                    CHECK (place_type IN (
                      'teaching', 'library', 'dormitory', 'dining', 'sports',
                      'landscape', 'parking', 'gate', 'transport', 'service'
                    )),
  address           TEXT NOT NULL DEFAULT '',
  description       TEXT NOT NULL DEFAULT '',
  aliases           TEXT[] NOT NULL DEFAULT '{}',
  sort              INTEGER NOT NULL DEFAULT 0,
  active            BOOLEAN NOT NULL DEFAULT TRUE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_campus_map_places_public
  ON campus_map_places(campus_id, active, sort DESC, id);
CREATE INDEX IF NOT EXISTS idx_campus_map_places_updated
  ON campus_map_places(updated_at DESC);

