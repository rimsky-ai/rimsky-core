// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @source: lib/foundation/persistence/postgres/database.go
// @diverged: true
// @reason: parallel driver — SQLite connection pool (modernc.org/sqlite single-writer with widened pool for in-process unified stack) vs Postgres (pgx connection pool, no writer-serialization constraint)

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @decision: persistence-driver — the in-process unified stack
// (compose-run verb) and the supervisor's settle tx + control-api's
// request handlers all open their own Begin against the same driver.
// At MaxOpenConns=1, a long-running tx (the supervisor's dispatch
// settle, typically tens of ms) blocks every other goroutine's Begin
// in the process; under the verb's terminal-wait poll loop this
// manifests as control-api handlers and the wait-loop polls they
// back receiving context-deadline-exceeded errors after ~30s. The
// SQLite writer slot at the FILE level is still 1 (writers serialize
// via busy_timeout=5000ms), so widening the pool does not break the
// writer-slot invariant; read-only paths under WAL run lock-free and
// benefit from the wider pool. The pool size is decoupled from the
// writer-slot count.
const sqliteMaxOpenConns = 8

func init() {
	persistence.RegisterSQLite(open)
}

type database struct {
	db *sql.DB
	c  *advisoryLockerImpl
	s  *tablesImpl
	q  *queueImpl
}

func (d *database) Queue() persistence.Queue {
	if d.q == nil {
		return nil
	}
	return d.q
}

func (d *database) Tables() persistence.Tables {
	if d.s == nil {
		return nil
	}
	return d.s
}

func (d *database) AdvisoryLocker() persistence.AdvisoryLocker {
	if d.c == nil {
		return nil
	}
	return d.c
}

func (d *database) SetBlobBackend(bb persistence.BlobBackend, threshold int, retention time.Duration) {
	if d.s != nil {
		d.s.SetBlobBackend(bb, threshold, retention)
	}
}

func (d *database) Close() error { return d.db.Close() }

func (d *database) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

func (d *database) Migrate(ctx context.Context, log shared.Logger) error {
	if d.c == nil {
		return errors.New("sqlite driver: advisory locker not initialized")
	}
	return newMigrator(d.db).Run(ctx, d.c, log)
}

func open(ctx context.Context, cfg persistence.SQLiteConfig) (persistence.Database, error) {
	if !filepath.IsAbs(cfg.Path) {
		return nil, fmt.Errorf("sqlite: path %q must be absolute", cfg.Path)
	}
	if strings.Contains(cfg.Path, "?") {
		return nil, fmt.Errorf("sqlite: path %q must not contain '?' (reserved by the file: URI query string)", cfg.Path)
	}
	parent := filepath.Dir(cfg.Path)
	if _, err := os.Stat(parent); err != nil {
		return nil, fmt.Errorf("sqlite: parent dir %q: %w", parent, err)
	}

	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_txlock", "immediate")
	dsn := "file:" + cfg.Path + "?" + q.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(sqliteMaxOpenConns)
	if got := db.Stats().MaxOpenConnections; got != sqliteMaxOpenConns {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: MaxOpenConnections=%d after SetMaxOpenConns(%d) — refusing to boot",
			got, sqliteMaxOpenConns)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}

	slog.Warn("persistence driver in use",
		"driver", "sqlite",
		"path", cfg.Path,
		"warning", "SQLite driver is for local development only — not supported for production. Use the postgres driver for deployed rimsky instances.")

	d := &database{db: db}
	d.c = newAdvisoryLocker(cfg.Path)
	d.s = newTables(db)
	d.q = newQueue(db)
	return d, nil
}
