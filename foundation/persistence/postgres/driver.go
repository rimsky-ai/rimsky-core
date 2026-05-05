// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package postgres is the Postgres-backed persistence.Driver. Lifted from
// the previous core/queue/postgres + core/storage/postgres + core/migrations
// packages and refactored to use persistence.Tx instead of pgx.Tx in
// every interface signature.
//
// This package is the only place outside core/cmd/, core/internal/pgtest/,
// core/scenario/, stores/, and test/smoke/ permitted to import pgx
// (per spec §2.10; enforced by golangci-lint depguard in `.golangci.yml`).
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// driver is the persistence.Driver impl wrapping a *pgxpool.Pool plus the
// per-feature aspect impls (queue, store, advisory-locker).
type driver struct {
	pool *pgxpool.Pool
	c    *advisoryLockerImpl
	s    *storeImpl
	q    *queueImpl
}

func (d *driver) Queue() persistence.Queue                   { return d.q }
func (d *driver) Store() persistence.Store                   { return d.s }
func (d *driver) AdvisoryLocker() persistence.AdvisoryLocker { return d.c }

// Close shuts down the connection pool. pgxpool.Pool.Close() is void; the
// Driver-level error return is reserved for future drivers (the SQLite impl
// will plumb a real close error from sql.DB.Close()).
func (d *driver) Close() error { d.pool.Close(); return nil }

// Ping issues a trivial round-trip to the database to surface
// connectivity problems.
func (d *driver) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// Migrate runs the embedded SQL files under the advisory-locker's migration
// lock. Idempotent — re-runs apply nothing.
func (d *driver) Migrate(ctx context.Context, log shared.Logger) error {
	return newMigrator(d.pool).Run(ctx, d.c, log)
}

func init() {
	persistence.RegisterPostgres(open)
}

func open(ctx context.Context, cfg persistence.PostgresConfig) (persistence.Driver, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		pcfg.MaxConns = int32(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		pcfg.MinConns = int32(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		pcfg.MaxConnLifetime = cfg.ConnMaxLifetime
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	d := &driver{pool: pool}
	d.c = newAdvisoryLocker(pool)
	d.s = newStore(pool)
	d.q = newQueue(pool)
	return d, nil
}
