ALTER TABLE notification_outbox ADD COLUMN IF NOT EXISTS dedupe_key VARCHAR(200);
CREATE UNIQUE INDEX IF NOT EXISTS notification_outbox_dedupe_idx ON notification_outbox(dedupe_key) WHERE dedupe_key IS NOT NULL;
