// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// testaccess.go provides test-only escape hatches for code that needs
// raw *pgxpool.Pool access against a postgres-backed
// persistence.Driver. Used by:
//
//   - core/scenario/harness.go — scenario tests seed fixtures via raw
//     SQL (executor_blocked_test, etc.).
//   - test/smoke/setup.go — the smoke fixture's force-fire driver.
//   - core/internal/pgtest — exposes StartPostgres for legacy callers.
//
// Production code MUST go through the persistence.Driver interface.
// Adding a non-test caller of these helpers is a regression
// against blessed-invariant 9a (the persistence layer is the single
// runtime-state surface).
package postgres

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// WrapPgxTxForTest wraps a pgx.Tx as a persistence.Tx for test code
// that holds a tx outside the persistence.Driver.Transaction
// callback. Returns nil for a nil input. Test-only.
func WrapPgxTxForTest(tx pgx.Tx) persistence.Tx {
	if tx == nil {
		return nil
	}
	return &pgTx{tx: tx}
}

// PoolFromDriverForTest returns the underlying *pgxpool.Pool for a
// postgres-backed persistence.Driver. Returns (nil, false) for any
// other driver. Test-only.
func PoolFromDriverForTest(d persistence.Driver) (*pgxpool.Pool, bool) {
	pd, ok := d.(*driver)
	if !ok {
		return nil, false
	}
	return pd.pool, true
}

// StoreFromPoolForTest wraps a *pgxpool.Pool as a persistence.Store.
// Test-only escape hatch for test files that hold a pool from outside
// the driver. Prefer Driver.Store() in new code.
func StoreFromPoolForTest(pool *pgxpool.Pool) persistence.Store {
	return newStore(pool)
}

// QueueFromPoolForTest wraps a *pgxpool.Pool as a persistence.Queue.
// Test-only.
func QueueFromPoolForTest(pool *pgxpool.Pool) persistence.Queue {
	return newQueue(pool)
}

// AdvisoryLockerFromPoolForTest wraps a *pgxpool.Pool as a
// persistence.AdvisoryLocker. Test-only.
func AdvisoryLockerFromPoolForTest(pool *pgxpool.Pool) persistence.AdvisoryLocker {
	return newAdvisoryLocker(pool)
}
