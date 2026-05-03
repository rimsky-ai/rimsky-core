// Package pgtest provides a test-only helper for spinning up a Postgres
// container, applying rimsky migrations, and returning a ready pool.
// Shared across storage, queue, and scenario tests so each test file
// doesn't re-implement the harness.
package pgtest

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/fallguy/rimsky/core/persistence"
	pgpersist "github.com/fallguy/rimsky/core/persistence/postgres"
	"github.com/fallguy/rimsky/core/shared"
)

// StartPostgres spins up a throwaway Postgres 14 container, applies all
// rimsky migrations, and returns a connection pool. Caller MUST invoke the
// returned teardown func (typically via t.Cleanup). Multi-test parallel-
// safe: testcontainers assigns unique container names.
//
// Wraps OpenDriver and returns the underlying pool via the test-only
// PoolFromDriverForTest helper. Prefer OpenDriver for new code.
func StartPostgres(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	d := OpenDriver(ctx, t)
	pool, ok := pgpersist.PoolFromDriverForTest(d)
	if !ok {
		t.Fatalf("pgtest: PoolFromDriverForTest returned !ok")
	}
	// Cleanup is registered inside OpenDriver; return a no-op teardown
	// so existing call sites remain backward-compatible.
	return pool, func() {}
}

// StartFreshPostgresDSN spins up a Postgres container and returns the DSN
// without applying any migrations. Caller MUST invoke the returned
// teardown func. Used by tests that exercise the migration runner itself.
func StartFreshPostgresDSN(ctx context.Context, t *testing.T) (string, func()) {
	t.Helper()
	container, err := pgmodule.Run(ctx,
		"postgres:14-alpine",
		pgmodule.WithDatabase("rimsky"),
		pgmodule.WithUsername("rimsky"),
		pgmodule.WithPassword("rimsky"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("pgtest: start postgres: %v", err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pgtest: connection string: %v", err)
	}
	teardown := func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("pgtest: terminate warn: %v", err)
		}
	}
	return dsn, teardown
}

// ExecForTest runs a raw SQL command against the underlying Postgres
// pool of a persistence.Driver. Test-only escape hatch for tests that
// need to seed or mutate state through SQL paths the persistence
// interface does not surface (e.g. directly inserting into
// rimsky_dispatch.last_heartbeat_at). Fatals on driver-mismatch or
// SQL error.
//
// Lives here (not in a per-test package) so callers can stay outside
// the pgx-isolation depguard rule by importing pgtest instead of pgx
// directly.
func ExecForTest(ctx context.Context, t *testing.T, d persistence.Driver, sql string, args ...any) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDriverForTest(d)
	if !ok {
		t.Fatalf("pgtest.ExecForTest: not a postgres driver")
	}
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("pgtest.ExecForTest: %v\nsql: %s", err, sql)
	}
}

// QueryRowForTest runs a raw SQL SELECT against the underlying Postgres
// pool of a persistence.Driver and scans into dest. Test-only escape
// hatch in the same vein as ExecForTest. Fatals on driver-mismatch or
// SQL error.
func QueryRowForTest(ctx context.Context, t *testing.T, d persistence.Driver, sql string, args []any, dest ...any) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDriverForTest(d)
	if !ok {
		t.Fatalf("pgtest.QueryRowForTest: not a postgres driver")
	}
	if err := pool.QueryRow(ctx, sql, args...).Scan(dest...); err != nil {
		t.Fatalf("pgtest.QueryRowForTest: %v\nsql: %s", err, sql)
	}
}

// QueryForTest runs a raw SQL SELECT against the underlying Postgres
// pool and invokes scan on each row. Test-only escape hatch for the
// scenario tests that need to walk multiple rows (e.g. listing
// rimsky_frames rows by instance_id) without importing pgx. Fatals on
// driver-mismatch or SQL error; the scan callback returns its own
// error which is reported via t.Fatalf.
func QueryForTest(ctx context.Context, t *testing.T, d persistence.Driver,
	sql string, args []any, scan func(scan func(...any) error) error) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDriverForTest(d)
	if !ok {
		t.Fatalf("pgtest.QueryForTest: not a postgres driver")
	}
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("pgtest.QueryForTest: %v\nsql: %s", err, sql)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows.Scan); err != nil {
			t.Fatalf("pgtest.QueryForTest: scan: %v\nsql: %s", err, sql)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pgtest.QueryForTest: rows: %v\nsql: %s", err, sql)
	}
}

// HoldAdvisoryLock acquires a single Postgres advisory lock on a fresh
// connection from the driver's pool and returns a release fn. Test-only
// helper for the scheduler advisory-lock test (which needs to simulate a
// peer replica holding the per-tick lock). Fatals on driver-mismatch or
// SQL error.
func HoldAdvisoryLock(ctx context.Context, t *testing.T, d persistence.Driver, key int64) (release func()) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDriverForTest(d)
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
		// context.Background to avoid stranding the lock if the test ctx
		// is already cancelled.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		conn.Release()
	}
}

// OpenDriver spins up a fresh Postgres container, opens a
// persistence.Driver against it, applies migrations, and returns the
// driver. Cleanup (Close + Terminate) is registered via t.Cleanup.
//
// Used by tests that target the persistence.Driver surface directly
// (conformance suite, scenario harness, post-Task-22 cmd binaries).
func OpenDriver(ctx context.Context, t *testing.T) persistence.Driver {
	t.Helper()
	dsn, terminate := StartFreshPostgresDSN(ctx, t)
	// Register the container teardown immediately so a panic in
	// persistence.Open does not leak the container. Cleanups run in LIFO
	// order: the driver Close() registered below runs before this
	// terminate.
	t.Cleanup(terminate)

	d, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		t.Fatalf("pgtest: open driver: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("pgtest: migrate: %v", err)
	}
	return d
}
