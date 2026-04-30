// rimsky-scheduler is the env-var-driven entry point for the
// scheduler process. Builds a typed config.SchedulerConfig from
// environment variables, loads the stores config from
// RIMSKY_STORES_CONFIG (per spec §6.1: name → endpoint + declared
// capabilities), and calls config.StartScheduler.
//
// Environment variables:
//
//	RIMSKY_DB_URL               required; Postgres DSN.
//	RIMSKY_SCHEDULER_TICK_MS    optional; default 1500.
//	RIMSKY_HEARTBEAT_TIMEOUT_MS optional; default 15000.
//	RIMSKY_STORES_CONFIG        optional; default /etc/rimsky/stores.yml.
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

// defaultStoresConfigPath is the path used when RIMSKY_STORES_CONFIG is
// unset.
const defaultStoresConfigPath = "/etc/rimsky/stores.yml"

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

	storesPath := os.Getenv("RIMSKY_STORES_CONFIG")
	if storesPath == "" {
		storesPath = defaultStoresConfigPath
	}
	// The scheduler does not dial remote stores in v3 (the v2 visibility-
	// timeout sweep is gone; the orphan reaper no longer fires
	// Store.Abandon per spec §7.5). It still consumes named_locks for
	// validation; ignore the stores block.
	_, namedLocksCfg, err := config.LoadStoresConfigYAML(storesPath)
	if err != nil {
		log.Error("load stores config", "error", err.Error(), "path", storesPath)
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
		NamedLocks:       namedLocksCfg,
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
