-- Lightweight change token for clients. Admin mutations bump the revision in the
-- same transaction as place data so clients can avoid downloading an unchanged list.
CREATE TABLE IF NOT EXISTS campus_map_metadata (
  singleton    BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  revision     BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  active_count INTEGER NOT NULL DEFAULT 0 CHECK (active_count >= 0),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO campus_map_metadata(singleton, revision, active_count)
VALUES (TRUE, 1, (SELECT COUNT(*) FROM campus_map_places WHERE active = TRUE))
ON CONFLICT (singleton) DO NOTHING;

