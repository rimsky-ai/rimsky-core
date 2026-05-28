// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package testpg provides a testcontainer helper for spinning up a plain
// Postgres container without applying any migrations. Implementer-facing:
// service authors building rimsky publishers / executors / store-services
// can use this to back their own state-DB tests without taking a
// dependency on rimsky's schema.
//
// Rimsky-internal callers needing a Postgres container with rimsky
// migrations applied use pkg:internal/pgmigrate instead (which is built
// on top of this helper).
package testpg

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
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

// StartFreshPostgresDSN spins up a Postgres container and returns the DSN
// without applying any migrations. Caller MUST invoke the returned
// teardown func.
//
// The wait strategy pairs the postgres-ready log signal with
// `wait.ForListeningPort` — both are required to defeat the docker
// port-table race. Without the port-listening wait, ConnectionString
// races against docker reflecting the bound port and intermittently
// fails with `port "5432/tcp" not found` even after the container's
// own ready-log fires. The startup timeout is raised to 300s so the
// per-poll Docker state-query (~1-6s under saturated parallel load;
// occasional 15-20s spikes when the daemon is heavily contended) has
// slack to converge before the strategy gives up.
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
		t.Fatalf("testpg: start postgres: %v", err)
	}
	dsn, err := resolveConnectionString(ctx, t, container)
	if err != nil {
		t.Fatalf("testpg: connection string: %v", err)
	}
	teardown := func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("testpg: terminate warn: %v", err)
		}
	}
	return dsn, teardown
}

// resolveConnectionString calls `container.ConnectionString` and retries
// with exponential backoff on the testcontainers "port not found" race.
// Returns the successful DSN; logs a WARN line per retry so the flake
// becomes observable in CI logs. Production-fast tests (port mapping
// resolves on first attempt) log nothing.
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
		// Retry only on the documented "port not found" race; any other
		// error (context cancelled, container terminated, daemon
		// unreachable) is non-recoverable and surfaces immediately.
		if !strings.Contains(err.Error(), "port") || !strings.Contains(err.Error(), "not found") {
			return "", err
		}
		if attempt < portMappingMaxAttempts {
			t.Logf("testpg: port lookup retry %d/%d for container %s: %v",
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
