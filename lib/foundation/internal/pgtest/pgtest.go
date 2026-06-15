// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package pgtest provides a test-only helper for spinning up a Postgres
// container, applying rimsky migrations, and returning a ready pool.
// Shared across storage, queue, and scenario tests so each test file
// doesn't re-implement the harness.
package pgtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// portMappingMaxAttempts caps the retry loop in resolveConnectionString.
// Under heavy parallel-container load testcontainers occasionally returns
// `port "5432/tcp" not found` from PortEndpoint before the docker port
// table reflects the container's bound port. Empirically the race
// window has been observed extending past 3s under saturated parallel
// load — 5s of capped backoff (200ms + 400ms + 800ms + 1.6s + 2s + 2s +
// 2s + 2s) is the smallest budget that absorbed every observed flake
// in the cleanup-cycle test reruns. Happy-path tests succeed on attempt
// 1 and pay nothing.
const portMappingMaxAttempts = 8

// portMappingMaxBackoff caps the per-retry sleep so the backoff doesn't
// grow without bound on a genuinely-broken container.
const portMappingMaxBackoff = 2 * time.Second

// StartPostgres spins up a throwaway Postgres 14 container, applies all
// rimsky migrations, and returns a connection pool. Caller MUST invoke the
// returned teardown func (typically via t.Cleanup). Multi-test parallel-
// safe: testcontainers assigns unique container names.
//
// Wraps OpenDriver and returns the underlying pool via the test-only
// PoolFromDatabaseForTest helper. Prefer OpenDriver for new code.
func StartPostgres(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	d := OpenDriver(ctx, t)
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest: PoolFromDatabaseForTest returned !ok")
	}
	return pool, func() {}
}

// StartFreshPostgresDSN spins up a Postgres container and returns the DSN
// without applying any migrations. Caller MUST invoke the returned
// teardown func. Used by tests that exercise the migration runner itself.
//
// @source: test/support/testpg/testpg.go::StartFreshPostgresDSN
// @diverged: false
// @reason: foundation/ does not import testpg because that would add a
// foundation→testpg cross-module test dependency (a `replace` directive
// and module coupling) for a ~40-line helper; the duplication is kept
// tracked and byte-identical instead. Keep this body byte-identical
// to the testpg copy modulo the log prefix; port-mapping retry tuning
// lives in two places and any fix must land in both. Symptoms drift
// silently if the copies disagree (testpg consumers see one timeout
// behavior, rimsky-internal tests see another).
//
// The wait strategy pairs the postgres-ready log signal with
// `wait.ForListeningPort` — both are required to defeat the docker
// port-table race. Without the port-listening wait, ConnectionString
// races against docker reflecting the bound port and intermittently
// fails with `port "5432/tcp" not found` even after the container's
// own ready-log fires. The startup timeout is raised to 300s so the
// per-poll Docker state-query (~1-6s under saturated parallel load;
// occasional 15-20s spikes when the daemon is heavily contended) has
// slack to converge before the strategy gives up. Cycle-4 evidence
// showed 180s was tight enough that `wait.ForListeningPort` itself
// would time out (`retries: 9, port: "invalid port"`) under heavy
// parallel scenario runs.
//
// `resolveConnectionString` adds a belt-and-suspenders retry around
// the eventual ConnectionString call to absorb any residual race that
// slips past the wait strategy.
func StartFreshPostgresDSN(ctx context.Context, t *testing.T) (string, func()) {
	t.Helper()
	container, err := pgmodule.Run(ctx,
		"postgres:14-alpine",
		pgmodule.WithDatabase("rimsky"),
		pgmodule.WithUsername("rimsky"),
		pgmodule.WithPassword("rimsky"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(300*time.Second),
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(300*time.Second),
			),
		),
	)
	if err != nil {
		t.Fatalf("pgtest: start postgres: %v", err)
	}
	dsn, err := resolveConnectionString(ctx, t, container)
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

// resolveConnectionString calls `container.ConnectionString` and retries
// with exponential backoff on the testcontainers "port not found" race.
// Returns the successful DSN; logs a WARN line per retry so the flake
// becomes observable in CI logs. Production-fast tests (port mapping
// resolves on first attempt) log nothing.
//
// @source: test/support/testpg/testpg.go::resolveConnectionString
// @diverged: false
// @reason: foundation/ does not import testpg (avoids a foundation→testpg
// cross-module test dependency). See StartFreshPostgresDSN above for the
// divergence-tracking rationale.
func resolveConnectionString(
	ctx context.Context, t *testing.T, container *pgmodule.PostgresContainer,
) (string, error) {
	t.Helper()
	backoff := 200 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= portMappingMaxAttempts; attempt++ {
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err == nil {
			return dsn, nil
		}
		lastErr = err
		// @deliberate: retry only on the documented "port not found"
		// race; any other error (context cancelled, container
		// terminated, daemon unreachable) is non-recoverable and
		// surfaces immediately.
		if !strings.Contains(err.Error(), "port") || !strings.Contains(err.Error(), "not found") {
			return "", err
		}
		if attempt < portMappingMaxAttempts {
			t.Logf("pgtest: port lookup retry %d/%d for container %s: %v",
				attempt, portMappingMaxAttempts, container.GetContainerID(), err)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > portMappingMaxBackoff {
				backoff = portMappingMaxBackoff
			}
		}
	}
	return "", lastErr
}

// ExecForTest runs a raw SQL command against the underlying Postgres
// pool of a persistence.Database. Test-only escape hatch for tests that
// need to seed or mutate state through SQL paths the persistence
// interface does not surface (e.g. directly inserting into
// rimsky_node_runs.last_heartbeat_at). Fatals on driver-mismatch or
// SQL error.
//
// Lives here (not in a per-test package) so callers can stay outside
// the pgx-isolation depguard rule by importing pgtest instead of pgx
// directly.
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

// TryExecForTest runs a raw SQL command and returns the resulting error
// (nil on success). Test-only escape hatch for tests that need to assert
// a CHECK / FK constraint REJECTS an INSERT — ExecForTest fatals on any
// error, which would mask the rejection rather than surface it. Fatals
// only on driver-mismatch.
func TryExecForTest(ctx context.Context, t *testing.T, d persistence.Database, sql string, args ...any) error {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest.TryExecForTest: not a postgres driver")
	}
	_, err := pool.Exec(ctx, sql, args...)
	return err
}

// QueryRowForTest runs a raw SQL SELECT against the underlying Postgres
// pool of a persistence.Database and scans into dest. Test-only escape
// hatch in the same vein as ExecForTest. Fatals on driver-mismatch or
// SQL error.
func QueryRowForTest(ctx context.Context, t *testing.T, d persistence.Database, sql string, args []any, dest ...any) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
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
func QueryForTest(ctx context.Context, t *testing.T, d persistence.Database,
	sql string, args []any, scan func(scan func(...any) error) error) {
	t.Helper()
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
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

// QueryRowsForTest is the column-name-keyed variant of QueryForTest.
// Returns one map per row keyed by the column name; values land as
// whatever pgx scans into `any` (typically string / []byte / int64 /
// time.Time / nil). Cross-driver conformance tests that need to read
// columns not surfaced by the application-layer projections (e.g.
// claimed_by on sibling rimsky_node_runs rows that share a node_id)
// use this helper.
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

// HoldAdvisoryLock acquires a single Postgres advisory lock on a fresh
// connection from the driver's pool and returns a release fn. Test-only
// helper for the scheduler advisory-lock test (which needs to simulate a
// peer replica holding the per-tick lock). Fatals on driver-mismatch or
// SQL error.
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
		// @deliberate: context.Background here (not the test ctx) so an
		// already-cancelled test ctx does not strand the advisory lock
		// on the connection.
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
func OpenDriver(ctx context.Context, t *testing.T) persistence.Database {
	t.Helper()
	dsn, terminate := StartFreshPostgresDSN(ctx, t)
	// @deliberate: register the container teardown before persistence.Open
	// so a panic inside Open does not leak the container. t.Cleanup runs in
	// LIFO order, so the driver Close registered below executes before this
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
