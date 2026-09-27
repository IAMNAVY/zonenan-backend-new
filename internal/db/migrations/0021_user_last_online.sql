-- Store server-observed authenticated activity separately from product analytics.
ALTER TABLE zonenan_users
  ADD COLUMN IF NOT EXISTS last_online_at TIMESTAMPTZ;

