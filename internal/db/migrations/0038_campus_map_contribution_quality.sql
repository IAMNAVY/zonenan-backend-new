-- Duplicate suppression without a daily submission quota.
ALTER TABLE campus_map_contributions
  ADD COLUMN IF NOT EXISTS fingerprint TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_campus_map_contributions_pending_duplicate
  ON campus_map_contributions(user_id, fingerprint)
  WHERE status = 'pending' AND fingerprint <> '';

CREATE INDEX IF NOT EXISTS idx_campus_map_contributions_corroboration
  ON campus_map_contributions(fingerprint, status)
  WHERE fingerprint <> '';

