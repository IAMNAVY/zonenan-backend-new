-- Membership order processing fixes: one order must be idempotent across
-- webhook and API sync, and the admin event view needs the validated totals.
DROP INDEX IF EXISTS idx_membership_grants_external;
CREATE UNIQUE INDEX IF NOT EXISTS idx_membership_grants_external
  ON membership_grants(external_ref, membership_type_key)
  WHERE external_ref <> '';

ALTER TABLE membership_events
  ADD COLUMN IF NOT EXISTS month INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS total_amount TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS show_amount TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS product_type INTEGER NOT NULL DEFAULT 0;

