package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testStore connects to the dev database. Locally it skips if Postgres isn't
// running; in CI (where GitHub sets CI=true) a missing database fails the test.
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hookrelay:hookrelay@127.0.0.1:5433/hookrelay?sslmode=disable"
	}
	fail := t.Skipf
	if os.Getenv("CI") != "" {
		fail = t.Fatalf
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("postgres not available: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		fail("postgres not available: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

// newTestTenant creates a tenant with three endpoints and deletes it
// (cascading to everything else) when the test ends.
func newTestTenant(t *testing.T, s *Store) Tenant {
	t.Helper()
	ctx := context.Background()

	hash := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		t.Fatal(err)
	}
	tenant, err := s.CreateTenantWithKey(ctx, "test-"+t.Name(), "hr_test", hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1::uuid`, tenant.ID)
	})

	endpoints := []struct {
		url   string
		types []string
	}{
		{"https://example.com/a", []string{"invoice.paid"}}, // matches
		{"https://example.com/b", nil},                      // subscribes to all -> matches
		{"https://example.com/c", []string{"user.created"}}, // does NOT match
	}
	for _, e := range endpoints {
		if _, err := s.CreateEndpoint(ctx, tenant.ID, e.url, "whsec_test", e.types); err != nil {
			t.Fatal(err)
		}
	}
	return tenant
}

func TestPublishEventFanOutAndIdempotency(t *testing.T) {
	s := testStore(t)
	tenant := newTestTenant(t, s)
	ctx := context.Background()

	key := "order-123"
	payload := json.RawMessage(`{"invoice_id":"inv_1"}`)

	first, err := s.PublishEvent(ctx, tenant.ID, "invoice.paid", payload, &key)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.DeliveriesCreated != 2 {
		t.Fatalf("first publish: replayed=%v deliveries=%d, want false/2", first.Replayed, first.DeliveriesCreated)
	}

	second, err := s.PublishEvent(ctx, tenant.ID, "invoice.paid", payload, &key)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.DeliveriesCreated != 0 || second.Event.ID != first.Event.ID {
		t.Fatalf("second publish: replayed=%v deliveries=%d id=%s, want true/0/%s",
			second.Replayed, second.DeliveriesCreated, second.Event.ID, first.Event.ID)
	}

	_, deliveries, err := s.GetEvent(ctx, tenant.ID, first.Event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("got %d deliveries, want 2", len(deliveries))
	}
}

// Ten requests with the same idempotency key at the same moment must produce
// exactly one event. This is the case a naive "SELECT, then INSERT" gets wrong.
func TestPublishEventConcurrentIdempotency(t *testing.T) {
	s := testStore(t)
	tenant := newTestTenant(t, s)
	key := "concurrent-key"

	const n = 10
	var wg sync.WaitGroup
	results := make([]PublishResult, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.PublishEvent(context.Background(), tenant.ID, "invoice.paid",
				json.RawMessage(`{"n":1}`), &key)
		}(i)
	}
	wg.Wait()

	created := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		if !results[i].Replayed {
			created++
		}
		if results[i].Event.ID != results[0].Event.ID {
			t.Fatalf("request %d got a different event id", i)
		}
	}
	if created != 1 {
		t.Fatalf("%d requests created an event, want exactly 1", created)
	}
}

func TestGetEventIsTenantScoped(t *testing.T) {
	s := testStore(t)
	owner := newTestTenant(t, s)
	other := newTestTenant(t, s)

	res, err := s.PublishEvent(context.Background(), owner.ID, "invoice.paid", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetEvent(context.Background(), other.ID, res.Event.ID); err != ErrNotFound {
		t.Fatalf("another tenant read the event: err=%v, want ErrNotFound", err)
	}
}
