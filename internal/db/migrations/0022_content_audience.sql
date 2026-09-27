-- 统一运营内容受众：enabled=全体，beta=beta名单，disabled=全部关闭，admin=管理员。
CREATE TABLE IF NOT EXISTS beta_memberships (
  user_id BIGINT PRIMARY KEY REFERENCES zonenan_users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE announcements
  ADD COLUMN IF NOT EXISTS release_state TEXT NOT NULL DEFAULT 'enabled';
ALTER TABLE home_ads
  ADD COLUMN IF NOT EXISTS release_state TEXT NOT NULL DEFAULT 'enabled';

UPDATE announcements SET release_state = 'enabled' WHERE release_state IS NULL OR release_state = '';
UPDATE home_ads SET release_state = 'enabled' WHERE release_state IS NULL OR release_state = '';

ALTER TABLE announcements DROP CONSTRAINT IF EXISTS announcements_release_state_check;
ALTER TABLE announcements ADD CONSTRAINT announcements_release_state_check
  CHECK (release_state IN ('enabled', 'beta', 'disabled', 'admin'));
ALTER TABLE home_ads DROP CONSTRAINT IF EXISTS home_ads_release_state_check;
ALTER TABLE home_ads ADD CONSTRAINT home_ads_release_state_check
  CHECK (release_state IN ('enabled', 'beta', 'disabled', 'admin'));

CREATE INDEX IF NOT EXISTS idx_beta_memberships_user ON beta_memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_announcements_release_state ON announcements(release_state);
CREATE INDEX IF NOT EXISTS idx_home_ads_release_state ON home_ads(release_state);

