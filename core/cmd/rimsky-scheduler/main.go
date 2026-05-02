// rimsky-scheduler is the env-var-driven entry point for the
// scheduler process. Builds a typed config.SchedulerConfig from
// environment variables, loads the unified deployment-shape config
// from RIMSKY_CONFIG (stores + named_locks + executors per docs/specs/
// 2026-05-01-control-plane-and-store-lifecycle-design.md §3.1), and
// calls config.StartScheduler.
//
// Environment variables:
//
//	RIMSKY_DB_URL               required; Postgres DSN.
//	RIMSKY_SCHEDULER_TICK_MS    optional; default 1500.
//	RIMSKY_HEARTBEAT_TIMEOUT_MS optional; default 15000.
//	RIMSKY_CONFIG               optional; default /etc/rimsky/rimsky.yml.
//	RIMSKY_LOG_LEVEL            optional; debug|info|warn|error (default info).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/config"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// defaultRimskyConfigPath is the path used when RIMSKY_CONFIG is unset.
const defaultRimskyConfigPath = "/etc/rimsky/rimsky.yml"

func main() {
	dsn := os.Getenv("RIMSKY_DB_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "rimsky-scheduler: missing RIMSKY_DB_URL")
		os.Exit(1)
	}
	tickMs := atoiDefault(os.Getenv("RIMSKY_SCHEDULER_TICK_MS"), 1500)
	heartbeatMs := atoiDefault(os.Getenv("RIMSKY_HEARTBEAT_TIMEOUT_MS"), 15000)
	logLevel := os.Getenv("RIMSKY_LOG_LEVEL")

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(logLevel)})))
	log := shared.NewSlogLogger(slog.Default())

	configPath := os.Getenv("RIMSKY_CONFIG")
	if configPath == "" {
		configPath = defaultRimskyConfigPath
	}
	// All three rimsky processes dial stores at startup per spec §3.5 /
	// §6.6. The scheduler does not call any of the four runtime verbs
	// today, but the handshake guard keeps rimsky's three processes in
	// lock-step on the operator-declared topology.
	rimskyCfg, err := config.LoadRimskyConfigYAML(configPath)
	if err != nil {
		log.Error("load rimsky config", "error", err.Error(), "path", configPath)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("pgxpool.New", "error", err.Error())
		os.Exit(1)
	}

	sb := pgstorage.New(pool)
	q := pgqueue.New(pool)

	h, err := config.StartScheduler(config.SchedulerConfig{
		Storage:          sb,
		Queue:            q,
		Clock:            shared.SystemClock{},
		Logger:           log,
		TickInterval:     time.Duration(tickMs) * time.Millisecond,
		HeartbeatTimeout: time.Duration(heartbeatMs) * time.Millisecond,
		Pool:             pool,
		Stores:           rimskyCfg.Stores,
		NamedLocks:       rimskyCfg.NamedLocks,
	})
	if err != nil {
		log.Error("StartScheduler", "error", err.Error())
		pool.Close()
		os.Exit(1)
	}

	waitForSignal(log)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		log.Error("scheduler shutdown", "error", err.Error())
	}
	pool.Close()
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return d
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func waitForSignal(log shared.Logger) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	s := <-sigCh
	log.Info("signal received", "signal", s.String())
}
