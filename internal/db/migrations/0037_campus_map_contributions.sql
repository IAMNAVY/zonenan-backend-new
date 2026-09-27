-- Community submitted campus-map additions and corrections. Only reviewed
-- official places are exposed by the public campus-map endpoints.
CREATE TABLE IF NOT EXISTS campus_map_contributions (
  id              BIGSERIAL PRIMARY KEY,
  user_id         BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  kind            TEXT NOT NULL CHECK (kind IN ('new_place', 'correction')),
  place_id        BIGINT REFERENCES campus_map_places(id) ON DELETE SET NULL,
  proposed_place  JSONB,
  message         TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'approved', 'rejected')),
  review_note     TEXT NOT NULL DEFAULT '',
  reviewed_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (
    (kind = 'new_place' AND proposed_place IS NOT NULL AND place_id IS NULL) OR
    (kind = 'correction' AND place_id IS NOT NULL)
  )
);

CREATE INDEX IF NOT EXISTS idx_campus_map_contributions_review
  ON campus_map_contributions(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_campus_map_contributions_user
  ON campus_map_contributions(user_id, created_at DESC);


