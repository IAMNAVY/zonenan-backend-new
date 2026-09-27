CREATE TABLE IF NOT EXISTS job_runs (
  id BIGSERIAL PRIMARY KEY, job_name VARCHAR(100) NOT NULL, run_key VARCHAR(160) NOT NULL UNIQUE,
  scheduled_at TIMESTAMPTZ NOT NULL, started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), finished_at TIMESTAMPTZ,
  status VARCHAR(20) NOT NULL DEFAULT 'running' CHECK(status IN ('running','succeeded','failed','skipped')),
  processed_count BIGINT NOT NULL DEFAULT 0, retry_count INTEGER NOT NULL DEFAULT 0,
  error_summary TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS job_runs_recent_idx ON job_runs(job_name,started_at DESC);
CREATE TABLE IF NOT EXISTS notification_outbox (
  id BIGSERIAL PRIMARY KEY, recipient_type VARCHAR(30) NOT NULL, recipient_id BIGINT NOT NULL,
  event_type VARCHAR(80) NOT NULL, payload JSONB NOT NULL DEFAULT '{}', status VARCHAR(20) NOT NULL DEFAULT 'pending',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), delivered_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS merchant_events_installation_dedupe_idx ON merchant_events(merchant_id,installation_hash,event_type,source,dedupe_bucket) WHERE installation_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS rental_reports_duplicate_idx ON rental_reports(reporter_user_id,listing_id,status);
INSERT INTO admin_permissions(permission_key,description) VALUES ('jobs.read','查看后台任务') ON CONFLICT(permission_key) DO UPDATE SET description=EXCLUDED.description;
INSERT INTO admin_role_permissions(role_key,permission_key) SELECT r.role_key,p.permission_key FROM admin_roles r CROSS JOIN admin_permissions p WHERE r.role_key='super_admin' AND p.permission_key='jobs.read' ON CONFLICT DO NOTHING;
