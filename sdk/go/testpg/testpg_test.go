// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package testpg

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestStartFreshPostgresDSN_BasicConnect confirms the helper returns a
// usable DSN — a fresh connection pool opens and a trivial query
// succeeds. No migrations are applied: the test exercises only the
// vanilla Postgres surface that downstream service authors get.
func TestStartFreshPostgresDSN_BasicConnect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn, teardown := StartFreshPostgresDSN(ctx, t)
	t.Cleanup(teardown)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("expected 1, got %d", one)
	}
}
