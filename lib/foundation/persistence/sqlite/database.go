// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @decision: persistence-driver
const sqliteUnifiedStackMaxOpenConns = 8

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
	db.SetMaxOpenConns(sqliteUnifiedStackMaxOpenConns)
	if got := db.Stats().MaxOpenConnections; got != sqliteUnifiedStackMaxOpenConns {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: MaxOpenConnections=%d after SetMaxOpenConns(%d) — refusing to boot",
			got, sqliteUnifiedStackMaxOpenConns)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}

	slog.Warn("PERSISTENCE.SQLITEDRIVER.INUSE",
		"driver", "sqlite",
		"path", cfg.Path,
		"warning", "SQLite driver is for local development only — not supported for production. Use the postgres driver for deployed rimsky instances.")

	d := &database{db: db}
	d.c = newAdvisoryLocker(cfg.Path)
	d.s = newTables(db)
	d.q = newQueue(db)
	d.q.setTables(d.s)
	return d, nil
}
