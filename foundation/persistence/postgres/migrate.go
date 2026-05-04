package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/persistence/postgres/migrations"
)

// newMigrator returns the persistence.Migrator wired with Postgres
// callbacks. The lock is acquired by Migrator.Run via the Coordinator.
func newMigrator(pool *pgxpool.Pool) persistence.Migrator {
	return persistence.Migrator{
		FS: migrations.FS,
		Bootstrap: func(ctx context.Context) error {
			_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
				filename    TEXT PRIMARY KEY,
				applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`)
			if err != nil {
				return fmt.Errorf("bootstrap rimsky_migrations: %w", err)
			}
			return nil
		},
		QueryHas: func(ctx context.Context, filename string) (bool, error) {
			var exists bool
			err := pool.QueryRow(ctx,
				"SELECT EXISTS(SELECT 1 FROM rimsky_migrations WHERE filename = $1)",
				filename,
			).Scan(&exists)
			return exists, err
		},
		// ApplyOne runs the migration SQL and records it inside a single
		// pgx tx, preserving the pre-refactor per-file atomicity.
		ApplyOne: func(ctx context.Context, sql string, filename string) error {
			tx, err := pool.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin tx: %w", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, sql); err != nil {
				return fmt.Errorf("exec sql: %w", err)
			}
			if _, err := tx.Exec(ctx,
				"INSERT INTO rimsky_migrations (filename) VALUES ($1) ON CONFLICT DO NOTHING",
				filename,
			); err != nil {
				return fmt.Errorf("record run: %w", err)
			}
			return tx.Commit(ctx)
		},
	}
}
