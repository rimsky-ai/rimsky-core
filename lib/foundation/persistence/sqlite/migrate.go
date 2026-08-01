// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite/migrations"
)

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
		ApplyOne: func(ctx context.Context, sqlText string, filename string) (err error) {
			conn, connErr := db.Conn(ctx)
			if connErr != nil {
				return fmt.Errorf("acquire migration conn: %w", connErr)
			}
			defer func() {
				if _, pragmaErr := conn.ExecContext(context.WithoutCancel(ctx),
					"PRAGMA foreign_keys=ON"); pragmaErr != nil {
					_ = conn.Raw(func(driverConn any) error { return driver.ErrBadConn })
					if err == nil {
						err = fmt.Errorf("re-enable foreign_keys: %w", pragmaErr)
					}
				}
				_ = conn.Close()
			}()
			if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
				return fmt.Errorf("disable foreign_keys for table rebuilds: %w", err)
			}
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("begin tx: %w", err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(ctx, sqlText); err != nil {
				return fmt.Errorf("exec sql: %w", err)
			}
			if err := failOnForeignKeyViolations(ctx, tx, filename); err != nil {
				return err
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

func failOnForeignKeyViolations(ctx context.Context, tx *sql.Tx, filename string) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var table, parent string
		var rowid, fkID sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkID); err != nil {
			return fmt.Errorf("foreign_key_check after %s: scan violation: %w", filename, err)
		}
		return fmt.Errorf("foreign_key_check after %s: %s rowid=%d violates reference to %s",
			filename, table, rowid.Int64, parent)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("foreign_key_check after %s: %w", filename, err)
	}
	return nil
}
