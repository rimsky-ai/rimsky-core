// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite/migrations"
)

func newMigrator(db *sql.DB) persistence.Migrator {
	return persistence.Migrator{
		FS: migrations.FS,
		// @decision: migrations-append-only-numbered
		Bootstrap: func(ctx context.Context) error {
			if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
                filename    TEXT PRIMARY KEY,
                applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
            )`); err != nil {
				return fmt.Errorf("bootstrap rimsky_migrations: %w", err)
			}
			present, err := migrationsDigestColumnPresent(ctx, db)
			if err != nil {
				return fmt.Errorf("bootstrap rimsky_migrations digest column: %w", err)
			}
			if present {
				return nil
			}
			if _, err := db.ExecContext(ctx, `ALTER TABLE rimsky_migrations ADD COLUMN digest TEXT`); err != nil {
				return fmt.Errorf("bootstrap rimsky_migrations digest column: %w", err)
			}
			return nil
		},
		QueryApplied: func(ctx context.Context, filename string) (bool, string, error) {
			var digest sql.NullString
			err := db.QueryRowContext(ctx,
				"SELECT digest FROM rimsky_migrations WHERE filename = ?",
				filename,
			).Scan(&digest)
			if errors.Is(err, sql.ErrNoRows) {
				return false, "", nil
			}
			if err != nil {
				return false, "", err
			}
			return true, digest.String, nil
		},
		RecordDigest: func(ctx context.Context, filename string, digest string) error {
			_, err := db.ExecContext(ctx,
				"UPDATE rimsky_migrations SET digest = ? WHERE filename = ? AND digest IS NULL",
				digest, filename)
			return err
		},
		ApplyOne: func(ctx context.Context, sqlText string, filename string, digest string) (err error) {
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
				"INSERT INTO rimsky_migrations (filename, digest) VALUES (?, ?) ON CONFLICT DO NOTHING",
				filename, digest,
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

func migrationsDigestColumnPresent(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(rimsky_migrations)")
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid        int
			name       string
			colType    sql.NullString
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if name == "digest" {
			return true, nil
		}
	}
	return false, rows.Err()
}
