package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestAllow(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("redis not available: %v", err)
		}
		t.Skipf("redis not available: %v", err)
	}

	// Pin the clock to the start of a window so the test can't straddle two.
	l := New(rdb)
	fixed := time.Unix((time.Now().Unix()/60+1)*60, 0)
	l.now = func() time.Time { return fixed }
	key := "test:" + t.Name() + ":" + time.Now().Format(time.RFC3339Nano)

	for i := 1; i <= 3; i++ {
		ok, _, err := l.Allow(ctx, key, 3, time.Minute)
		if err != nil || !ok {
			t.Fatalf("request %d: ok=%v err=%v, want allowed", i, ok, err)
		}
	}
	ok, retry, err := l.Allow(ctx, key, 3, time.Minute)
	if err != nil || ok {
		t.Fatalf("request 4: ok=%v err=%v, want blocked", ok, err)
	}
	if retry != time.Minute {
		t.Fatalf("retry after %v, want 1m (clock is pinned to the window start)", retry)
	}
}
