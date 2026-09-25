// Package ratelimit is a fixed-window rate limiter backed by Redis, so the
// limit holds across every API instance instead of per process.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter struct {
	rdb *redis.Client
	now func() time.Time
}

func New(rdb *redis.Client) *Limiter { return &Limiter{rdb: rdb, now: time.Now} }

// Allow counts one request for key in the current window. It returns whether
// the request is allowed and, if not, how long until the window resets.
//
// Fixed windows are simple and cheap (one INCR per request). Their known
// weakness: a client can send `limit` requests at the end of one window and
// `limit` more at the start of the next. A sliding window or token bucket
// smooths that out at the cost of more Redis work; for abuse protection this
// is usually good enough.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	secs := int64(window / time.Second)
	if secs < 1 {
		secs = 1
	}
	now := l.now()
	bucket := now.Unix() / secs
	k := fmt.Sprintf("rl:%s:%d", key, bucket)

	// INCR and EXPIRE in one round trip. NX sets the expiry only on the first
	// request of the window, so the key always disappears when the window ends.
	pipe := l.rdb.TxPipeline()
	count := pipe.Incr(ctx, k)
	pipe.ExpireNX(ctx, k, time.Duration(secs)*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return true, 0, err
	}
	if count.Val() > int64(limit) {
		reset := time.Unix((bucket+1)*secs, 0).Sub(now)
		return false, reset, nil
	}
	return true, 0, nil
}
