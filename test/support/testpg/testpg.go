// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const portMappingMaxAttempts = 8

const portMappingMaxBackoff = 2 * time.Second

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
