// Package pgtest provides a test-only helper for spinning up a Postgres
// container, applying rimsky migrations, and returning a ready pool.
// Shared across storage, queue, and scenario tests so each test file
// doesn't re-implement the harness.
package pgtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/fallguy/rimsky/core/migrations"
	"github.com/fallguy/rimsky/core/shared"
)

// StartPostgres spins up a throwaway Postgres 14 container, applies all
// rimsky migrations, and returns a connection pool. Caller MUST invoke the
// returned teardown func (typically via t.Cleanup). Multi-test parallel-
// safe: testcontainers assigns unique container names.
func StartPostgres(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
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

	// SELECT-1 loop to be robust against "container says ready but isn't"
	pool, err := waitForPool(ctx, dsn, 30*time.Second)
	if err != nil {
		t.Fatalf("pgtest: pool wait: %v", err)
	}

	if err := migrations.Run(ctx, pool, shared.SilentLogger{}); err != nil {
		pool.Close()
		_ = container.Terminate(context.Background())
		t.Fatalf("pgtest: migrate: %v", err)
	}

	teardown := func() {
		pool.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("pgtest: terminate warn: %v", err)
		}
	}
	return pool, teardown
}

func waitForPool(ctx context.Context, dsn string, timeout time.Duration) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err := pool.Ping(ctx); err == nil {
			return pool, nil
		} else {
			pool.Close()
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
	}
	return nil, fmt.Errorf("pgtest: pool not ready within %s: %w", timeout, lastErr)
}
