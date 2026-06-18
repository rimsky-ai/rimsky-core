// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const portMappingMaxAttempts = 8

const portMappingMaxBackoff = 2 * time.Second

func StartPostgres(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	d := OpenDriver(ctx, t)
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("pgtest: PoolFromDatabaseForTest returned !ok")
	}
	return pool, func() {}
}

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
	dsn, terminate := StartFreshPostgresDSN(ctx, t)
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
