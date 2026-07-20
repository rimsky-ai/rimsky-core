// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func WrapPgxTxForTest(tx pgx.Tx) persistence.Tx {
	if tx == nil {
		return nil
	}
	return &pgTx{tx: tx}
}

func UnwrapTxForTest(tx persistence.Tx) (pgx.Tx, error) {
	return unwrapTx(tx)
}

func PoolFromDatabaseForTest(d persistence.Database) (*pgxpool.Pool, bool) {
	pd, ok := d.(*database)
	if !ok {
		return nil, false
	}
	return pd.pool, true
}

func TablesFromPoolForTest(pool *pgxpool.Pool) persistence.Tables {
	return newTables(pool)
}

func QueueFromPoolForTest(pool *pgxpool.Pool) persistence.Queue {
	q := newQueue(pool)
	q.setTables(newTables(pool))
	return q
}

func AdvisoryLockerFromPoolForTest(pool *pgxpool.Pool) persistence.AdvisoryLocker {
	return newAdvisoryLocker(pool)
}
