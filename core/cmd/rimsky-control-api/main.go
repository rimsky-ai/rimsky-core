// rimsky-control-api is the env-var-driven entry point for the control
// API HTTP server. Reads RIMSKY_DB_URL, RIMSKY_CONTROL_API_HOST,
// RIMSKY_CONTROL_API_PORT, loads the unified deployment-shape config
// from RIMSKY_CONFIG (stores + named_locks + executors per docs/specs/
// 2026-05-01-control-plane-and-store-lifecycle-design.md §3.1), and
// calls config.StartControlAPI which dials each remote store-service.
//
// Environment variables:
//
//	RIMSKY_DB_URL            required; Postgres DSN for rimsky bookkeeping.
//	RIMSKY_CONTROL_API_HOST  optional; default 127.0.0.1.
//	RIMSKY_CONTROL_API_PORT  optional; default 8080 (0 = OS-assigned).
//	RIMSKY_CONFIG            optional; path to rimsky.yml.
//	                         default /etc/rimsky/rimsky.yml.
//	RIMSKY_LOG_LEVEL         optional; debug|info|warn|error (default info).
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
		fmt.Fprintln(os.Stderr, "rimsky-control-api: missing RIMSKY_DB_URL")
		os.Exit(1)
	}
	host := os.Getenv("RIMSKY_CONTROL_API_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port, _ := strconv.Atoi(os.Getenv("RIMSKY_CONTROL_API_PORT"))
	if port == 0 {
		port = 8080
	}
	logLevel := os.Getenv("RIMSKY_LOG_LEVEL")

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(logLevel)})))
	log := shared.NewSlogLogger(slog.Default())

	configPath := os.Getenv("RIMSKY_CONFIG")
	if configPath == "" {
		configPath = defaultRimskyConfigPath
	}
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

	h, err := config.StartControlAPI(config.ControlAPIConfig{
		Storage:    sb,
		Queue:      q,
		Clock:      shared.SystemClock{},
		Logger:     log,
		Host:       host,
		Port:       port,
		Stores:     rimskyCfg.Stores,
		NamedLocks: rimskyCfg.NamedLocks,
		Executors:  rimskyCfg.Executors,
	})
	if err != nil {
		log.Error("StartControlAPI", "error", err.Error())
		pool.Close()
		os.Exit(1)
	}
	log.Info("control api listening", "addr", h.Addr())

	waitForSignal(log)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		log.Error("control api shutdown", "error", err.Error())
	}
	pool.Close()
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
