// rimsky-control-api is the env-var-driven entry point for the control
// API HTTP server. Reads RIMSKY_CONFIG (persistence + stores +
// named_locks + executors per docs/specs/2026-05-01-control-plane-and-
// store-lifecycle-design.md §3.1 and 2026-05-02-persistence-pluggable-
// and-unified-image-design.md §8) and calls config.StartControlAPI
// which dials each remote store-service.
//
// Environment variables:
//
//	RIMSKY_CONFIG            optional; path to rimsky.yml.
//	                         default /etc/rimsky/rimsky.yml.
//	RIMSKY_CONTROL_API_HOST  optional; default 127.0.0.1.
//	RIMSKY_CONTROL_API_PORT  optional; default 8080 (0 = OS-assigned).
//	RIMSKY_LOG_LEVEL         optional; debug|info|warn|error (default info).
//	RIMSKY_LOG_BINARY        optional; structured slog field for unified-image.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/fallguy/rimsky/core/config"
	"github.com/fallguy/rimsky/core/persistence"
	_ "github.com/fallguy/rimsky/core/persistence/postgres" // register driver
	"github.com/fallguy/rimsky/core/shared"

	_ "github.com/fallguy/rimsky/core/persistence/sqlite" // register driver
)

// defaultRimskyConfigPath is the path used when RIMSKY_CONFIG is unset.
const defaultRimskyConfigPath = "/etc/rimsky/rimsky.yml"

func main() {
	host := os.Getenv("RIMSKY_CONTROL_API_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port, _ := strconv.Atoi(os.Getenv("RIMSKY_CONTROL_API_PORT"))
	if port == 0 {
		port = 8080
	}
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

	h, err := config.StartControlAPI(config.ControlAPIConfig{
		Driver:     driver,
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
		_ = driver.Close()
		os.Exit(1)
	}
	log.Info("control api listening", "addr", h.Addr())

	waitForSignal(log)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		log.Error("control api shutdown", "error", err.Error())
	}
	_ = driver.Close()
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
