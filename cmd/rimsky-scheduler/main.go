// rimsky-scheduler is the env-var-driven entry point for the
// scheduler process. Builds a typed config.SchedulerConfig from
// environment variables, loads the unified deployment-shape config
// from RIMSKY_CONFIG (persistence + stores + named_locks + executors per
// docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md
// §3.1 and 2026-05-02-persistence-pluggable-and-unified-image-design.md
// §8), and calls config.StartScheduler.
//
// Environment variables:
//
//	RIMSKY_CONFIG               optional; default /etc/rimsky/rimsky.yml.
//	RIMSKY_SCHEDULER_TICK_MS    optional; default 1500.
//	RIMSKY_HEARTBEAT_TIMEOUT_MS optional; default 15000.
//	RIMSKY_LOG_LEVEL            optional; debug|info|warn|error (default info).
//	RIMSKY_LOG_BINARY           optional; structured slog field for unified-image.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/fallguy/rimsky/foundation/persistence"
	_ "github.com/fallguy/rimsky/foundation/persistence/postgres" // register driver
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/shared"

	_ "github.com/fallguy/rimsky/foundation/persistence/sqlite" // register driver
)

// defaultRimskyConfigPath is the path used when RIMSKY_CONFIG is unset.
const defaultRimskyConfigPath = "/etc/rimsky/rimsky.yml"

func main() {
	tickMs := atoiDefault(os.Getenv("RIMSKY_SCHEDULER_TICK_MS"), 1500)
	heartbeatMs := atoiDefault(os.Getenv("RIMSKY_HEARTBEAT_TIMEOUT_MS"), 15000)
	logLevel := os.Getenv("RIMSKY_LOG_LEVEL")

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(logLevel)})
	logger := slog.New(handler)
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		logger = logger.With("binary", name)
	}
	slog.SetDefault(logger)
	log := shared.NewSlogLogger(logger)

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
	driver, err := persistence.Open(ctx, rimskyCfg.Persistence)
	if err != nil {
		log.Error("persistence.Open", "error", err.Error())
		os.Exit(1)
	}

	h, err := config.StartScheduler(config.SchedulerConfig{
		Driver:           driver,
		Clock:            shared.SystemClock{},
		Logger:           log,
		TickInterval:     time.Duration(tickMs) * time.Millisecond,
		HeartbeatTimeout: time.Duration(heartbeatMs) * time.Millisecond,
		Stores:           rimskyCfg.Stores,
		NamedLocks:       rimskyCfg.NamedLocks,
	})
	if err != nil {
		log.Error("StartScheduler", "error", err.Error())
		_ = driver.Close()
		os.Exit(1)
	}

	waitForSignal(log)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		log.Error("scheduler shutdown", "error", err.Error())
	}
	_ = driver.Close()
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
