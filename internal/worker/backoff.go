package worker

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultSchedule is the wait after each failed attempt (8 attempts total,
// about 4.7 hours). It starts fast because most failures are brief blips, then
// backs off so a receiver that's down isn't hammered while it recovers.
var DefaultSchedule = []time.Duration{
	5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute,
	30 * time.Minute, 1 * time.Hour, 3 * time.Hour,
}

// FastSchedule is for local demos, so a delivery reaches the DLQ in about 4 minutes.
var FastSchedule = []time.Duration{
	2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second,
	30 * time.Second, 60 * time.Second, 120 * time.Second,
}

const maxRetryAfter = time.Hour

// NextDelay returns the wait after failed attempt number `attempt` (1-based),
// with ±20% jitter. Without jitter, if a receiver goes down and 10,000
// deliveries fail at once, all 10,000 retry at the same instant and knock it
// over again the moment it recovers (the "thundering herd" problem).
func NextDelay(schedule []time.Duration, attempt int) time.Duration {
	i := attempt - 1
	if i < 0 {
		i = 0
	}
	if i >= len(schedule) {
		i = len(schedule) - 1
	}
	base := float64(schedule[i])
	return time.Duration(base * (0.8 + 0.4*rand.Float64()))
}

// parseRetryAfter reads a Retry-After header, which can be seconds ("120") or
// an HTTP date. Capped so a receiver can't park deliveries forever.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	}
	if d < 0 {
		return 0
	}
	return min(d, maxRetryAfter)
}
