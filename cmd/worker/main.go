package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"hookrelay/internal/store"
	"hookrelay/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://hookrelay:hookrelay@127.0.0.1:5433/hookrelay?sslmode=disable"
	}

	cfg := worker.DefaultConfig()
	if v, err := strconv.Atoi(os.Getenv("WORKER_CONCURRENCY")); err == nil && v > 0 {
		cfg.Concurrency = v
	}
	if os.Getenv("FAST_RETRIES") == "true" {
		cfg.Schedule = worker.FastSchedule
		logger.Warn("FAST_RETRIES enabled: using the short demo retry schedule")
	}
	if os.Getenv("ALLOW_PRIVATE_NETWORKS") == "true" {
		cfg.AllowPrivateNetworks = true
		logger.Warn("ALLOW_PRIVATE_NETWORKS enabled: SSRF protection is OFF (dev only)")
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

	logger.Info("worker started", "concurrency", cfg.Concurrency, "max_attempts", cfg.MaxAttempts)
	_ = worker.New(store.New(pool), cfg, logger).Run(ctx)
	logger.Info("worker stopped")
}
