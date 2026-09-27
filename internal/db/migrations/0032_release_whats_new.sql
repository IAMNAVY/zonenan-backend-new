ALTER TABLE app_releases ADD COLUMN IF NOT EXISTS whats_new JSONB;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'app_releases_whats_new_object'
  ) THEN
    ALTER TABLE app_releases ADD CONSTRAINT app_releases_whats_new_object
      CHECK (whats_new IS NULL OR jsonb_typeof(whats_new) = 'object');
  END IF;
END $$;

