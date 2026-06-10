// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartFreshPostgres brings up a standalone postgres:15-alpine container
// and returns its DSN. No rimsky migrations are applied — callers that
// need rimsky's schema use BringUpRimsky instead. Cleanup is registered
// via t.Cleanup.
//
// Replacement for the pre-2026-05-24 rimsky-internal
// `pkg:internal/pgtest::StartFreshPostgresDSN` helper, which is not
// reachable from lib/services under the `consumption-side-isolation`
// depguard.
func StartFreshPostgres(ctx context.Context, t testing.TB) string {
	t.Helper()
	c, err := pgmodule.Run(ctx,
		"postgres:15-alpine",
		pgmodule.WithDatabase("rimsky_test"),
		pgmodule.WithUsername("test"),
		pgmodule.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(120*time.Second),
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(120*time.Second),
			),
		),
	)
	if err != nil {
		t.Fatalf("harness: start postgres: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("harness: postgres DSN: %v", err)
	}
	return dsn
}

// StartFreshPostgresWithAlias brings up a standalone postgres:15-alpine
// container on the given existing docker network with the given hostname
// alias, returning two DSNs:
//   - internalDSN: reachable from sibling containers on the network at
//     `host=<alias>`. Use this in env vars handed to other containers
//     (e.g. RIMSKY_SENSOR_CRON_STATE_DSN on the sensor-cron peer).
//   - hostDSN: reachable from the test process via the mapped host port.
//     Use this for direct pgxpool queries that assert against the same
//     durable state the sibling container is reading/writing.
//
// No rimsky migrations are applied — callers that need rimsky's schema
// use BringUpRimsky instead. Cleanup is registered via t.Cleanup.
//
// Used by tests that bring up a sibling persistence backend for a peer
// service (e.g. the sensor-cron restart-recovery scenario needs a
// dedicated state DB on the shared rimsky network).
func StartFreshPostgresWithAlias(ctx context.Context, t testing.TB, networkName, alias string) (internalDSN, hostDSN string) {
	t.Helper()
	c, err := pgmodule.Run(ctx,
		"postgres:15-alpine",
		pgmodule.WithDatabase("rimsky_test"),
		pgmodule.WithUsername("test"),
		pgmodule.WithPassword("test"),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(120*time.Second),
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(120*time.Second),
			),
		),
	)
	if err != nil {
		t.Fatalf("harness: start postgres (alias=%s): %v", alias, err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	host, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("harness: postgres DSN (alias=%s): %v", alias, err)
	}
	// Sibling-container DSN: the pgmodule Run defaults to user/pass/db
	// test/test/rimsky_test and binds 5432 inside the container. The
	// network alias resolves to the container's bridge IP, so peers on
	// the same network reach Postgres at the standard 5432.
	in := "postgres://test:test@" + alias + ":5432/rimsky_test?sslmode=disable"
	return in, host
}
