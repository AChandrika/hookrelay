// Package store holds all SQL. Every query that reads tenant data filters by
// tenant_id, so one tenant can never see another tenant's rows.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

type Tenant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Endpoint struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	EventTypes []string  `json:"event_types"`
	Enabled        bool      `json:"enabled"`
	DisabledReason *string   `json:"disabled_reason,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	Secret         string    `json:"secret,omitempty"` // only returned when the endpoint is created
}

type Event struct {
	ID             string          `json:"id"`
	EventType      string          `json:"event_type"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey *string         `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type Delivery struct {
	ID            string    `json:"id"`
	EndpointID    string    `json:"endpoint_id"`
	Status        string    `json:"status"`
	AttemptCount  int       `json:"attempt_count"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LastError     *string   `json:"last_error,omitempty"`
}

type PublishResult struct {
	Event             Event `json:"event"`
	DeliveriesCreated int64 `json:"deliveries_created"`
	Replayed          bool  `json:"replayed"` // true when the idempotency key was already used
}

// CreateTenantWithKey creates a tenant and its first API key in one transaction,
// so we never end up with a tenant that nobody can authenticate as.
func (s *Store) CreateTenantWithKey(ctx context.Context, name, keyPrefix string, keyHash []byte) (Tenant, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Tenant{}, err
	}
	defer tx.Rollback(ctx) // no-op after a successful Commit

	var t Tenant
	err = tx.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id::text, name, created_at`,
		name,
	).Scan(&t.ID, &t.Name, &t.CreatedAt)
	if err != nil {
		return Tenant{}, fmt.Errorf("insert tenant: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO api_keys (tenant_id, prefix, key_hash) VALUES ($1::uuid, $2, $3)`,
		t.ID, keyPrefix, keyHash,
	)
	if err != nil {
		return Tenant{}, fmt.Errorf("insert api key: %w", err)
	}
	return t, tx.Commit(ctx)
}

// TenantIDForKey resolves an API key hash to its tenant. Revoked keys are rejected.
func (s *Store) TenantIDForKey(ctx context.Context, keyHash []byte) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT tenant_id::text FROM api_keys WHERE key_hash = $1 AND revoked_at IS NULL`,
		keyHash,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) CreateEndpoint(ctx context.Context, tenantID, url, secret string, eventTypes []string) (Endpoint, error) {
	if eventTypes == nil {
		eventTypes = []string{}
	}
	e := Endpoint{Secret: secret}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO endpoints (tenant_id, url, secret, event_types)
		 VALUES ($1::uuid, $2, $3, $4)
		 RETURNING id::text, url, event_types, enabled, created_at`,
		tenantID, url, secret, eventTypes,
	).Scan(&e.ID, &e.URL, &e.EventTypes, &e.Enabled, &e.CreatedAt)
	if err != nil {
		return Endpoint{}, fmt.Errorf("insert endpoint: %w", err)
	}
	return e, nil
}

// ListEndpoints never returns secrets.
func (s *Store) ListEndpoints(ctx context.Context, tenantID string) ([]Endpoint, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, url, event_types, enabled, disabled_reason, created_at
		 FROM endpoints WHERE tenant_id = $1::uuid ORDER BY created_at`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Endpoint{}
	for rows.Next() {
		var e Endpoint
		if err := rows.Scan(&e.ID, &e.URL, &e.EventTypes, &e.Enabled, &e.DisabledReason, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PublishEvent stores the event and fans it out to every matching endpoint in a
// single transaction: either the event and all its deliveries exist, or none do.
//
// Idempotency: the UNIQUE (tenant_id, idempotency_key) constraint does the real
// work. If two requests with the same key arrive at the same time, the second
// INSERT waits for the first transaction, then hits the conflict and returns no
// row. We then load and return the original event instead of creating a duplicate.
func (s *Store) PublishEvent(ctx context.Context, tenantID, eventType string, payload json.RawMessage, idemKey *string) (PublishResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PublishResult{}, err
	}
	defer tx.Rollback(ctx)

	var res PublishResult
	ev := &res.Event
	err = tx.QueryRow(ctx,
		`INSERT INTO events (tenant_id, event_type, payload, idempotency_key)
		 VALUES ($1::uuid, $2, $3, $4)
		 ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
		 RETURNING id::text, event_type, payload, idempotency_key, created_at`,
		tenantID, eventType, payload, idemKey,
	).Scan(&ev.ID, &ev.EventType, &ev.Payload, &ev.IdempotencyKey, &ev.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`SELECT id::text, event_type, payload, idempotency_key, created_at
			 FROM events WHERE tenant_id = $1::uuid AND idempotency_key = $2`,
			tenantID, idemKey,
		).Scan(&ev.ID, &ev.EventType, &ev.Payload, &ev.IdempotencyKey, &ev.CreatedAt)
		if err != nil {
			return PublishResult{}, fmt.Errorf("load existing event: %w", err)
		}
		res.Replayed = true
		return res, tx.Commit(ctx)
	}
	if err != nil {
		return PublishResult{}, fmt.Errorf("insert event: %w", err)
	}

	// The ::uuid cast is required: inside INSERT ... SELECT, Postgres can't
	// infer the type of a bare $1 in the SELECT list and would treat it as text.
	tag, err := tx.Exec(ctx,
		`INSERT INTO deliveries (event_id, endpoint_id)
		 SELECT $1::uuid, id FROM endpoints
		 WHERE tenant_id = $2::uuid
		   AND enabled
		   AND (cardinality(event_types) = 0 OR $3::text = ANY (event_types))`,
		ev.ID, tenantID, eventType,
	)
	if err != nil {
		return PublishResult{}, fmt.Errorf("fan out deliveries: %w", err)
	}
	res.DeliveriesCreated = tag.RowsAffected()
	return res, tx.Commit(ctx)
}

// GetEvent returns an event and its deliveries, scoped to the tenant.
func (s *Store) GetEvent(ctx context.Context, tenantID, eventID string) (Event, []Delivery, error) {
	var ev Event
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, event_type, payload, idempotency_key, created_at
		 FROM events WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		eventID, tenantID,
	).Scan(&ev.ID, &ev.EventType, &ev.Payload, &ev.IdempotencyKey, &ev.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, nil, ErrNotFound
	}
	if err != nil {
		return Event{}, nil, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id::text, endpoint_id::text, status::text, attempt_count, next_attempt_at, last_error
		 FROM deliveries WHERE event_id = $1::uuid ORDER BY created_at`,
		eventID,
	)
	if err != nil {
		return Event{}, nil, err
	}
	defer rows.Close()

	deliveries := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.Status, &d.AttemptCount, &d.NextAttemptAt, &d.LastError); err != nil {
			return Event{}, nil, err
		}
		deliveries = append(deliveries, d)
	}
	return ev, deliveries, rows.Err()
}
