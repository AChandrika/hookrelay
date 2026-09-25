package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hookrelay/internal/diagnosis"
	"hookrelay/internal/store"
	"hookrelay/internal/worker"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dbURL := getenv("DATABASE_URL", "postgres://hookrelay:hookrelay@127.0.0.1:5433/hookrelay?sslmode=disable")

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
	st := store.New(pool)

	// AI diagnosis: DIAGNOSIS=off disables it; OLLAMA_URL=none uses rules only.
	if os.Getenv("DIAGNOSIS") != "off" {
		cfg.AutoDiagnose = true
		runner := &diagnosis.Runner{
			Store: st, Log: logger,
			Lease: 4 * time.Minute, CacheTTL: 24 * time.Hour, Poll: 2 * time.Second,
		}
		if url := getenv("OLLAMA_URL", "http://127.0.0.1:11434"); url != "none" {
			runner.LLM = diagnosis.NewClient(url, getenv("OLLAMA_MODEL", "qwen3:4b"))
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := runner.LLM.Check(checkCtx); err != nil {
				logger.Warn("AI diagnosis will fall back to rules until this is fixed", "err", err)
			} else {
				logger.Info("AI diagnosis ready", "model", runner.LLM.Model)
			}
			cancel()
		}
		go runner.Run(ctx)
	}

	logger.Info("worker started", "concurrency", cfg.Concurrency, "max_attempts", cfg.MaxAttempts, "auto_diagnose", cfg.AutoDiagnose)
	_ = worker.New(st, cfg, logger).Run(ctx)
	logger.Info("worker stopped")
}
