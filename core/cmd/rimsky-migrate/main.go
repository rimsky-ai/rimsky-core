package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/migrations"
	"github.com/fallguy/rimsky/core/shared"
)

func main() {
	logger := shared.NewSlogLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	if err := run(logger); err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(logger shared.Logger) error {
	dbURL := os.Getenv("RIMSKY_DB_URL")
	if dbURL == "" {
		return fmt.Errorf("missing RIMSKY_DB_URL")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("open pgxpool: %w", err)
	}
	defer pool.Close()

	if err := migrations.Run(ctx, pool, logger); err != nil {
		return fmt.Errorf("migrations.Run: %w", err)
	}
	return nil
}
