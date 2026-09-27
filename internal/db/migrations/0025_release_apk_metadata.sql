-- APK metadata calculated by the backend from the public Android artifact.
-- Existing rows may remain NULL until their APK URL is revalidated.
ALTER TABLE app_releases
  ADD COLUMN IF NOT EXISTS android_version_name VARCHAR(32),
  ADD COLUMN IF NOT EXISTS android_version_code INTEGER,
  ADD COLUMN IF NOT EXISTS android_size_bytes BIGINT,
  ADD COLUMN IF NOT EXISTS android_verified_at TIMESTAMPTZ;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'app_releases_android_version_code_positive') THEN
    ALTER TABLE app_releases ADD CONSTRAINT app_releases_android_version_code_positive
      CHECK (android_version_code IS NULL OR android_version_code > 0);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'app_releases_android_size_nonnegative') THEN
    ALTER TABLE app_releases ADD CONSTRAINT app_releases_android_size_nonnegative
      CHECK (android_size_bytes IS NULL OR android_size_bytes >= 0);
  END IF;
END $$;

