-- +goose Up
-- gen_random_uuid() is built into Postgres 13+.

CREATE TYPE delivery_status AS ENUM ('pending', 'in_flight', 'succeeded', 'dead');

CREATE TABLE tenants (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Never store raw API keys. Store sha256(key); show only the prefix in the UI.
CREATE TABLE api_keys (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prefix      text NOT NULL,
    key_hash    bytea NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);

CREATE TABLE endpoints (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    url              text NOT NULL,
    secret           text NOT NULL,
    event_types      text[] NOT NULL DEFAULT '{}',
    enabled          boolean NOT NULL DEFAULT true,
    disabled_reason  text,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX endpoints_tenant_enabled_idx ON endpoints (tenant_id) WHERE enabled;

CREATE TABLE events (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_type       text NOT NULL,
    payload          jsonb NOT NULL,
    idempotency_key  text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, idempotency_key)
);

-- One row per (event, endpoint). This table IS the queue; status 'dead' IS the DLQ.
CREATE TABLE deliveries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id         uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    endpoint_id      uuid NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    status           delivery_status NOT NULL DEFAULT 'pending',
    attempt_count    int NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    locked_until     timestamptz,
    last_error       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, endpoint_id)
);
CREATE INDEX deliveries_due_idx      ON deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX deliveries_stuck_idx    ON deliveries (locked_until)    WHERE status = 'in_flight';
CREATE INDEX deliveries_endpoint_idx ON deliveries (endpoint_id, created_at DESC);

CREATE TABLE delivery_attempts (
    id                bigserial PRIMARY KEY,
    delivery_id       uuid NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
    attempt_number    int NOT NULL,
    started_at        timestamptz NOT NULL,
    duration_ms       int NOT NULL,
    response_status   int,
    response_headers  jsonb,
    response_body     text,
    error             text,
    UNIQUE (delivery_id, attempt_number)
);

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

-- +goose Down
DROP TABLE diagnoses;
DROP TABLE delivery_attempts;
DROP TABLE deliveries;
DROP TABLE events;
DROP TABLE endpoints;
DROP TABLE api_keys;
DROP TABLE tenants;
DROP TYPE delivery_status;