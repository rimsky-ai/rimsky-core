package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/shared"

	_ "github.com/fallguy/rimsky/foundation/persistence/postgres" // register driver
	_ "github.com/fallguy/rimsky/foundation/persistence/sqlite"   // register driver
)

func main() {
	logger := shared.NewSlogLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
		logger = shared.NewSlogLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("binary", name))
	}

	if err := run(logger); err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(logger shared.Logger) error {
	cfgPath := os.Getenv("RIMSKY_CONFIG")
	if cfgPath == "" {
		cfgPath = "/etc/rimsky/rimsky.yml"
	}
	cfg, err := config.LoadRimskyConfigYAML(cfgPath)
	if err != nil {
		return fmt.Errorf("load rimsky config: %w", err)
	}

	ctx := context.Background()
	driver, err := persistence.Open(ctx, cfg.Persistence)
	if err != nil {
		return fmt.Errorf("persistence.Open: %w", err)
	}
	defer func() { _ = driver.Close() }()

	if err := driver.Migrate(ctx, logger); err != nil {
		return fmt.Errorf("driver.Migrate: %w", err)
	}
	logger.Info("migrations complete")
	return nil
}
