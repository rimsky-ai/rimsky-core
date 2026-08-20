// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres/migrations"
)

func newMigrator(pool *pgxpool.Pool) persistence.Migrator {
	return persistence.Migrator{
		FS: migrations.FS,
		// @decision: migrations-append-only-numbered
		Bootstrap: func(ctx context.Context) error {
			if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
				filename    TEXT PRIMARY KEY,
				applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`); err != nil {
				return fmt.Errorf("bootstrap rimsky_migrations: %w", err)
			}
			if _, err := pool.Exec(ctx,
				`ALTER TABLE rimsky_migrations ADD COLUMN IF NOT EXISTS digest TEXT`); err != nil {
				return fmt.Errorf("bootstrap rimsky_migrations digest column: %w", err)
			}
			return nil
		},
		QueryApplied: func(ctx context.Context, filename string) (bool, string, error) {
			var digest *string
			err := pool.QueryRow(ctx,
				"SELECT digest FROM rimsky_migrations WHERE filename = $1",
				filename,
			).Scan(&digest)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, "", nil
			}
			if err != nil {
				return false, "", err
			}
			if digest == nil {
				return true, "", nil
			}
			return true, *digest, nil
		},
		RecordDigest: func(ctx context.Context, filename string, digest string) error {
			_, err := pool.Exec(ctx,
				"UPDATE rimsky_migrations SET digest = $2 WHERE filename = $1 AND digest IS NULL",
				filename, digest)
			return err
		},
		ApplyOne: func(ctx context.Context, sql string, filename string, digest string) error {
			tx, err := pool.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin tx: %w", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, sql); err != nil {
				return fmt.Errorf("exec sql: %w", err)
			}
			if _, err := tx.Exec(ctx,
				"INSERT INTO rimsky_migrations (filename, digest) VALUES ($1, $2) ON CONFLICT DO NOTHING",
				filename, digest,
			); err != nil {
				return fmt.Errorf("record run: %w", err)
			}
			return tx.Commit(ctx)
		},
	}
}
