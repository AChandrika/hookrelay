-- +goose Up

-- Replaying a failed delivery gives it a fresh retry budget. Attempt numbers
-- keep counting up so the full history is preserved; the worker counts
-- retries from retry_base instead of from zero.
ALTER TABLE deliveries ADD COLUMN retry_base int NOT NULL DEFAULT 0;

-- Dashboard lists, newest first, paginated by (created_at, id).
CREATE INDEX events_tenant_created_idx      ON events (tenant_id, created_at DESC, id DESC);
CREATE INDEX deliveries_status_created_idx  ON deliveries (status, created_at DESC, id DESC);
CREATE INDEX delivery_attempts_started_idx  ON delivery_attempts (started_at);

-- +goose Down
DROP INDEX delivery_attempts_started_idx;
DROP INDEX deliveries_status_created_idx;
DROP INDEX events_tenant_created_idx;
ALTER TABLE deliveries DROP COLUMN retry_base;
