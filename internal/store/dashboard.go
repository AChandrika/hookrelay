package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrBadCursor        = errors.New("invalid cursor")
	ErrEndpointDisabled = errors.New("endpoint is disabled")
	ErrNotReplayable    = errors.New("only failed deliveries can be replayed")
)

// Cursor is a keyset-pagination position: the (created_at, id) of the last row
// on the previous page. Unlike OFFSET, the next page is found with an index
// seek, so page 500 is as fast as page 1, and rows inserted while someone is
// paging don't shift the results and cause duplicates or skipped rows.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func (c Cursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.CreatedAt.Format(time.RFC3339Nano) + "|" + c.ID))
}

// DecodeCursor returns nil for an empty string (first page).
func DecodeCursor(s string) (*Cursor, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrBadCursor
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, ErrBadCursor
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil, ErrBadCursor
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrBadCursor
	}
	return &Cursor{CreatedAt: t, ID: id}, nil
}

func cursorArgs(c *Cursor) (*time.Time, *string) {
	if c == nil {
		return nil, nil
	}
	return &c.CreatedAt, &c.ID
}

// ---------- Events ----------

type EventSummary struct {
	ID         string    `json:"id"`
	EventType  string    `json:"event_type"`
	CreatedAt  time.Time `json:"created_at"`
	Total      int       `json:"total"`
	Succeeded  int       `json:"succeeded"`
	Failed     int       `json:"failed"`
	InProgress int       `json:"in_progress"`
}

// ListEvents returns one page of events with per-event delivery counts. It
// fetches limit+1 rows: if the extra row exists, there is a next page.
func (s *Store) ListEvents(ctx context.Context, tenantID string, limit int, after *Cursor) ([]EventSummary, *Cursor, error) {
	cAt, cID := cursorArgs(after)
	rows, err := s.pool.Query(ctx, `
		SELECT e.id::text, e.event_type, e.created_at,
		       count(d.id),
		       count(d.id) FILTER (WHERE d.status = 'succeeded'),
		       count(d.id) FILTER (WHERE d.status = 'dead'),
		       count(d.id) FILTER (WHERE d.status IN ('pending', 'in_flight'))
		FROM events e
		LEFT JOIN deliveries d ON d.event_id = e.id
		WHERE e.tenant_id = $1::uuid
		  AND ($2::timestamptz IS NULL OR (e.created_at, e.id) < ($2::timestamptz, $3::uuid))
		GROUP BY e.id
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $4`,
		tenantID, cAt, cID, limit+1,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	out := []EventSummary{}
	for rows.Next() {
		var e EventSummary
		if err := rows.Scan(&e.ID, &e.EventType, &e.CreatedAt, &e.Total, &e.Succeeded, &e.Failed, &e.InProgress); err != nil {
			return nil, nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		return out, &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
	}
	return out, nil, nil
}

// ---------- Deliveries ----------

type DeliveryRow struct {
	ID            string    `json:"id"`
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	EndpointID    string    `json:"endpoint_id"`
	EndpointURL   string    `json:"endpoint_url"`
	Status        string    `json:"status"`
	AttemptCount  int       `json:"attempt_count"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LastError     *string   `json:"last_error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type DeliveryFilter struct {
	Status  *string // one of the delivery_status values, validated by the caller
	EventID *string
	Limit   int
	After   *Cursor
}

const deliveryColumns = `
	d.id::text, d.event_id::text, e.event_type, d.endpoint_id::text, ep.url, d.status::text,
	d.attempt_count, d.next_attempt_at, d.last_error, d.created_at, d.updated_at`

func scanDeliveryRow(row pgx.Row, d *DeliveryRow) error {
	return row.Scan(&d.ID, &d.EventID, &d.EventType, &d.EndpointID, &d.EndpointURL, &d.Status,
		&d.AttemptCount, &d.NextAttemptAt, &d.LastError, &d.CreatedAt, &d.UpdatedAt)
}

func (s *Store) ListDeliveries(ctx context.Context, tenantID string, f DeliveryFilter) ([]DeliveryRow, *Cursor, error) {
	cAt, cID := cursorArgs(f.After)
	rows, err := s.pool.Query(ctx, `
		SELECT `+deliveryColumns+`
		FROM deliveries d
		JOIN endpoints ep ON ep.id = d.endpoint_id
		JOIN events e ON e.id = d.event_id
		WHERE ep.tenant_id = $1::uuid
		  AND ($2::text IS NULL OR d.status = $2::text::delivery_status)
		  AND ($3::uuid IS NULL OR d.event_id = $3::uuid)
		  AND ($4::timestamptz IS NULL OR (d.created_at, d.id) < ($4::timestamptz, $5::uuid))
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT $6`,
		tenantID, f.Status, f.EventID, cAt, cID, f.Limit+1,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()

	out := []DeliveryRow{}
	for rows.Next() {
		var d DeliveryRow
		if err := scanDeliveryRow(rows, &d); err != nil {
			return nil, nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[f.Limit-1]
		return out, &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
	}
	return out, nil, nil
}

type Attempt struct {
	AttemptNumber   int             `json:"attempt_number"`
	StartedAt       time.Time       `json:"started_at"`
	DurationMS      int             `json:"duration_ms"`
	ResponseStatus  *int            `json:"response_status,omitempty"`
	ResponseHeaders json.RawMessage `json:"response_headers,omitempty"`
	ResponseBody    *string         `json:"response_body,omitempty"`
	Error           *string         `json:"error,omitempty"`
}

type DeliveryDetail struct {
	Delivery DeliveryRow `json:"delivery"`
	Event    Event       `json:"event"`
	Attempts []Attempt   `json:"attempts"` // newest first
}

func (s *Store) GetDelivery(ctx context.Context, tenantID, id string) (DeliveryDetail, error) {
	var out DeliveryDetail
	err := scanDeliveryRow(s.pool.QueryRow(ctx, `
		SELECT `+deliveryColumns+`
		FROM deliveries d
		JOIN endpoints ep ON ep.id = d.endpoint_id
		JOIN events e ON e.id = d.event_id
		WHERE d.id = $1::uuid AND ep.tenant_id = $2::uuid`,
		id, tenantID,
	), &out.Delivery)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeliveryDetail{}, ErrNotFound
	}
	if err != nil {
		return DeliveryDetail{}, err
	}

	ev := &out.Event
	err = s.pool.QueryRow(ctx,
		`SELECT id::text, event_type, payload, idempotency_key, created_at FROM events WHERE id = $1::uuid`,
		out.Delivery.EventID,
	).Scan(&ev.ID, &ev.EventType, &ev.Payload, &ev.IdempotencyKey, &ev.CreatedAt)
	if err != nil {
		return DeliveryDetail{}, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT attempt_number, started_at, duration_ms, response_status, response_headers, response_body, error
		FROM delivery_attempts WHERE delivery_id = $1::uuid
		ORDER BY attempt_number DESC`,
		id,
	)
	if err != nil {
		return DeliveryDetail{}, err
	}
	defer rows.Close()
	out.Attempts = []Attempt{}
	for rows.Next() {
		var a Attempt
		var headers []byte
		if err := rows.Scan(&a.AttemptNumber, &a.StartedAt, &a.DurationMS, &a.ResponseStatus, &headers, &a.ResponseBody, &a.Error); err != nil {
			return DeliveryDetail{}, err
		}
		a.ResponseHeaders = headers
		out.Attempts = append(out.Attempts, a)
	}
	return out, rows.Err()
}

// ReplayDelivery moves a failed (dead) delivery back to the queue. Refuses if
// the endpoint is disabled, because the replay would fail again immediately.
func (s *Store) ReplayDelivery(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deliveries d
		SET status = 'pending', retry_base = d.attempt_count, next_attempt_at = now(),
		    last_error = NULL, updated_at = now()
		FROM endpoints ep
		WHERE d.id = $1::uuid AND d.endpoint_id = ep.id AND ep.tenant_id = $2::uuid
		  AND d.status = 'dead' AND ep.enabled`,
		id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("replay delivery: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	// Nothing changed; find out why so the API can give a useful error.
	var status string
	var enabled bool
	err = s.pool.QueryRow(ctx, `
		SELECT d.status::text, ep.enabled
		FROM deliveries d JOIN endpoints ep ON ep.id = d.endpoint_id
		WHERE d.id = $1::uuid AND ep.tenant_id = $2::uuid`,
		id, tenantID,
	).Scan(&status, &enabled)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	case !enabled:
		return ErrEndpointDisabled
	default:
		return ErrNotReplayable
	}
}

// ---------- Endpoints ----------

func (s *Store) EnableEndpoint(ctx context.Context, tenantID, id string) (Endpoint, error) {
	var e Endpoint
	err := s.pool.QueryRow(ctx, `
		UPDATE endpoints SET enabled = true, disabled_reason = NULL
		WHERE id = $1::uuid AND tenant_id = $2::uuid
		RETURNING id::text, url, event_types, enabled, disabled_reason, created_at`,
		id, tenantID,
	).Scan(&e.ID, &e.URL, &e.EventTypes, &e.Enabled, &e.DisabledReason, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}
	return e, err
}

// ReplayFailedForEndpoint requeues every failed delivery for one endpoint,
// typically right after the customer fixes their receiver and re-enables it.
func (s *Store) ReplayFailedForEndpoint(ctx context.Context, tenantID, endpointID string) (int64, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx,
		`SELECT enabled FROM endpoints WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		endpointID, tenantID,
	).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, ErrEndpointDisabled
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE deliveries
		SET status = 'pending', retry_base = attempt_count, next_attempt_at = now(),
		    last_error = NULL, updated_at = now()
		WHERE endpoint_id = $1::uuid AND status = 'dead'`,
		endpointID,
	)
	if err != nil {
		return 0, fmt.Errorf("replay failed deliveries: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ---------- Stats ----------

type HourBucket struct {
	Hour      time.Time `json:"hour"`
	Delivered int       `json:"delivered"`
	Failed    int       `json:"failed"`
}

type Stats struct {
	Hourly   []HourBucket   `json:"hourly"` // last 24 hours, oldest first, no gaps
	ByStatus map[string]int `json:"by_status"`
}

func (s *Store) Stats(ctx context.Context, tenantID string) (Stats, error) {
	out := Stats{
		Hourly:   []HourBucket{},
		ByStatus: map[string]int{"pending": 0, "in_flight": 0, "succeeded": 0, "dead": 0},
	}

	// generate_series produces all 24 hours, so hours with no traffic still
	// appear as zeros instead of gaps in the chart.
	rows, err := s.pool.Query(ctx, `
		WITH hours AS (
			SELECT generate_series(date_trunc('hour', now()) - interval '23 hours',
			                       date_trunc('hour', now()), interval '1 hour') AS hour
		),
		attempts AS (
			SELECT date_trunc('hour', a.started_at) AS hour, a.response_status
			FROM delivery_attempts a
			JOIN deliveries d ON d.id = a.delivery_id
			JOIN endpoints ep ON ep.id = d.endpoint_id
			WHERE ep.tenant_id = $1::uuid AND a.started_at >= date_trunc('hour', now()) - interval '23 hours'
		)
		SELECT h.hour,
		       count(a.hour) FILTER (WHERE a.response_status BETWEEN 200 AND 299),
		       count(a.hour) FILTER (WHERE a.response_status IS NULL OR a.response_status NOT BETWEEN 200 AND 299)
		FROM hours h
		LEFT JOIN attempts a ON a.hour = h.hour
		GROUP BY h.hour
		ORDER BY h.hour`,
		tenantID,
	)
	if err != nil {
		return Stats{}, fmt.Errorf("hourly stats: %w", err)
	}
	for rows.Next() {
		var b HourBucket
		if err := rows.Scan(&b.Hour, &b.Delivered, &b.Failed); err != nil {
			rows.Close()
			return Stats{}, err
		}
		out.Hourly = append(out.Hourly, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Stats{}, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT d.status::text, count(*)
		FROM deliveries d JOIN endpoints ep ON ep.id = d.endpoint_id
		WHERE ep.tenant_id = $1::uuid
		GROUP BY d.status`,
		tenantID,
	)
	if err != nil {
		return Stats{}, fmt.Errorf("status stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return Stats{}, err
		}
		out.ByStatus[status] = n
	}
	return out, rows.Err()
}
