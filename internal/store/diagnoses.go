package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNothingToDiagnose = errors.New("delivery has no attempts yet")

type Diagnosis struct {
	ID               string          `json:"id"`
	DeliveryID       string          `json:"delivery_id"`
	Status           string          `json:"status"` // pending, running, done, failed
	Model            *string         `json:"model,omitempty"`
	PromptVersion    *string         `json:"prompt_version,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	Note             *string         `json:"note,omitempty"`
	Cached           bool            `json:"cached"`
	DurationMS       *int            `json:"duration_ms,omitempty"`
	PromptTokens     *int            `json:"prompt_tokens,omitempty"`
	CompletionTokens *int            `json:"completion_tokens,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

const diagnosisColumns = `
	g.id::text, g.delivery_id::text, g.status::text, g.model, g.prompt_version, g.result,
	g.note, g.cached, g.duration_ms, g.prompt_tokens, g.completion_tokens, g.created_at, g.updated_at`

func scanDiagnosis(row pgx.Row) (Diagnosis, error) {
	var g Diagnosis
	var result []byte
	err := row.Scan(&g.ID, &g.DeliveryID, &g.Status, &g.Model, &g.PromptVersion, &result,
		&g.Note, &g.Cached, &g.DurationMS, &g.PromptTokens, &g.CompletionTokens, &g.CreatedAt, &g.UpdatedAt)
	g.Result = result
	return g, err
}

// EnqueueDiagnosis queues a diagnosis for a tenant's delivery. If one is
// already queued or running, it returns that one (created = false).
func (s *Store) EnqueueDiagnosis(ctx context.Context, tenantID, deliveryID string) (Diagnosis, bool, error) {
	var endpointID string
	var attempts int
	err := s.pool.QueryRow(ctx, `
		SELECT d.endpoint_id::text,
		       (SELECT count(*) FROM delivery_attempts a WHERE a.delivery_id = d.id)
		FROM deliveries d JOIN endpoints ep ON ep.id = d.endpoint_id
		WHERE d.id = $1::uuid AND ep.tenant_id = $2::uuid`,
		deliveryID, tenantID,
	).Scan(&endpointID, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Diagnosis{}, false, ErrNotFound
	}
	if err != nil {
		return Diagnosis{}, false, err
	}
	if attempts == 0 {
		return Diagnosis{}, false, ErrNothingToDiagnose
	}
	return s.enqueue(ctx, deliveryID, endpointID)
}

// EnqueueDiagnosisForDelivery is the worker's version: no tenant check, since
// the worker only calls it for deliveries it just processed.
func (s *Store) EnqueueDiagnosisForDelivery(ctx context.Context, deliveryID, endpointID string) error {
	_, _, err := s.enqueue(ctx, deliveryID, endpointID)
	return err
}

func (s *Store) enqueue(ctx context.Context, deliveryID, endpointID string) (Diagnosis, bool, error) {
	// ON CONFLICT targets the partial unique index: at most one active
	// diagnosis per delivery, enforced by the database, not by a check-then-insert.
	g, err := scanDiagnosis(s.pool.QueryRow(ctx, `
		INSERT INTO diagnoses AS g (delivery_id, endpoint_id)
		VALUES ($1::uuid, $2::uuid)
		ON CONFLICT (delivery_id) WHERE status IN ('pending', 'running') DO NOTHING
		RETURNING `+diagnosisColumns,
		deliveryID, endpointID,
	))
	if err == nil {
		return g, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Diagnosis{}, false, fmt.Errorf("enqueue diagnosis: %w", err)
	}
	g, err = scanDiagnosis(s.pool.QueryRow(ctx, `
		SELECT `+diagnosisColumns+` FROM diagnoses g
		WHERE g.delivery_id = $1::uuid AND g.status IN ('pending', 'running')`,
		deliveryID,
	))
	return g, false, err
}

// LatestDiagnosis returns the most recent diagnosis for a tenant's delivery.
func (s *Store) LatestDiagnosis(ctx context.Context, tenantID, deliveryID string) (Diagnosis, error) {
	g, err := scanDiagnosis(s.pool.QueryRow(ctx, `
		SELECT `+diagnosisColumns+`
		FROM diagnoses g JOIN endpoints ep ON ep.id = g.endpoint_id
		WHERE g.delivery_id = $1::uuid AND ep.tenant_id = $2::uuid
		ORDER BY g.created_at DESC LIMIT 1`,
		deliveryID, tenantID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Diagnosis{}, ErrNotFound
	}
	return g, err
}

// ---------- Worker side ----------

type DiagnosisJob struct {
	ID         string
	DeliveryID string
	LeaseUntil time.Time // fencing token, same idea as deliveries
}

// ClaimDiagnosis takes the oldest queued diagnosis, or returns nil if none.
func (s *Store) ClaimDiagnosis(ctx context.Context, lease time.Duration) (*DiagnosisJob, error) {
	var j DiagnosisJob
	err := s.pool.QueryRow(ctx, `
		UPDATE diagnoses
		SET status = 'running', locked_until = now() + make_interval(secs => $1::double precision), updated_at = now()
		WHERE id = (
			SELECT id FROM diagnoses WHERE status = 'pending'
			ORDER BY created_at LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id::text, delivery_id::text, locked_until`,
		lease.Seconds(),
	).Scan(&j.ID, &j.DeliveryID, &j.LeaseUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim diagnosis: %w", err)
	}
	return &j, nil
}

func (s *Store) ReapDiagnoses(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE diagnoses SET status = 'pending', locked_until = NULL, updated_at = now()
		WHERE status = 'running' AND locked_until < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DiagnosisContext is what the diagnoser reads about a delivery.
type DiagnosisContext struct {
	EndpointID  string
	EndpointURL string
	EventType   string
	Attempts    []Attempt // newest first
}

func (s *Store) LoadDiagnosisContext(ctx context.Context, deliveryID string, maxAttempts int) (DiagnosisContext, error) {
	var dc DiagnosisContext
	err := s.pool.QueryRow(ctx, `
		SELECT ep.id::text, ep.url, e.event_type
		FROM deliveries d
		JOIN endpoints ep ON ep.id = d.endpoint_id
		JOIN events e ON e.id = d.event_id
		WHERE d.id = $1::uuid`,
		deliveryID,
	).Scan(&dc.EndpointID, &dc.EndpointURL, &dc.EventType)
	if errors.Is(err, pgx.ErrNoRows) {
		return dc, ErrNotFound
	}
	if err != nil {
		return dc, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT attempt_number, started_at, duration_ms, response_status, response_headers, response_body, error
		FROM delivery_attempts WHERE delivery_id = $1::uuid
		ORDER BY attempt_number DESC LIMIT $2`,
		deliveryID, maxAttempts,
	)
	if err != nil {
		return dc, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Attempt
		var headers []byte
		if err := rows.Scan(&a.AttemptNumber, &a.StartedAt, &a.DurationMS, &a.ResponseStatus, &headers, &a.ResponseBody, &a.Error); err != nil {
			return dc, err
		}
		a.ResponseHeaders = headers
		dc.Attempts = append(dc.Attempts, a)
	}
	return dc, rows.Err()
}

// FindCachedDiagnosis returns a recent result for the same failure, model and
// prompt version. The fingerprint includes the endpoint ID, so a cached result
// is never shared between tenants.
func (s *Store) FindCachedDiagnosis(ctx context.Context, fingerprint, model, promptVersion string, since time.Time) (json.RawMessage, bool, error) {
	var result []byte
	err := s.pool.QueryRow(ctx, `
		SELECT result FROM diagnoses
		WHERE status = 'done' AND fingerprint = $1 AND model = $2 AND prompt_version = $3
		  AND created_at >= $4 AND result IS NOT NULL
		ORDER BY created_at DESC LIMIT 1`,
		fingerprint, model, promptVersion, since,
	).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
}

type DiagnosisOutcome struct {
	Fingerprint      string
	Model            string
	PromptVersion    string
	Result           json.RawMessage
	Note             *string
	Cached           bool
	DurationMS       int
	PromptTokens     int
	CompletionTokens int
}

func (s *Store) CompleteDiagnosis(ctx context.Context, j DiagnosisJob, o DiagnosisOutcome) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE diagnoses
		SET status = 'done', fingerprint = $2, model = $3, prompt_version = $4, result = $5,
		    note = $6, cached = $7, duration_ms = $8, prompt_tokens = $9, completion_tokens = $10,
		    locked_until = NULL, updated_at = now()
		WHERE id = $1::uuid AND status = 'running' AND locked_until = $11`,
		j.ID, o.Fingerprint, o.Model, o.PromptVersion, o.Result, o.Note, o.Cached,
		o.DurationMS, o.PromptTokens, o.CompletionTokens, j.LeaseUntil,
	)
	if err != nil {
		return fmt.Errorf("complete diagnosis: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) FailDiagnosis(ctx context.Context, j DiagnosisJob, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE diagnoses SET status = 'failed', note = $2, locked_until = NULL, updated_at = now()
		WHERE id = $1::uuid AND status = 'running' AND locked_until = $3`,
		j.ID, reason, j.LeaseUntil,
	)
	return err
}
