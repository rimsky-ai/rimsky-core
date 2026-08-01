// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pgpool"
)

var sharedPostgresPool = pgpool.New(pgpool.Config{
	Image:    "postgres:15-alpine",
	Database: "rimsky_test",
	User:     "test",
	Password: "test",
})

func StartFreshPostgres(ctx context.Context, t testing.TB) string {
	t.Helper()
	return sharedPostgresPool.AcquireFresh(ctx, t)
}

func StartFreshPostgresWithAlias(ctx context.Context, t testing.TB, networkName, alias string) (internalDSN, hostDSN string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	c, err := runPostgresWithRetry(ctx,
		"postgres:15-alpine",
		pgmodule.WithDatabase("rimsky_test"),
		pgmodule.WithUsername("test"),
		pgmodule.WithPassword("test"),
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(120*time.Second),
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(120*time.Second),
			),
		),
	)
	if err != nil {
		t.Fatalf("harness: start postgres (alias=%s): %v", uniqueAlias, err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	host, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("harness: postgres DSN (alias=%s): %v", uniqueAlias, err)
	}
	in := "postgres://test:test@" + uniqueAlias + ":5432/rimsky_test?sslmode=disable"
	return in, host
}
