// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// WrapPgxTxForTest wraps a pgx.Tx as a persistence.Tx for test code
// that holds a tx outside the persistence.Database.Transaction
// callback. Returns nil for a nil input. Test-only.
func WrapPgxTxForTest(tx pgx.Tx) persistence.Tx {
	if tx == nil {
		return nil
	}
	return &pgTx{tx: tx}
}

// PoolFromDatabaseForTest returns the underlying *pgxpool.Pool for a
// postgres-backed persistence.Database. Returns (nil, false) for any
// other driver. Test-only.
func PoolFromDatabaseForTest(d persistence.Database) (*pgxpool.Pool, bool) {
	pd, ok := d.(*database)
	if !ok {
		return nil, false
	}
	return pd.pool, true
}

// TablesFromPoolForTest wraps a *pgxpool.Pool as a persistence.Tables.
// Test-only escape hatch for test files that hold a pool from outside
// the database. Prefer Database.Tables() in new code.
func TablesFromPoolForTest(pool *pgxpool.Pool) persistence.Tables {
	return newTables(pool)
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
