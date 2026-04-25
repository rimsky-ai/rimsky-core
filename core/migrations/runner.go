package migrations

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
)

// advisoryLockKey is a fixed int64 used to serialize concurrent migration
// runners via Postgres session-level pg_advisory_lock. Never reuse this
// key elsewhere in rimsky — it's dedicated to migration serialization.
const advisoryLockKey int64 = 5412893270184856212

// Run applies all embedded *.sql files (in filename-sorted order) that haven't
// yet been recorded in rimsky_migrations. Concurrent runners serialize via
// pg_advisory_lock(advisoryLockKey). Idempotent: re-runs report 0 applied.
func Run(ctx context.Context, pool *pgxpool.Pool, log shared.Logger) error {
	// Acquire a dedicated connection so the session-level lock stays held across
	// all statements in this migration pass. Release it at the end.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrations.Run: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("migrations.Run: pg_advisory_lock: %w", err)
	}
	// Use context.Background() for the unlock: if the caller's ctx was
	// canceled mid-migration, we still want the unlock to run so a stuck
	// lock doesn't block a subsequent runner.
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)

	// Ensure tracker table exists (001-initial.sql also creates it with IF NOT
	// EXISTS, but we might need it before applying 001 to record that 001 ran).
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
        filename    TEXT PRIMARY KEY,
        applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
    )`); err != nil {
		return fmt.Errorf("migrations.Run: create tracker: %w", err)
	}

	// Read all .sql files from the embedded FS.
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return fmt.Errorf("migrations.Run: read embed fs: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	applied := 0
	for _, filename := range files {
		// Check if already applied.
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM rimsky_migrations WHERE filename = $1)", filename).Scan(&exists); err != nil {
			return fmt.Errorf("migrations.Run: check %s: %w", filename, err)
		}
		if exists {
			continue
		}

		sqlBytes, err := fs.ReadFile(FS, filename)
		if err != nil {
			return fmt.Errorf("migrations.Run: read %s: %w", filename, err)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrations.Run: begin tx for %s: %w", filename, err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migrations.Run: exec %s: %w", filename, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO rimsky_migrations (filename) VALUES ($1) ON CONFLICT DO NOTHING", filename); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migrations.Run: record %s: %w", filename, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrations.Run: commit %s: %w", filename, err)
		}
		if log != nil {
			log.Info("migration applied", "filename", filename)
		}
		applied++
	}

	if log != nil {
		if applied == 0 {
			log.Info("no migrations to apply")
		} else {
			log.Info("migrations complete", "applied", applied)
		}
	}
	return nil
}
