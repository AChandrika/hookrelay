package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestListEventsPagination(t *testing.T) {
	s := testStore(t)
	tenant := newTestTenant(t, s)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := s.PublishEvent(ctx, tenant.ID, "invoice.paid", json.RawMessage(`{}`), nil); err != nil {
			t.Fatal(err)
		}
	}

	page1, next, err := s.ListEvents(ctx, tenant.ID, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || next == nil {
		t.Fatalf("page 1: %d events, next=%v; want 2 and a cursor", len(page1), next)
	}
	if page1[0].Total != 2 {
		t.Fatalf("event should have 2 deliveries, got %d", page1[0].Total)
	}

	// Round-trip the cursor through its string form, like the browser does.
	cur, err := DecodeCursor(next.Encode())
	if err != nil {
		t.Fatal(err)
	}
	page2, next2, err := s.ListEvents(ctx, tenant.ID, 2, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || next2 != nil {
		t.Fatalf("page 2: %d events, next=%v; want 1 and no cursor", len(page2), next2)
	}
	for _, e := range page1 {
		if e.ID == page2[0].ID {
			t.Fatal("event appeared on both pages")
		}
	}
}

func TestReplayDelivery(t *testing.T) {
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

	// A pending delivery can't be replayed.
	if err := s.ReplayDelivery(ctx, tenant.ID, id); !errors.Is(err, ErrNotReplayable) {
		t.Fatalf("replay pending: got %v, want ErrNotReplayable", err)
	}

	// Simulate 8 failed attempts.
	if _, err := s.pool.Exec(ctx, `UPDATE deliveries SET status = 'dead', attempt_count = 8 WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplayDelivery(ctx, tenant.ID, id); err != nil {
		t.Fatalf("replay dead: %v", err)
	}
	var status string
	var attempts, base int
	if err := s.pool.QueryRow(ctx, `SELECT status::text, attempt_count, retry_base FROM deliveries WHERE id = $1::uuid`, id).
		Scan(&status, &attempts, &base); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 8 || base != 8 {
		t.Fatalf("after replay: status=%s attempts=%d retry_base=%d, want pending/8/8", status, attempts, base)
	}

	// Another tenant can't replay it.
	other := newTestTenant(t, s)
	if err := s.ReplayDelivery(ctx, other.ID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant replay: got %v, want ErrNotFound", err)
	}

	// A disabled endpoint blocks replay.
	if _, err := s.pool.Exec(ctx, `UPDATE deliveries SET status = 'dead' WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE endpoints SET enabled = false WHERE id = $1::uuid`, ds[0].EndpointID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplayDelivery(ctx, tenant.ID, id); !errors.Is(err, ErrEndpointDisabled) {
		t.Fatalf("replay with disabled endpoint: got %v, want ErrEndpointDisabled", err)
	}
}

func TestStatsHasTwentyFourHours(t *testing.T) {
	s := testStore(t)
	tenant := newTestTenant(t, s)
	st, err := s.Stats(context.Background(), tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Hourly) != 24 {
		t.Fatalf("got %d hourly buckets, want 24", len(st.Hourly))
	}
}
