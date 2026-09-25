package worker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hookrelay/internal/signing"
	"hookrelay/internal/store"
)

// End to end against real Postgres and a real HTTP receiver: the first attempt
// gets a 500, the retry succeeds, and every request carries a valid signature.
//
// Stop any running worker before running this, or it may claim these deliveries.
func TestRetryThenSucceed(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hookrelay:hookrelay@127.0.0.1:5433/hookrelay?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = pool.Ping(ctx)
		cancel()
	}
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("postgres not available: %v", err)
		}
		t.Skipf("postgres not available: %v", err)
	}
	defer pool.Close()
	s := store.New(pool)
	ctx := context.Background()

	var secret atomic.Value
	var calls atomic.Int32
	var badSig atomic.Bool
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := signing.Verify(secret.Load().(string), r.Header, body, time.Now()); err != nil {
			badSig.Store(true)
		}
		if calls.Add(1) == 1 {
			http.Error(w, "temporary failure", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	hash := make([]byte, 32)
	_, _ = rand.Read(hash)
	tenant, err := s.CreateTenantWithKey(ctx, "worker-test", "hr_test", hash)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1::uuid`, tenant.ID)

	ep, err := s.CreateEndpoint(ctx, tenant.ID, receiver.URL, "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", nil)
	if err != nil {
		t.Fatal(err)
	}
	secret.Store(ep.Secret)

	res, err := s.PublishEvent(ctx, tenant.ID, "invoice.paid", json.RawMessage(`{"amount":42}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.AllowPrivateNetworks = true // httptest listens on 127.0.0.1
	cfg.BatchSize = 1000           // make sure our delivery is claimed even if the dev DB has others
	w := New(s, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Attempt 1: receiver returns 500 -> back to pending with a future retry time.
	if _, err := w.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_, ds, err := s.GetEvent(ctx, tenant.ID, res.Event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ds[0].Status != store.StatusPending || ds[0].AttemptCount != 1 {
		t.Fatalf("after attempt 1: status=%s attempts=%d, want pending/1", ds[0].Status, ds[0].AttemptCount)
	}

	// Skip the backoff wait, then attempt 2 succeeds.
	if _, err := pool.Exec(ctx, `UPDATE deliveries SET next_attempt_at = now() WHERE id = $1::uuid`, ds[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_, ds, _ = s.GetEvent(ctx, tenant.ID, res.Event.ID)
	if ds[0].Status != store.StatusSucceeded || ds[0].AttemptCount != 2 {
		t.Fatalf("after attempt 2: status=%s attempts=%d, want succeeded/2", ds[0].Status, ds[0].AttemptCount)
	}
	if badSig.Load() {
		t.Fatal("receiver saw an invalid signature")
	}

	var attempts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_attempts WHERE delivery_id = $1::uuid`, ds[0].ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("recorded %d attempts, want 2", attempts)
	}
}
