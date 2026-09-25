package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDiagnosisQueue(t *testing.T) {
	s := testStore(t)
	tenant := newTestTenant(t, s)
	ctx := context.Background()

	res, err := s.PublishEvent(ctx, tenant.ID, "invoice.paid", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, ds, err := s.GetEvent(ctx, tenant.ID, res.Event.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := ds[0].ID

	if _, _, err := s.EnqueueDiagnosis(ctx, tenant.ID, id); !errors.Is(err, ErrNothingToDiagnose) {
		t.Fatalf("no attempts yet: got %v, want ErrNothingToDiagnose", err)
	}

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO delivery_attempts (delivery_id, attempt_number, started_at, duration_ms, response_status)
		VALUES ($1::uuid, 1, now(), 12, 500)`, id); err != nil {
		t.Fatal(err)
	}

	first, created, err := s.EnqueueDiagnosis(ctx, tenant.ID, id)
	if err != nil || !created {
		t.Fatalf("first enqueue: created=%v err=%v", created, err)
	}
	second, created, err := s.EnqueueDiagnosis(ctx, tenant.ID, id)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second enqueue should return the active one: created=%v err=%v", created, err)
	}

	other := newTestTenant(t, s)
	if _, _, err := s.EnqueueDiagnosis(ctx, other.ID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant enqueue: got %v, want ErrNotFound", err)
	}

	// Claim until we get ours (the dev database may hold other queued jobs).
	var job *DiagnosisJob
	for i := 0; i < 50; i++ {
		job, err = s.ClaimDiagnosis(ctx, time.Minute)
		if err != nil || job == nil || job.ID == first.ID {
			break
		}
	}
	if err != nil || job == nil || job.ID != first.ID {
		t.Fatalf("claim: job=%v err=%v", job, err)
	}

	result := json.RawMessage(`{"category":"receiver_error"}`)
	if err := s.CompleteDiagnosis(ctx, *job, DiagnosisOutcome{Fingerprint: "fp-" + first.ID, Model: "test", PromptVersion: "v1", Result: result}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDiagnosis(ctx, *job, DiagnosisOutcome{Fingerprint: "x", Model: "test", PromptVersion: "v1", Result: result}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("completing twice: got %v, want ErrLeaseLost", err)
	}

	got, ok, err := s.FindCachedDiagnosis(ctx, "fp-"+first.ID, "test", "v1", time.Now().Add(-time.Hour))
	if err != nil || !ok || string(got) == "" {
		t.Fatalf("cache lookup: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := s.FindCachedDiagnosis(ctx, "fp-"+first.ID, "test", "v2", time.Now().Add(-time.Hour)); ok {
		t.Fatal("a different prompt version must not hit the cache")
	}

	latest, err := s.LatestDiagnosis(ctx, tenant.ID, id)
	if err != nil || latest.Status != "done" {
		t.Fatalf("latest: status=%s err=%v", latest.Status, err)
	}
}
