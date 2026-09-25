package worker

import (
	"net"
	"net/http"
	"testing"
	"time"

	"hookrelay/internal/store"
)

func TestNextDelayJitterBounds(t *testing.T) {
	for attempt := 1; attempt <= 10; attempt++ {
		i := min(attempt-1, len(DefaultSchedule)-1)
		base := DefaultSchedule[i]
		for n := 0; n < 200; n++ {
			d := NextDelay(DefaultSchedule, attempt)
			if d < time.Duration(float64(base)*0.8) || d > time.Duration(float64(base)*1.2) {
				t.Fatalf("attempt %d: delay %v outside ±20%% of %v", attempt, d, base)
			}
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"120":                           2 * time.Minute,
		"-5":                            0,
		"999999":                        time.Hour, // capped
		"Fri, 25 Sep 2026 12:00:30 GMT": 30 * time.Second,
		"garbage":                       0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "100.64.0.1", "::1", "fe80::1", "fc00::1"}
	allowed := []string{"93.184.216.34", "8.8.8.8", "2606:4700:4700::1111"}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range allowed {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestDecide(t *testing.T) {
	w := &Worker{cfg: DefaultConfig(), now: func() time.Time { return time.Unix(1_700_000_000, 0) }}

	tests := []struct {
		name         string
		prevAttempts int
		retryBase    int
		res          sendResult
		wantStatus   string
		wantDisable  bool
	}{
		{"2xx succeeds", 0, 0, sendResult{statusCode: 204}, store.StatusSucceeded, false},
		{"500 retries", 0, 0, sendResult{statusCode: 500}, store.StatusPending, false},
		{"401 retries", 2, 0, sendResult{statusCode: 401}, store.StatusPending, false},
		{"timeout retries", 0, 0, sendResult{err: errTest}, store.StatusPending, false},
		{"410 disables endpoint", 0, 0, sendResult{statusCode: 410}, store.StatusDead, true},
		{"last attempt goes to DLQ", 7, 0, sendResult{statusCode: 500}, store.StatusDead, false},
		{"3xx is not success", 0, 0, sendResult{statusCode: 302}, store.StatusPending, false},
		{"replayed delivery gets fresh retries", 8, 8, sendResult{statusCode: 500}, store.StatusPending, false},
		{"replayed delivery can die again", 15, 8, sendResult{statusCode: 500}, store.StatusDead, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := w.decide(store.ClaimedDelivery{AttemptCount: tc.prevAttempts, RetryBase: tc.retryBase}, tc.res)
			if o.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", o.Status, tc.wantStatus)
			}
			if (o.DisableEndpoint != "") != tc.wantDisable {
				t.Fatalf("disable = %q, want disable=%v", o.DisableEndpoint, tc.wantDisable)
			}
			if o.Status == store.StatusPending && o.NextAttemptAt == nil {
				t.Fatal("retry without next_attempt_at")
			}
		})
	}
}

func TestDecideRespectsRetryAfter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	w := &Worker{cfg: DefaultConfig(), now: func() time.Time { return now }}
	o := w.decide(store.ClaimedDelivery{}, sendResult{statusCode: http.StatusTooManyRequests, retryAfter: 10 * time.Minute})
	if o.NextAttemptAt == nil || o.NextAttemptAt.Sub(now) != 10*time.Minute {
		t.Fatalf("next attempt = %v, want now+10m", o.NextAttemptAt)
	}
}

type testErr struct{}

func (testErr) Error() string { return "timeout" }

var errTest = testErr{}
