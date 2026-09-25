package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"hookrelay/internal/api"
	"hookrelay/internal/ratelimit"
	"hookrelay/internal/store"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Dev defaults match docker-compose.yml, so the API runs with no setup.
	dbURL := getenv("DATABASE_URL", "postgres://hookrelay:hookrelay@127.0.0.1:5433/hookrelay?sslmode=disable")
	redisAddr := getenv("REDIS_ADDR", "127.0.0.1:6379")
	// 127.0.0.1 avoids the Windows Firewall prompt in dev. In Docker, set ADDR=:8080.
	addr := getenv("ADDR", "127.0.0.1:8080")
	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		adminToken = "dev-admin-token"
		logger.Warn("ADMIN_TOKEN not set; using insecure dev default")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Error("invalid database config", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		logger.Error("database unreachable", "err", err)
		os.Exit(1)
	}

	var limiter *ratelimit.Limiter
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		logger.Warn("redis unreachable; rate limiting is off", "addr", redisAddr, "err", err)
	} else {
		limiter = ratelimit.New(rdb)
	}
	cancel()
	defer rdb.Close()

	srv := &http.Server{
		Addr: addr,
		Handler: (&api.Server{
			Store: store.New(pool), Limiter: limiter, AdminToken: adminToken, Log: logger,
		}).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown: on Ctrl+C, stop accepting new requests and give
	// in-flight ones up to 10 seconds to finish.
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "err", err)
		}
		close(shutdownDone)
	}()

	logger.Info("api listening", "addr", addr, "rate_limiting", limiter != nil)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
	<-shutdownDone
	logger.Info("api stopped")
}
