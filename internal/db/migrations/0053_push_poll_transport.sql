ALTER TABLE push_devices
  ADD COLUMN IF NOT EXISTS poll_secret_hash TEXT NOT NULL DEFAULT '';

ALTER TABLE push_devices
  DROP CONSTRAINT IF EXISTS push_devices_provider_check;

ALTER TABLE push_devices
  ADD CONSTRAINT push_devices_provider_check
  CHECK (provider IN ('fcm', 'apns', 'poll'));

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'push_devices'::regclass
      AND conname = 'push_devices_transport_token_check'
  ) THEN
    ALTER TABLE push_devices
      ADD CONSTRAINT push_devices_transport_token_check
      CHECK (provider = 'poll' OR push_token <> '');
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'push_devices'::regclass
      AND conname = 'push_devices_poll_secret_check'
  ) THEN
    ALTER TABLE push_devices
      ADD CONSTRAINT push_devices_poll_secret_check
      CHECK (provider <> 'poll' OR poll_secret_hash <> '');
  END IF;
END $$;
