// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// Migrator runs *.sql files in filename-sorted order under the
// coordinator's migration lock. Each driver supplies its own filesystem,
// exec function, has-applied query, and record-applied mutator.
//
// The lock is held for the full pass via AdvisoryLocker.AcquireMigrationLock
// (Postgres: session-level pg_advisory_lock on a dedicated conn; SQLite:
// sync.Mutex). The release fn must run even if ctx is cancelled — both
// driver impls honor this.
//
// @blessed-invariant 8: session advisory lock on migrations. Held for the
// duration of the batch; released at session close.
type Migrator struct {
	FS        embed.FS
	QueryHas  func(ctx context.Context, filename string) (bool, error)
	Bootstrap func(ctx context.Context) error // ensures rimsky_migrations exists
	// ApplyOne runs the migration SQL and records it in rimsky_migrations
	// inside a single driver-internal transaction. Per-file atomicity is
	// load-bearing — a partially-applied migration with no
	// rimsky_migrations row would re-run on the next pass and likely
	// crash on duplicate-table errors. Each driver implements this
	// with its own tx primitive (Postgres: pool.Begin; SQLite: db.BeginTx).
	ApplyOne func(ctx context.Context, sql string, filename string) error
}

func (m Migrator) Run(ctx context.Context, advLock AdvisoryLocker, log shared.Logger) error {
	release, err := advLock.AcquireMigrationLock(ctx)
	if err != nil {
		return fmt.Errorf("persistence.Migrator: acquire lock: %w", err)
	}
	defer func() {
		if err := release(); err != nil {
			// We can't bubble this once Run has already returned, so log
			// at Warn level so unlock failures are at least visible.
			slog.Default().Warn("persistence.Migrator: release migration lock", "err", err)
		}
	}()

	if m.Bootstrap != nil {
		if err := m.Bootstrap(ctx); err != nil {
			return fmt.Errorf("persistence.Migrator: bootstrap: %w", err)
		}
	}

	entries, err := fs.ReadDir(m.FS, ".")
	if err != nil {
		return fmt.Errorf("persistence.Migrator: read embed fs: %w", err)
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
		has, err := m.QueryHas(ctx, filename)
		if err != nil {
			return fmt.Errorf("persistence.Migrator: check %s: %w", filename, err)
		}
		if has {
			continue
		}
		sqlBytes, err := fs.ReadFile(m.FS, filename)
		if err != nil {
			return fmt.Errorf("persistence.Migrator: read %s: %w", filename, err)
		}
		if err := m.ApplyOne(ctx, string(sqlBytes), filename); err != nil {
			return fmt.Errorf("persistence.Migrator: apply %s: %w", filename, err)
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
