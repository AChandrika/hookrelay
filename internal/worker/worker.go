// Package worker claims pending deliveries, sends them, and records the result.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"hookrelay/internal/signing"
	"hookrelay/internal/store"
)

type Config struct {
	Concurrency          int           // max deliveries in flight at once
	BatchSize            int           // max rows claimed per query
	PollInterval         time.Duration // wait when the queue is empty
	Lease                time.Duration // how long a claim lasts before the reaper takes it back
	RequestTimeout       time.Duration // per HTTP attempt; must be well under Lease
	ReapInterval         time.Duration
	MaxAttempts          int
	Schedule             []time.Duration
	AllowPrivateNetworks bool // dev only: lets you deliver to 127.0.0.1
}

func DefaultConfig() Config {
	return Config{
		Concurrency:    10,
		BatchSize:      20,
		PollInterval:   time.Second,
		Lease:          60 * time.Second,
		RequestTimeout: 15 * time.Second,
		ReapInterval:   15 * time.Second,
		MaxAttempts:    8,
		Schedule:       DefaultSchedule,
	}
}

type Worker struct {
	store  *store.Store
	cfg    Config
	client *http.Client
	log    *slog.Logger
	now    func() time.Time
}

func New(s *store.Store, cfg Config, log *slog.Logger) *Worker {
	return &Worker{
		store:  s,
		cfg:    cfg,
		client: newHTTPClient(cfg.AllowPrivateNetworks, cfg.RequestTimeout),
		log:    log,
		now:    time.Now,
	}
}

// Run loops until ctx is cancelled, then waits for in-flight deliveries to
// finish. It only claims as many rows as it has free slots, so claimed rows
// never sit waiting while their lease ticks down.
func (w *Worker) Run(ctx context.Context) error {
	go w.reapLoop(ctx)

	var wg sync.WaitGroup
	slots := make(chan struct{}, w.cfg.Concurrency)

	for ctx.Err() == nil {
		free := cap(slots) - len(slots)
		if free == 0 {
			sleep(ctx, 50*time.Millisecond)
			continue
		}
		batch, err := w.store.ClaimDeliveries(ctx, min(free, w.cfg.BatchSize), w.cfg.Lease)
		if err != nil {
			if ctx.Err() == nil {
				w.log.Error("claim failed", "err", err)
				sleep(ctx, w.cfg.PollInterval)
			}
			continue
		}
		if len(batch) == 0 {
			sleep(ctx, w.cfg.PollInterval)
			continue
		}
		for _, d := range batch {
			slots <- struct{}{}
			wg.Add(1)
			go func(d store.ClaimedDelivery) {
				defer wg.Done()
				defer func() { <-slots }()
				w.process(d)
			}(d)
		}
	}

	w.log.Info("shutting down, waiting for in-flight deliveries")
	wg.Wait()
	return nil
}

// ProcessOnce claims one batch and processes it synchronously. Used by tests.
func (w *Worker) ProcessOnce(ctx context.Context) (int, error) {
	batch, err := w.store.ClaimDeliveries(ctx, w.cfg.BatchSize, w.cfg.Lease)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	for _, d := range batch {
		wg.Add(1)
		go func(d store.ClaimedDelivery) {
			defer wg.Done()
			w.process(d)
		}(d)
	}
	wg.Wait()
	return len(batch), nil
}

func (w *Worker) reapLoop(ctx context.Context) {
	t := time.NewTicker(w.cfg.ReapInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := w.store.ReapExpired(ctx)
			if err != nil && ctx.Err() == nil {
				w.log.Error("reaper failed", "err", err)
			} else if n > 0 {
				w.log.Warn("requeued deliveries with expired leases", "count", n)
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// process handles one delivery. It deliberately uses a fresh context, not the
// Run context: on shutdown we want in-flight requests to finish, not be cut off.
func (w *Worker) process(d store.ClaimedDelivery) {
	ctx, cancel := context.WithTimeout(context.Background(), w.cfg.RequestTimeout+10*time.Second)
	defer cancel()
	log := w.log.With("delivery_id", d.ID, "event_id", d.EventID, "url", d.EndpointURL, "attempt", d.AttemptCount+1)

	var attempt *store.AttemptRecord
	var outcome store.Outcome
	if !d.EndpointEnabled {
		msg := "endpoint is disabled"
		outcome = store.Outcome{Status: store.StatusDead, LastError: &msg}
	} else {
		a, res := w.send(ctx, d)
		attempt = &a
		outcome = w.decide(d, res)
	}

	err := w.store.FinishDelivery(ctx, d, attempt, outcome)
	switch {
	case errors.Is(err, store.ErrLeaseLost):
		log.Warn("lease lost; another worker owns this delivery now, discarding result")
	case err != nil:
		// The row stays in_flight; the reaper will requeue it after the lease expires.
		log.Error("could not record result", "err", err)
	default:
		log.Info("delivery attempt finished", "outcome", outcome.Status,
			"http_status", statusOf(attempt), "last_error", deref(outcome.LastError))
	}
}

type sendResult struct {
	statusCode int // 0 when there was no response
	retryAfter time.Duration
	err        error
}

// envelope is the JSON body receivers get.
type envelope struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

func (w *Worker) send(ctx context.Context, d store.ClaimedDelivery) (store.AttemptRecord, sendResult) {
	start := w.now()
	a := store.AttemptRecord{StartedAt: start}
	fail := func(err error) (store.AttemptRecord, sendResult) {
		msg := err.Error()
		a.Error = &msg
		a.Duration = w.now().Sub(start)
		return a, sendResult{err: err}
	}

	body, err := json.Marshal(envelope{ID: d.EventID, Type: d.EventType, CreatedAt: d.EventCreatedAt, Data: d.Payload})
	if err != nil {
		return fail(fmt.Errorf("encode body: %w", err))
	}
	// webhook-id is the EVENT id, identical on every retry, so receivers can
	// dedupe. The timestamp and signature are fresh for each attempt.
	sig, err := signing.Sign(d.EndpointSecret, d.EventID, start, body)
	if err != nil {
		return fail(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return fail(fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hookrelay/0.1")
	req.Header.Set("webhook-id", d.EventID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(start.Unix(), 10))
	req.Header.Set("webhook-signature", sig)

	resp, err := w.client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	a.Duration = w.now().Sub(start)
	code := resp.StatusCode
	a.ResponseStatus = &code
	// Postgres text columns reject NUL bytes and invalid UTF-8, and a receiver
	// can send anything, so clean it before storing.
	bodyText := strings.ReplaceAll(strings.ToValidUTF8(string(raw), "\uFFFD"), "\x00", "")
	a.ResponseBody = &bodyText
	a.ResponseHeaders = pickHeaders(resp.Header)

	return a, sendResult{statusCode: code, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), w.now())}
}

// decide turns an attempt result into the next state. Kept free of I/O so it
// can be unit-tested with a plain table of cases.
func (w *Worker) decide(d store.ClaimedDelivery, r sendResult) store.Outcome {
	if r.err == nil && r.statusCode >= 200 && r.statusCode < 300 {
		return store.Outcome{Status: store.StatusSucceeded}
	}
	if r.statusCode == http.StatusGone {
		// 410 means "this URL is gone for good, stop sending". Retrying would waste
		// work, so we disable the endpoint. Its other queued deliveries go to the
		// DLQ when claimed, and can be replayed if the customer fixes the URL.
		msg := "receiver returned 410 Gone; endpoint disabled"
		return store.Outcome{Status: store.StatusDead, LastError: &msg, DisableEndpoint: "receiver returned 410 Gone"}
	}

	// Everything else is retried: timeouts, connection errors, 5xx, 429, and
	// other 4xx too, since a 401 or 404 is often a receiver misconfiguration
	// that gets fixed within hours.
	lastErr := describe(r)
	attemptNum := d.AttemptCount + 1
	if attemptNum >= w.cfg.MaxAttempts {
		msg := fmt.Sprintf("gave up after %d attempts: %s", attemptNum, lastErr)
		return store.Outcome{Status: store.StatusDead, LastError: &msg}
	}
	delay := NextDelay(w.cfg.Schedule, attemptNum)
	if r.retryAfter > delay {
		delay = r.retryAfter // the receiver asked us to wait longer; respect it
	}
	next := w.now().Add(delay)
	return store.Outcome{Status: store.StatusPending, NextAttemptAt: &next, LastError: &lastErr}
}

func describe(r sendResult) string {
	if r.err != nil {
		return r.err.Error()
	}
	return fmt.Sprintf("receiver returned HTTP %d", r.statusCode)
}

func pickHeaders(h http.Header) json.RawMessage {
	keep := map[string]string{}
	for _, k := range []string{"Content-Type", "Content-Length", "Retry-After", "Server"} {
		if v := h.Get(k); v != "" {
			keep[k] = v
		}
	}
	if len(keep) == 0 {
		return nil
	}
	b, _ := json.Marshal(keep)
	return b
}

func statusOf(a *store.AttemptRecord) int {
	if a == nil || a.ResponseStatus == nil {
		return 0
	}
	return *a.ResponseStatus
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
