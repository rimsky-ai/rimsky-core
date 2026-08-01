// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// @decision: persistence-dual-backend
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

var openPostgres = stubOpenPostgres

var openSQLite = stubOpenSQLite

func stubOpenPostgres(context.Context, PostgresConfig) (Database, error) {
	return nil, errors.New("postgres driver not yet wired")
}
func stubOpenSQLite(context.Context, SQLiteConfig) (Database, error) {
	return nil, errors.New("sqlite driver not yet wired")
}

func RegisterPostgres(fn func(context.Context, PostgresConfig) (Database, error)) {
	openPostgres = fn
}
func RegisterSQLite(fn func(context.Context, SQLiteConfig) (Database, error)) {
	openSQLite = fn
}
