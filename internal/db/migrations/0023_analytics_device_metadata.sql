-- Latest consented device display metadata for each authenticated app user.
ALTER TABLE analytics_user_versions
  ADD COLUMN IF NOT EXISTS os_version VARCHAR(64),
  ADD COLUMN IF NOT EXISTS device_brand VARCHAR(64),
  ADD COLUMN IF NOT EXISTS device_model VARCHAR(64);

