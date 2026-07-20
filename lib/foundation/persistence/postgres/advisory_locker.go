// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: advisory-lock

package postgres

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const (
	RimskySchedulerTickLockKey int64 = 4853127298010834892

	advisoryMigrationLockKey int64 = 5412893270184856212
)

type advisoryLockerImpl struct {
	pool *pgxpool.Pool
}

func newAdvisoryLocker(pool *pgxpool.Pool) *advisoryLockerImpl {
	return &advisoryLockerImpl{pool: pool}
}

func (c *advisoryLockerImpl) TrySchedulerTick(ctx context.Context) (bool, func(), error) {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("postgres.TrySchedulerTick: acquire: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", RimskySchedulerTickLockKey).Scan(&got); err != nil {
		conn.Release()
		return false, nil, fmt.Errorf("postgres.TrySchedulerTick: try lock: %w", err)
	}
	if !got {
		conn.Release()
		return false, nil, nil
	}
	release := func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", RimskySchedulerTickLockKey)
		conn.Release()
	}
	return true, release, nil
}

func (c *advisoryLockerImpl) AcquireMigrationLock(ctx context.Context) (func() error, error) {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres.AcquireMigrationLock: acquire: %w", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryMigrationLockKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("postgres.AcquireMigrationLock: lock: %w", err)
	}
	return func() error {
		_, err := conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryMigrationLockKey)
		conn.Release()
		if err != nil {
			slog.Default().Warn("migration advisory unlock failed",
				"lock_key", advisoryMigrationLockKey, "err", err)
			return fmt.Errorf("postgres.AcquireMigrationLock: unlock: %w", err)
		}
		return nil
	}, nil
}

func (c *advisoryLockerImpl) TakeNamedLockInTx(ctx context.Context, tx persistence.Tx, name string) error {
	pgT, err := unwrapTx(tx)
	if err != nil {
		return fmt.Errorf("postgres.TakeNamedLockInTx: %w", err)
	}
	_, err = pgT.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "rimsky_lock:"+name)
	return err
}

func (c *advisoryLockerImpl) TakeClaimScopeLockInTx(ctx context.Context, tx persistence.Tx, storeName string, claimScopeData []byte) error {
	pgT, err := unwrapTx(tx)
	if err != nil {
		return fmt.Errorf("postgres.TakeClaimScopeLockInTx: %w", err)
	}
	key := "rimsky_scope:" + storeName + ":" + hex.EncodeToString(claimScopeData)
	_, err = pgT.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", key)
	return err
}

var unwrapTx func(persistence.Tx) (pgx.Tx, error)
