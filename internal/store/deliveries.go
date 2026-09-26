package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrLeaseLost means this worker's claim on a delivery expired and the row was
// given to someone else (or requeued). The worker must discard its result.
var ErrLeaseLost = errors.New("delivery lease lost")

const (
	StatusPending   = "pending"
	StatusSucceeded = "succeeded"
	StatusDead      = "dead"
)

// ClaimedDelivery is everything a worker needs to send one webhook.
type ClaimedDelivery struct {
	ID           string
	AttemptCount int       // attempts made before this one
	RetryBase    int       // attempt_count when last replayed; retries are counted from here
	LeaseUntil   time.Time // doubles as a fencing token, see FinishDelivery

	EventID        string
	EventType      string
	Payload        json.RawMessage
	EventCreatedAt time.Time

	EndpointID      string
	EndpointURL     string
	EndpointSecret  string
	EndpointEnabled bool
}

// ClaimDeliveries atomically moves up to `limit` due deliveries to in_flight and
// returns them with their event and endpoint.
//
// FOR UPDATE SKIP LOCKED is what makes many workers safe: rows another worker is
// claiming right now are skipped instead of waited on, so two workers never get
// the same row and never block each other.
func (s *Store) ClaimDeliveries(ctx context.Context, limit int, lease time.Duration) ([]ClaimedDelivery, error) {
	rows, err := s.pool.Query(ctx, `
		WITH claimed AS (
			UPDATE deliveries d
			SET status = 'in_flight',
			    locked_until = now() + make_interval(secs => $2::double precision),
			    updated_at = now()
			WHERE d.id IN (
				SELECT id FROM deliveries
				WHERE status = 'pending' AND next_attempt_at <= now()
				ORDER BY next_attempt_at
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING d.id, d.event_id, d.endpoint_id, d.attempt_count, d.retry_base, d.locked_until
		)
		SELECT c.id::text, c.attempt_count, c.retry_base, c.locked_until,
		       e.id::text, e.event_type, e.payload, e.created_at,
		       ep.id::text, ep.url, ep.secret, ep.enabled
		FROM claimed c
		JOIN events e ON e.id = c.event_id
		JOIN endpoints ep ON ep.id = c.endpoint_id`,
		limit, lease.Seconds(),
	)
	if err != nil {
		return nil, fmt.Errorf("claim deliveries: %w", err)
	}
	defer rows.Close()

	var out []ClaimedDelivery
	for rows.Next() {
		var d ClaimedDelivery
		if err := rows.Scan(&d.ID, &d.AttemptCount, &d.RetryBase, &d.LeaseUntil,
			&d.EventID, &d.EventType, &d.Payload, &d.EventCreatedAt,
			&d.EndpointID, &d.EndpointURL, &d.EndpointSecret, &d.EndpointEnabled); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AttemptRecord is one HTTP attempt, stored for the dashboard and AI diagnosis.
type AttemptRecord struct {
	StartedAt       time.Time
	Duration        time.Duration
	ResponseStatus  *int            // nil on timeout / connection error
	ResponseHeaders json.RawMessage // selected headers as a JSON object, or nil
	ResponseBody    *string         // truncated
	Error           *string
}

// Outcome is what the worker decided after an attempt.
type Outcome struct {
	Status          string     // StatusSucceeded, StatusPending (retry) or StatusDead
	NextAttemptAt   *time.Time // set when retrying
	LastError       *string
	DisableEndpoint string // non-empty: disable the endpoint with this reason
}

// FinishDelivery records an attempt and the outcome in one transaction.
//
// The WHERE clause checks locked_until against the value from our claim. That is
// a fencing token: if our lease expired, the reaper requeued the row and another
// worker may have claimed it with a new locked_until. Our stale update then
// matches zero rows, and we return ErrLeaseLost instead of overwriting their work.
//
// attempt may be nil when nothing was sent (e.g. the endpoint was disabled).
func (s *Store) FinishDelivery(ctx context.Context, d ClaimedDelivery, attempt *AttemptRecord, o Outcome) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	increment := 0
	if attempt != nil {
		increment = 1
	}
	tag, err := tx.Exec(ctx, `
		UPDATE deliveries
		SET status = $2::text::delivery_status,
		    attempt_count = attempt_count + $3,
		    next_attempt_at = COALESCE($4::timestamptz, next_attempt_at),
		    last_error = $5,
		    locked_until = NULL,
		    updated_at = now()
		WHERE id = $1::uuid AND status = 'in_flight' AND locked_until = $6`,
		d.ID, o.Status, increment, o.NextAttemptAt, o.LastError, d.LeaseUntil,
	)
	if err != nil {
		return fmt.Errorf("update delivery: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}

	if attempt != nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO delivery_attempts
				(delivery_id, attempt_number, started_at, duration_ms,
				 response_status, response_headers, response_body, error)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)`,
			d.ID, d.AttemptCount+1, attempt.StartedAt, int(attempt.Duration.Milliseconds()),
			attempt.ResponseStatus, attempt.ResponseHeaders, attempt.ResponseBody, attempt.Error,
		)
		if err != nil {
			return fmt.Errorf("insert attempt: %w", err)
		}
	}

	if o.DisableEndpoint != "" {
		_, err = tx.Exec(ctx,
			`UPDATE endpoints SET enabled = false, disabled_reason = $2 WHERE id = $1::uuid`,
			d.EndpointID, o.DisableEndpoint,
		)
		if err != nil {
			return fmt.Errorf("disable endpoint: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ReapExpired puts deliveries whose lease ran out back in the queue. This is
// what makes a crashed worker harmless, and also why delivery is at-least-once:
// the crashed worker may already have sent the request before dying.
func (s *Store) ReapExpired(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deliveries
		SET status = 'pending', locked_until = NULL, updated_at = now()
		WHERE status = 'in_flight' AND locked_until < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type QueueStats struct {
	Due, Scheduled, InFlight, Dead int
}

// QueueStats counts deliveries that aren't finished. Succeeded rows are
// excluded up front, since they're the vast majority over time.
func (s *Store) QueueStats(ctx context.Context) (QueueStats, error) {
	var q QueueStats
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'pending' AND next_attempt_at <= now()),
		       count(*) FILTER (WHERE status = 'pending' AND next_attempt_at > now()),
		       count(*) FILTER (WHERE status = 'in_flight'),
		       count(*) FILTER (WHERE status = 'dead')
		FROM deliveries WHERE status <> 'succeeded'`,
	).Scan(&q.Due, &q.Scheduled, &q.InFlight, &q.Dead)
	return q, err
}
