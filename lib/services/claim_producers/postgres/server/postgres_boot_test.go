// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func bootPostgresTestContainer(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	container, err := pgmodule.Run(ctx,
		"postgres:14-alpine",
		pgmodule.WithDatabase("rimsky"),
		pgmodule.WithUsername("rimsky"),
		pgmodule.WithPassword("rimsky"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(60*time.Second),
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
			),
		),
	)
	if err != nil {
		t.Skipf("postgres testcontainer unavailable (requires Docker): %v", err)
	}
	t.Cleanup(func() {
		//nolint:testwallclock-pacing the teardown discards the terminate error, so no verdict reads this grace
		termCtx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = container.Terminate(termCtx)
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, dsn
}
