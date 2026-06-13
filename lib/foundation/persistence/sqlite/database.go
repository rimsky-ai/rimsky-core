// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package sqlite is the SQLite-backed persistence.Database.
//
// SQLite is the dev-only driver per spec §1 and §6. Multi-host /
// multi-replica deployments require Postgres. The startup banner says
// so loudly on every process that opens it.
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

// sqliteMaxOpenConns is the connection-pool size for the SQLite driver.
// MUST stay at 1 — see the comment in open() for the load-bearing details.
const sqliteMaxOpenConns = 1

func init() {
	persistence.RegisterSQLite(open)
}

// database is the persistence.Database impl wrapping a *sql.DB plus the
// per-feature aspect impls. Fully wired: AdvisoryLocker(), Tables(), and
// Queue() all return non-nil concrete impls. SQLite is the dev-only
// driver per spec §6 — for production / multi-replica deploys, use
// the postgres driver.
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

// SetBlobBackend installs the active BlobBackend + spill threshold +
// orphan-retention window on the database's Tables. See the postgres impl
// for the contract; same semantics apply to SQLite.
func (d *database) SetBlobBackend(bb persistence.BlobBackend, threshold int, retention time.Duration) {
	if d.s != nil {
		d.s.SetBlobBackend(bb, threshold, retention)
	}
}

func (d *database) Close() error { return d.db.Close() }

// Ping issues a trivial round-trip to surface connectivity problems.
func (d *database) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// Migrate runs all embedded SQL migrations under the advisory-locker's
// migration lock.
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
	// The path is spliced into a `file:` URI below, where `?` begins the
	// query string — a `?` in the path would make the database file the
	// driver opens diverge from the path the advisory locker derives its
	// lock-file names from (two processes could lock different files).
	// Reject it outright rather than risking a split-brain.
	if strings.Contains(cfg.Path, "?") {
		return nil, fmt.Errorf("sqlite: path %q must not contain '?' (reserved by the file: URI query string)", cfg.Path)
	}
	parent := filepath.Dir(cfg.Path)
	if _, err := os.Stat(parent); err != nil {
		return nil, fmt.Errorf("sqlite: parent dir %q: %w", parent, err)
	}

	// modernc.org/sqlite supports PRAGMA via _pragma=name=value query
	// params. Repeat the param for each PRAGMA. _txlock=immediate runs
	// each tx as BEGIN IMMEDIATE so the writer slot is held for the
	// whole tx (the SQLite analogue of a shared exclusive lock — used by
	// the coordinator's no-op named/scope locks).
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
	// sqliteMaxOpenConns is NOT load-bearing for correctness. Conn-level
	// serialization only ever covered one process, and no read-then-write
	// impl relies on it anymore: the Tables layer requires an explicit
	// caller-supplied tx (tablesImpl.q panics on nil — the BEGIN
	// IMMEDIATE writer-slot hold covers e.g. node_attributes.MergeDelta),
	// and the Queue / APIKey surfaces that accept tx == nil open an
	// internal immediate-mode transaction around their multi-statement
	// sequences (queueImpl.Enqueue, apiKeysImpl.MarkRevoked). Atomicity
	// therefore holds across OS processes sharing the database file, not
	// just across goroutines. The limit stays at 1 for throughput and
	// simplicity: SQLite admits a single writer per database file, so a
	// wider pool would only add SQLITE_BUSY contention and busy_timeout
	// churn between this process's own connections.
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
