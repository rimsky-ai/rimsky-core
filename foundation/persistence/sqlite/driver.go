// Package sqlite is the SQLite-backed persistence.Driver.
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

	_ "modernc.org/sqlite"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// sqliteMaxOpenConns is the connection-pool size for the SQLite driver.
// MUST stay at 1 — see the comment in open() for the load-bearing details.
const sqliteMaxOpenConns = 1

func init() {
	persistence.RegisterSQLite(open)
}

// driver is the persistence.Driver impl wrapping a *sql.DB plus the
// per-feature aspect impls. Fully wired: AdvisoryLocker(), Store(), and
// Queue() all return non-nil concrete impls. SQLite is the dev-only
// driver per spec §6 — for production / multi-replica deploys, use
// the postgres driver.
type driver struct {
	db *sql.DB
	c  *advisoryLockerImpl
	s  *storeImpl
	q  *queueImpl
}

func (d *driver) Queue() persistence.Queue {
	if d.q == nil {
		return nil
	}
	return d.q
}

func (d *driver) Store() persistence.Store {
	if d.s == nil {
		return nil
	}
	return d.s
}

func (d *driver) AdvisoryLocker() persistence.AdvisoryLocker {
	if d.c == nil {
		return nil
	}
	return d.c
}

func (d *driver) Close() error { return d.db.Close() }

// Ping issues a trivial round-trip to surface connectivity problems.
func (d *driver) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// Migrate runs all embedded SQL migrations under the advisory-locker's
// migration lock.
func (d *driver) Migrate(ctx context.Context, log shared.Logger) error {
	if d.c == nil {
		return errors.New("sqlite driver: advisory locker not initialized")
	}
	return newMigrator(d.db).Run(ctx, d.c, log)
}

func open(ctx context.Context, cfg persistence.SQLiteConfig) (persistence.Driver, error) {
	if !filepath.IsAbs(cfg.Path) {
		return nil, fmt.Errorf("sqlite: path %q must be absolute", cfg.Path)
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
	// sqliteMaxOpenConns is load-bearing — multiple per-feature impls
	// (notably node_attributes.MergeDelta) rely on conn-level
	// serialization for read-then-write atomicity when the caller
	// passes tx == nil. Raising this limit would silently introduce
	// races. Any code that wants concurrent SQLite readers must first
	// rewrite every read-then-write impl to either (a) require a
	// caller-supplied tx (BEGIN IMMEDIATE writer-slot hold) or (b) use
	// a SQL-level atomic UPDATE.
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

	d := &driver{db: db}
	d.c = newAdvisoryLocker(db)
	d.s = newStore(db)
	d.q = newQueue(db)
	return d, nil
}
