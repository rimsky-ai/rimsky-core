// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite/migrations"
)

// newMigrator returns the persistence.Migrator wired with SQLite
// callbacks. The lock is acquired by Migrator.Run via the Coordinator
// (sync.Mutex under SQLite — single-process is the only supported
// topology).
func newMigrator(db *sql.DB) persistence.Migrator {
	return persistence.Migrator{
		FS: migrations.FS,
		Bootstrap: func(ctx context.Context) error {
			_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
                filename    TEXT PRIMARY KEY,
                applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
            )`)
			if err != nil {
				return fmt.Errorf("bootstrap rimsky_migrations: %w", err)
			}
			return nil
		},
		QueryHas: func(ctx context.Context, filename string) (bool, error) {
			var n int
			err := db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM rimsky_migrations WHERE filename = ?",
				filename,
			).Scan(&n)
			return n > 0, err
		},
		ApplyOne: func(ctx context.Context, sqlText string, filename string) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("begin tx: %w", err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(ctx, sqlText); err != nil {
				return fmt.Errorf("exec sql: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO rimsky_migrations (filename) VALUES (?) ON CONFLICT DO NOTHING",
				filename,
			); err != nil {
				return fmt.Errorf("record run: %w", err)
			}
			return tx.Commit()
		},
	}
}
