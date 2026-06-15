// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// Open constructs a Database for the given config. Validates mutual
// exclusion of postgres / sqlite sub-blocks (spec §8.2) and dispatches to
// the per-driver constructor. Constructors live in postgres/ and sqlite/
// and are wired in via package init().
func Open(ctx context.Context, cfg Config) (Database, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("persistence: invalid config: %w", err)
	}
	switch cfg.Driver {
	case "postgres":
		return openPostgres(ctx, *cfg.Postgres)
	case "sqlite":
		return openSQLite(ctx, *cfg.SQLite)
	default:
		return nil, fmt.Errorf("persistence: unknown driver %q", cfg.Driver)
	}
}

// Validate checks the config shape per spec §8.2: driver in
// {postgres,sqlite}, exactly one of Postgres/SQLite is non-nil, and the
// per-driver required fields are populated. The SQLite path must be
// absolute (relative paths are rejected at the loader and re-checked
// here). Exported so config loaders can validate before calling Open.
func (c Config) Validate() error {
	switch c.Driver {
	case "postgres":
		if c.Postgres == nil {
			return errors.New("driver=postgres requires postgres: block")
		}
		if c.SQLite != nil {
			return errors.New("driver=postgres but sqlite: block also present (mutually exclusive)")
		}
		if c.Postgres.DSN == "" {
			return errors.New("postgres.dsn is required")
		}
	case "sqlite":
		if c.SQLite == nil {
			return errors.New("driver=sqlite requires sqlite: block")
		}
		if c.Postgres != nil {
			return errors.New("driver=sqlite but postgres: block also present (mutually exclusive)")
		}
		if c.SQLite.Path == "" {
			return errors.New("sqlite.path is required")
		}
		if !filepath.IsAbs(c.SQLite.Path) {
			return fmt.Errorf("sqlite.path must be absolute (got %q)", c.SQLite.Path)
		}
	case "":
		return errors.New("persistence.driver is required")
	default:
		return fmt.Errorf("unknown driver %q (want postgres or sqlite)", c.Driver)
	}
	return nil
}

// openPostgres is a package-private var so the postgres/ subpackage can
// install the real driver via init(). This avoids open.go importing
// the postgres/ subpackage (which imports persistence/) and creating
// an import cycle.
var openPostgres = stubOpenPostgres

// openSQLite is a package-private var so the sqlite/ subpackage can
// install the real driver via init(); same cycle-break as
// openPostgres.
var openSQLite = stubOpenSQLite

func stubOpenPostgres(context.Context, PostgresConfig) (Database, error) {
	return nil, errors.New("postgres driver not yet wired")
}
func stubOpenSQLite(context.Context, SQLiteConfig) (Database, error) {
	return nil, errors.New("sqlite driver not yet wired")
}

// RegisterPostgres / RegisterSQLite are called from each driver's init()
// to install the constructor. Tests may also call these to install fakes.
func RegisterPostgres(fn func(context.Context, PostgresConfig) (Database, error)) {
	openPostgres = fn
}
func RegisterSQLite(fn func(context.Context, SQLiteConfig) (Database, error)) {
	openSQLite = fn
}
