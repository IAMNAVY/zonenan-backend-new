-- Upgrade installations created by the initial analytics migration from a
-- single latest-user mapping to a shared-device-safe many-user mapping.
ALTER TABLE analytics_installations
  DROP CONSTRAINT IF EXISTS analytics_installations_pkey;
ALTER TABLE analytics_installations
  ADD CONSTRAINT analytics_installations_pkey
  PRIMARY KEY (installation_hash, user_hash);

