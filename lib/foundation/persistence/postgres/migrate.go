// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres/migrations"
)

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
