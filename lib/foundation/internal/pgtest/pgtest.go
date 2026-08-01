// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pgtest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pgpool"
)

var sharedPool = pgpool.NewRimskySchemaPool()

func StartPostgres(ctx context.Context, t *testing.T) *pgxpool.Pool {
	t.Helper()
	d := OpenDriver(ctx, t)
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest: PoolFromDatabaseForTest returned !ok")
	}
	return pool
}

func StartFreshPostgresDSN(ctx context.Context, t *testing.T) string {
	t.Helper()
	return sharedPool.Acquire(ctx, t)
}

func StartUnmigratedPostgresDSN(ctx context.Context, t *testing.T) string {
	t.Helper()
	return sharedPool.AcquireFresh(ctx, t)
}

func ExecForTest(ctx context.Context, t *testing.T, d persistence.Database, sql string, args ...any) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest.ExecForTest: not a postgres driver")
	}
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("pgtest.ExecForTest: %v\nsql: %s", err, sql)
	}
}

func TryExecForTest(ctx context.Context, t *testing.T, d persistence.Database, sql string, args ...any) error {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest.TryExecForTest: not a postgres driver")
	}
	_, err := pool.Exec(ctx, sql, args...)
	return err
}

func QueryRowForTest(ctx context.Context, t *testing.T, d persistence.Database, sql string, dest []any, args ...any) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest.QueryRowForTest: not a postgres driver")
	}
	if err := pool.QueryRow(ctx, sql, args...).Scan(dest...); err != nil {
		t.Fatalf("pgtest.QueryRowForTest: %v\nsql: %s", err, sql)
	}
}

func QueryRowsForTest(ctx context.Context, t *testing.T, d persistence.Database,
	sql string, args ...any) []map[string]any {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest.QueryRowsForTest: not a postgres driver")
	}
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("pgtest.QueryRowsForTest: %v\nsql: %s", err, sql)
	}
	defer rows.Close()
	descs := rows.FieldDescriptions()
	var out []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			t.Fatalf("pgtest.QueryRowsForTest: values: %v\nsql: %s", err, sql)
		}
		row := make(map[string]any, len(descs))
		for i, fd := range descs {
			row[string(fd.Name)] = vals[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pgtest.QueryRowsForTest: rows: %v\nsql: %s", err, sql)
	}
	return out
}

func HoldAdvisoryLock(ctx context.Context, t *testing.T, d persistence.Database, key int64) (release func()) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest.HoldAdvisoryLock: not a postgres driver")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("pgtest.HoldAdvisoryLock: acquire: %v", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
		conn.Release()
		t.Fatalf("pgtest.HoldAdvisoryLock: try lock: %v", err)
	}
	if !got {
		conn.Release()
		t.Fatalf("pgtest.HoldAdvisoryLock: failed to acquire (already held)")
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		conn.Release()
	}
}

func OpenDriver(ctx context.Context, t *testing.T) persistence.Database {
	t.Helper()
	dsn := StartFreshPostgresDSN(ctx, t)

	d, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		t.Fatalf("pgtest: open driver: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	return d
}
