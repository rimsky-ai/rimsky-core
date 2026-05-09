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
	"time"

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

// SetBlobBackend installs the active BlobBackend + spill threshold +
// orphan-retention window on the driver's Store. Used by D6/D7 wiring
// so NodeAttributes.Upsert/Get spill transparently. Safe to call
// multiple times during construction; the last call wins.
func (d *driver) SetBlobBackend(bb persistence.BlobBackend, threshold int, retention time.Duration) {
	if d.s != nil {
		d.s.SetBlobBackend(bb, threshold, retention)
	}
}

// Close shuts down the connection pool. pgxpool.Pool.Close() is void; the
// Driver-level error return is reserved for future drivers (the SQLite impl
// will plumb a real close error from sql.DB.Close()).
func (d *driver) Close() error { d.pool.Close(); return nil }

// Pool exposes the underlying *pgxpool.Pool for in-package callers
// that need raw pool access (notably NewBlobBackendForDriver). Not
// exported across the persistence boundary — pgx-isolation per
// .golangci.yml prevents pgx imports from modeling/.
func (d *driver) Pool() *pgxpool.Pool { return d.pool }

// NewBlobBackendForDriver constructs the pg-largeobject BlobBackend
// using the pool underlying drv. Returns (nil, false) when drv is not
// a postgres driver. Used by modeling/config.OpenBlobBackend so the
// modeling layer does not need to import pgx (depguard).
func NewBlobBackendForDriver(drv persistence.Driver) (persistence.BlobBackend, bool) {
	pd, ok := drv.(*driver)
	if !ok {
		return nil, false
	}
	return NewPgLargeObjectBackend(pd.pool), true
}

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
