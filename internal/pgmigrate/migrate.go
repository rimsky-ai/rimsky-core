// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package pgmigrate provides the rimsky-internal Postgres test harness:
// a testcontainers-backed Postgres container with rimsky migrations
// applied, plus pgx-backed escape hatches (ExecForTest / QueryRowForTest
// / etc.) for tests that need to seed or assert against tables the
// persistence interface does not surface.
//
// Stays rimsky-internal because it imports foundation/persistence to
// apply migrations. Service authors building publishers / executors /
// store-services use pkg:sdk/go/testpg instead, which spins up a vanilla
// Postgres without rimsky's schema.
package pgmigrate

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	pgpersist "github.com/fallguyconsulting/rimsky/foundation/persistence/postgres"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/sdk/go/testpg"
)

// StartPostgres spins up a throwaway Postgres 14 container, applies all
// rimsky migrations, and returns a connection pool. Caller MUST invoke the
// returned teardown func (typically via t.Cleanup). Multi-test parallel-
// safe: testcontainers assigns unique container names.
//
// Wraps OpenDriver and returns the underlying pool via the test-only
// PoolFromDatabaseForTest helper. Prefer OpenDriver for new code.
//
// @source: foundation/internal/pgtest/pgtest.go::StartPostgres
// @diverged: false
// @reason: depguard visibility — pkg:internal/pgmigrate is reachable from
// rimsky-root callers (test/scenarios/, cmd/), pkg:foundation/internal/pgtest
// is reachable only from foundation/* per the foundation-internal-isolation
// rule. Identical body except for the log prefix; the helper-cluster
// (StartPostgres + Exec/QueryForTest + HoldAdvisoryLock + OpenDriver)
// has to live in both packages because foundation/* tests can't reach
// internal/pgmigrate. Fixes land in both copies.
func StartPostgres(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	d := OpenDriver(ctx, t)
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgmigrate: PoolFromDatabaseForTest returned !ok")
	}
	// Cleanup is registered inside OpenDriver; return a no-op teardown
	// so existing call sites remain backward-compatible.
	return pool, func() {}
}

// ExecForTest runs a raw SQL command against the underlying Postgres
// pool of a persistence.Database. Test-only escape hatch for tests that
// need to seed or mutate state through SQL paths the persistence
// interface does not surface (e.g. directly inserting into
// rimsky_node_runs.last_heartbeat_at). Fatals on driver-mismatch or
// SQL error.
//
// Lives here (not in a per-test package) so callers can stay outside
// the pgx-isolation depguard rule by importing pgmigrate instead of pgx
// directly.
//
// @source: foundation/internal/pgtest/pgtest.go::ExecForTest
// @diverged: false
// @reason: depguard visibility — see StartPostgres above.
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

// QueryRowForTest runs a raw SQL SELECT against the underlying Postgres
// pool of a persistence.Database and scans into dest. Test-only escape
// hatch in the same vein as ExecForTest. Fatals on driver-mismatch or
// SQL error.
//
// @source: foundation/internal/pgtest/pgtest.go::QueryRowForTest
// @diverged: false
// @reason: depguard visibility — see StartPostgres above.
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

// QueryForTest runs a raw SQL SELECT against the underlying Postgres
// pool and invokes scan on each row. Test-only escape hatch for the
// scenario tests that need to walk multiple rows (e.g. listing
// rimsky_frames rows by instance_id) without importing pgx. Fatals on
// driver-mismatch or SQL error; the scan callback returns its own
// error which is reported via t.Fatalf.
//
// @source: foundation/internal/pgtest/pgtest.go::QueryForTest
// @diverged: false
// @reason: depguard visibility — see StartPostgres above.
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

// HoldAdvisoryLock acquires a single Postgres advisory lock on a fresh
// connection from the driver's pool and returns a release fn. Test-only
// helper for the scheduler advisory-lock test (which needs to simulate a
// peer replica holding the per-tick lock). Fatals on driver-mismatch or
// SQL error.
//
// @source: foundation/internal/pgtest/pgtest.go::HoldAdvisoryLock
// @diverged: false
// @reason: depguard visibility — see StartPostgres above.
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
		// context.Background to avoid stranding the lock if the test ctx
		// is already cancelled.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		conn.Release()
	}
}

// OpenDriver spins up a fresh Postgres container, opens a
// persistence.Database against it, applies migrations, and returns the
// driver. Cleanup (Close + Terminate) is registered via t.Cleanup.
//
// Used by tests that target the persistence.Database surface directly
// (conformance suite, scenario harness, post-Task-22 cmd binaries).
//
// @source: foundation/internal/pgtest/pgtest.go::OpenDriver
// @diverged: false
// @reason: depguard visibility — see StartPostgres above. Note that
// internal/pgmigrate's OpenDriver delegates to sdk/go/testpg for the
// container startup; foundation/internal/pgtest cannot do that delegation
// (foundation can't import sdk/go), so its OpenDriver calls its own
// in-package StartFreshPostgresDSN.
func OpenDriver(ctx context.Context, t *testing.T) persistence.Database {
	t.Helper()
	dsn, terminate := testpg.StartFreshPostgresDSN(ctx, t)
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
		t.Fatalf("pgmigrate: open driver: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("pgmigrate: migrate: %v", err)
	}
	return d
}
