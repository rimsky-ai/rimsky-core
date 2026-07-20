// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type database struct {
	pool *pgxpool.Pool
	c    *advisoryLockerImpl
	s    *tablesImpl
	q    *queueImpl
}

func (d *database) Queue() persistence.Queue                   { return d.q }
func (d *database) Tables() persistence.Tables                 { return d.s }
func (d *database) AdvisoryLocker() persistence.AdvisoryLocker { return d.c }

func (d *database) SetBlobBackend(bb persistence.BlobBackend, threshold int, retention time.Duration) {
	if d.s != nil {
		d.s.SetBlobBackend(bb, threshold, retention)
	}
}

func (d *database) Close() error { d.pool.Close(); return nil }

func (d *database) Pool() *pgxpool.Pool { return d.pool }

func NewBlobBackendForDatabase(db persistence.Database) (persistence.BlobBackend, bool) {
	pd, ok := db.(*database)
	if !ok {
		return nil, false
	}
	return NewPgLargeObjectBackend(pd.pool), true
}

func (d *database) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

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
	if cfg.MinConns > 0 {
		pcfg.MinConns = int32(cfg.MinConns)
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
	d.q.setTables(d.s)
	return d, nil
}
