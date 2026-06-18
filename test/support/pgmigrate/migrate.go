// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package pgmigrate

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/testpg"
)

func StartPostgres(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	d := OpenDriver(ctx, t)
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgmigrate: PoolFromDatabaseForTest returned !ok")
	}
	return pool, func() {}
}

func ExecForTest(ctx context.Context, t *testing.T, d persistence.Database, sql string, args ...any) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgmigrate.ExecForTest: not a postgres driver")
	}
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("pgmigrate.ExecForTest: %v\nsql: %s", err, sql)
	}
}

func QueryRowForTest(ctx context.Context, t *testing.T, d persistence.Database, sql string, args []any, dest ...any) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgmigrate.QueryRowForTest: not a postgres driver")
	}
	if err := pool.QueryRow(ctx, sql, args...).Scan(dest...); err != nil {
		t.Fatalf("pgmigrate.QueryRowForTest: %v\nsql: %s", err, sql)
	}
}

func QueryForTest(ctx context.Context, t *testing.T, d persistence.Database,
	sql string, args []any, scan func(scan func(...any) error) error) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgmigrate.QueryForTest: not a postgres driver")
	}
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("pgmigrate.QueryForTest: %v\nsql: %s", err, sql)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows.Scan); err != nil {
			t.Fatalf("pgmigrate.QueryForTest: scan: %v\nsql: %s", err, sql)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pgmigrate.QueryForTest: rows: %v\nsql: %s", err, sql)
	}
}

func HoldAdvisoryLock(ctx context.Context, t *testing.T, d persistence.Database, key int64) (release func()) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgmigrate.HoldAdvisoryLock: not a postgres driver")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("pgmigrate.HoldAdvisoryLock: acquire: %v", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
		conn.Release()
		t.Fatalf("pgmigrate.HoldAdvisoryLock: try lock: %v", err)
	}
	if !got {
		conn.Release()
		t.Fatalf("pgmigrate.HoldAdvisoryLock: failed to acquire (already held)")
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
	dsn, terminate := testpg.StartFreshPostgresDSN(ctx, t)
	t.Cleanup(terminate)

	d, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		t.Fatalf("pgmigrate: open driver: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("pgmigrate: migrate: %v", err)
	}
	return d
}
