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
