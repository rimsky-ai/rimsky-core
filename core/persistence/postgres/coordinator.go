package postgres

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/persistence"
)

// Constants moved here from core/scheduler/scheduler.go and
// core/migrations/runner.go. Never reuse these int64s elsewhere.
const (
	// RimskySchedulerTickLockKey gates the scheduler tick under
	// pg_try_advisory_lock. @blessed-invariant 7.
	RimskySchedulerTickLockKey int64 = 4853127298010834892

	// advisoryMigrationLockKey serializes concurrent migration runners.
	// @blessed-invariant 8.
	advisoryMigrationLockKey int64 = 5412893270184856212
)

type coordinatorImpl struct {
	pool *pgxpool.Pool
}

func newCoordinator(pool *pgxpool.Pool) *coordinatorImpl {
	return &coordinatorImpl{pool: pool}
}

// TrySchedulerTick — @blessed-invariant 7. The scheduler skips its tick
// when another replica already holds the lock.
func (c *coordinatorImpl) TrySchedulerTick(ctx context.Context) (bool, func(), error) {
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
		// context.Background so a cancelled parent ctx doesn't strand the lock.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", RimskySchedulerTickLockKey)
		conn.Release()
	}
	return true, release, nil
}

// AcquireMigrationLock — @blessed-invariant 8.
//
// Note: holds the lock on a dedicated conn separate from the conn used by
// exec/queryHas/recordRun (per spec §4.1 connection-split note). This is
// a behavior change from the pre-refactor runner; cross-process
// serialization is preserved because the advisory lock is session-scoped
// and this conn lives for the migration's duration.
func (c *coordinatorImpl) AcquireMigrationLock(ctx context.Context) (func() error, error) {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres.AcquireMigrationLock: acquire: %w", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryMigrationLockKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("postgres.AcquireMigrationLock: lock: %w", err)
	}
	return func() error {
		// context.Background so a cancelled parent ctx doesn't strand the lock.
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

// TakeNamedLockInTx — @blessed-invariant 3, 10.
func (c *coordinatorImpl) TakeNamedLockInTx(ctx context.Context, tx persistence.Tx, name string) error {
	pgT, err := unwrapTx(tx)
	if err != nil {
		return fmt.Errorf("postgres.TakeNamedLockInTx: %w", err)
	}
	_, err = pgT.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "rimsky_lock:"+name)
	return err
}

// TakeRegionLockInTx — @blessed-invariant 3, 4b, 10.
func (c *coordinatorImpl) TakeRegionLockInTx(ctx context.Context, tx persistence.Tx, storeName string, regionData []byte) error {
	pgT, err := unwrapTx(tx)
	if err != nil {
		return fmt.Errorf("postgres.TakeRegionLockInTx: %w", err)
	}
	key := "rimsky_region:" + storeName + ":" + hex.EncodeToString(regionData)
	_, err = pgT.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", key)
	return err
}

// unwrapTx asserts that tx was issued by this driver and returns the
// underlying pgx.Tx. Defined in backend.go (Task 9).
//
// nil-tx is rejected by design: this helper is used by callers that
// REQUIRE a tx (advisory locks, FOR UPDATE, multi-statement atomicity).
// Helpers that can target the pool — q() in backend.go — accept nil-tx
// and fall through to the pool. If a future caller switches from
// q().Exec(...) to unwrapTx(tx) and starts seeing nil-tx errors, the
// fix is to keep the call on q() (non-tx work belongs there), not to
// soften this guard.
var unwrapTx func(persistence.Tx) (pgx.Tx, error)
