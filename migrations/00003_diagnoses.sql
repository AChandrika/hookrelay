-- +goose Up

-- The placeholder diagnoses table from 00001 was never used. Replace it with a
-- job table: rows are queued, claimed by a worker, and completed, the same
-- pattern as deliveries.
DROP TABLE diagnoses;

CREATE TYPE diagnosis_status AS ENUM ('pending', 'running', 'done', 'failed');

CREATE TABLE diagnoses (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_id        uuid NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
    endpoint_id        uuid NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    status             diagnosis_status NOT NULL DEFAULT 'pending',
    fingerprint        text,          -- identifies "the same failure" for caching
    model              text,          -- e.g. qwen3:4b, or "rules" for the fallback
    prompt_version     text,          -- cache and evals are per prompt version
    result             jsonb,         -- validated diagnosis JSON
    note               text,          -- why a fallback was used, or why it failed
    cached             boolean NOT NULL DEFAULT false,
    duration_ms        int,
    prompt_tokens      int,
    completion_tokens  int,
    locked_until       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- At most one queued or running diagnosis per delivery, so double-clicking
-- "Diagnose" (or the worker auto-queueing at the same time) can't create two.
CREATE UNIQUE INDEX diagnoses_one_active_idx ON diagnoses (delivery_id) WHERE status IN ('pending', 'running');
CREATE INDEX diagnoses_queue_idx    ON diagnoses (created_at) WHERE status = 'pending';
CREATE INDEX diagnoses_cache_idx    ON diagnoses (fingerprint, created_at DESC) WHERE status = 'done';
CREATE INDEX diagnoses_delivery_idx ON diagnoses (delivery_id, created_at DESC);

-- +goose Down
DROP TABLE diagnoses;
DROP TYPE diagnosis_status;
CREATE TABLE diagnoses (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id     uuid NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    fingerprint     text NOT NULL,
    model           text NOT NULL,
    prompt_version  text NOT NULL,
    result          jsonb NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX diagnoses_lookup_idx ON diagnoses (endpoint_id, fingerprint, created_at DESC);
