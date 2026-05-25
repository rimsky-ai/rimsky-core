// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package postgres is the Postgres-backed persistence.Database. Lifted from
// the previous foundation/persistence/postgres + foundation/persistence/postgres + foundation/persistence/postgres/migrations
// packages and refactored to use persistence.Tx instead of pgx.Tx in
// every interface signature.
//
// This package is the only place outside cmd/, foundation/internal/pgtest/,
// graph/scenario/, stores/, and test/smoke/ permitted to import pgx
// (per spec §2.10; enforced by golangci-lint depguard in `.golangci.yml`).
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// database is the persistence.Database impl wrapping a *pgxpool.Pool plus the
// per-feature aspect impls (queue, tables, advisory-locker).
type database struct {
	pool *pgxpool.Pool
	c    *advisoryLockerImpl
	s    *tablesImpl
	q    *queueImpl
}

func (d *database) Queue() persistence.Queue                   { return d.q }
func (d *database) Tables() persistence.Tables                 { return d.s }
func (d *database) AdvisoryLocker() persistence.AdvisoryLocker { return d.c }

// SetBlobBackend installs the active BlobBackend + spill threshold +
// orphan-retention window on the database's Tables. Used by D6/D7 wiring
// so NodeAttributes.Upsert/Get spill transparently. Safe to call
// multiple times during construction; the last call wins.
func (d *database) SetBlobBackend(bb persistence.BlobBackend, threshold int, retention time.Duration) {
	if d.s != nil {
		d.s.SetBlobBackend(bb, threshold, retention)
	}
}

// Close shuts down the connection pool. pgxpool.Pool.Close() is void; the
// Database-level error return is reserved for future drivers (the SQLite impl
// plumbs a real close error from sql.DB.Close()).
func (d *database) Close() error { d.pool.Close(); return nil }

// Pool exposes the underlying *pgxpool.Pool for in-package callers
// that need raw pool access (notably NewBlobBackendForDatabase). Not
// exported across the persistence boundary — pgx-isolation per
// .golangci.yml constrains pgx imports per the depguard pgx-isolation rule.
func (d *database) Pool() *pgxpool.Pool { return d.pool }

// NewBlobBackendForDatabase constructs the pg-largeobject BlobBackend
// using the pool underlying db. Returns (nil, false) when db is not
// a postgres database. Used by control/config.OpenBlobBackend so the
// control layer does not need to import pgx (depguard).
func NewBlobBackendForDatabase(db persistence.Database) (persistence.BlobBackend, bool) {
	pd, ok := db.(*database)
	if !ok {
		return nil, false
	}
	return NewPgLargeObjectBackend(pd.pool), true
}

// Ping issues a trivial round-trip to the database to surface
// connectivity problems.
func (d *database) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// Migrate runs the embedded SQL files under the advisory-locker's migration
// lock. Idempotent — re-runs apply nothing.
func (d *database) Migrate(ctx context.Context, log shared.Logger) error {
	return newMigrator(d.pool).Run(ctx, d.c, log)
}

func init() {
	persistence.RegisterPostgres(open)
}

func open(ctx context.Context, cfg persistence.PostgresConfig) (persistence.Database, error) {
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
	d := &database{pool: pool}
	d.c = newAdvisoryLocker(pool)
	d.s = newTables(pool)
	d.q = newQueue(pool)
	return d, nil
}
