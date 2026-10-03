-- +goose Up
ALTER TABLE request_logs
  ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;

UPDATE request_logs
SET started_at = CASE
  WHEN metadata ->> 'started_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([.][0-9]+)?Z$'
    THEN (metadata ->> 'started_at')::timestamptz
  ELSE created_at
END
WHERE started_at IS NULL;

ALTER TABLE request_logs
  ALTER COLUMN started_at SET DEFAULT NOW(),
  ALTER COLUMN started_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS request_logs_started_at_created_id_idx
  ON request_logs (started_at DESC, created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS request_logs_started_at_created_id_idx;
ALTER TABLE request_logs DROP COLUMN IF EXISTS started_at;
