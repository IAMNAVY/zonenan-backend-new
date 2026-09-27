-- Allow each published release to choose how clients announce it.
ALTER TABLE app_releases
  ADD COLUMN IF NOT EXISTS update_mode TEXT NOT NULL DEFAULT 'popup';

UPDATE app_releases SET update_mode='popup'
 WHERE update_mode IS NULL OR update_mode='';

ALTER TABLE app_releases
  DROP CONSTRAINT IF EXISTS app_releases_update_mode_check;
ALTER TABLE app_releases
  ADD CONSTRAINT app_releases_update_mode_check
  CHECK (update_mode IN ('popup', 'silent'));

