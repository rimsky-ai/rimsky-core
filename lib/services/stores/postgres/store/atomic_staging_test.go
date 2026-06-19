// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func bootStagingStore(t *testing.T) (*pgxpool.Pool, *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	container, err := pgmodule.Run(ctx,
		"postgres:14-alpine",
		pgmodule.WithDatabase("rimsky"),
		pgmodule.WithUsername("rimsky"),
		pgmodule.WithPassword("rimsky"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("postgres testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() {
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

	st, err := New(context.Background(), Config{
		Connection:     dsn,
		WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)
	return pool, st
}

func schemaExists(t *testing.T, pool *pgxpool.Pool, schema string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
		schema,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("query information_schema.schemata for %q: %v", schema, err)
	}
	return exists
}

func addressSchema(t *testing.T, addr json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(addr, &s); err != nil {
		t.Fatalf("decode Address %q as string: %v", string(addr), err)
	}
	if s == "" {
		t.Fatalf("Open returned an empty Address")
	}
	return s
}

func TestAtomicStaging_SchemaSwap(t *testing.T) {
	pool, st := bootStagingStore(t)
	ctx := context.Background()

	const canonical = "analytics_production"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+canonical); err != nil {
		t.Fatalf("create canonical schema: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE "+canonical+".items (id TEXT, payload TEXT)"); err != nil {
		t.Fatalf("create canonical table: %v", err)
	}

	t.Run("Open reserves a distinct staging schema", func(t *testing.T) {
		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, canonical)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if !out.Available {
			t.Fatalf("Open: expected Available, got Unavailable")
		}
		staging := addressSchema(t, out.Result.Address)

		if staging == canonical {
			t.Fatalf("Open returned the canonical schema %q as the write target; "+
				"expected a distinct staging schema (no schema reservation occurred)", canonical)
		}
		if !schemaExists(t, pool, staging) {
			t.Fatalf("Open did not reserve staging schema %q (absent from information_schema.schemata)", staging)
		}

		if err := st.Abandon(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("Abandon (cleanup): %v", err)
		}
	})

	t.Run("Commit swaps staged rows into canonical and discards staging", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+canonical+" CASCADE"); err != nil {
			t.Fatalf("reset canonical schema: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+canonical); err != nil {
			t.Fatalf("recreate canonical schema: %v", err)
		}

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, canonical)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		staging := addressSchema(t, out.Result.Address)

		if _, err := pool.Exec(ctx, "CREATE TABLE "+staging+".items (id TEXT, payload TEXT)"); err != nil {
			t.Fatalf("create staged table: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"INSERT INTO "+staging+".items (id, payload) VALUES ('a','x'),('b','y'),('c','z')"); err != nil {
			t.Fatalf("seed staged rows: %v", err)
		}

		if err := st.Commit(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		var rowCount int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+canonical+".items").Scan(&rowCount); err != nil {
			t.Fatalf("count canonical rows after Commit: %v", err)
		}
		if rowCount != 3 {
			t.Fatalf("after Commit, canonical %q has %d rows; want 3 (atomic swap did not land staged rows)",
				canonical, rowCount)
		}

		if schemaExists(t, pool, staging) {
			t.Fatalf("after Commit, staging schema %q still exists; the swap must consume it", staging)
		}
	})

	t.Run("Abandon discards staging and leaves canonical unchanged", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+canonical+" CASCADE"); err != nil {
			t.Fatalf("reset canonical schema: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+canonical); err != nil {
			t.Fatalf("recreate canonical schema: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE TABLE "+canonical+".items (id TEXT, payload TEXT)"); err != nil {
			t.Fatalf("create canonical table: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"INSERT INTO "+canonical+".items (id, payload) VALUES ('keep','me')"); err != nil {
			t.Fatalf("seed canonical row: %v", err)
		}

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, canonical)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		staging := addressSchema(t, out.Result.Address)
		if _, err := pool.Exec(ctx, "CREATE TABLE "+staging+".items (id TEXT, payload TEXT)"); err != nil {
			t.Fatalf("create staged table: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"INSERT INTO "+staging+".items (id, payload) VALUES ('drop','this')"); err != nil {
			t.Fatalf("seed staged rows: %v", err)
		}

		if err := st.Abandon(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("Abandon: %v", err)
		}

		if schemaExists(t, pool, staging) {
			t.Fatalf("after Abandon, staging schema %q still exists; it must be dropped", staging)
		}
		var id, payload string
		var rowCount int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+canonical+".items").Scan(&rowCount); err != nil {
			t.Fatalf("count canonical rows after Abandon: %v", err)
		}
		if rowCount != 1 {
			t.Fatalf("after Abandon, canonical %q has %d rows; want 1 (canonical must be unchanged)",
				canonical, rowCount)
		}
		if err := pool.QueryRow(ctx,
			"SELECT id, payload FROM "+canonical+".items").Scan(&id, &payload); err != nil {
			t.Fatalf("read canonical row after Abandon: %v", err)
		}
		if id != "keep" || payload != "me" {
			t.Fatalf("after Abandon, canonical row = (%q,%q); want (keep,me) — abandon altered canonical", id, payload)
		}
	})

	t.Run("swap collision surfaces pg/swap_failed at the store boundary", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+canonical+" CASCADE"); err != nil {
			t.Fatalf("reset canonical schema: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+canonical); err != nil {
			t.Fatalf("recreate canonical schema: %v", err)
		}

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, canonical)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		staging := addressSchema(t, out.Result.Address)
		if _, err := pool.Exec(ctx, "CREATE TABLE "+staging+".items (id TEXT, payload TEXT)"); err != nil {
			t.Fatalf("create staged table: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"INSERT INTO "+staging+".items (id, payload) VALUES ('a','x')"); err != nil {
			t.Fatalf("seed staged rows: %v", err)
		}

		collidingName := staging + "_swap_target_collision"
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+collidingName); err != nil {
			t.Fatalf("create colliding schema: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE TABLE "+canonical+".pinned (n INT)"); err != nil {
			t.Fatalf("create canonical pinned table: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"CREATE VIEW "+collidingName+".dep AS SELECT n FROM "+canonical+".pinned"); err != nil {
			t.Fatalf("create blocking view: %v", err)
		}

		err = st.Commit(ctx, claimID, out.Result.ClaimScope, out.Result.Address)
		if err == nil {
			t.Fatalf("Commit succeeded despite a forced swap collision; expected a pg/swap_failed error")
		}
		if !strings.Contains(err.Error(), "pg/swap_failed") {
			t.Fatalf("Commit collision error = %q; want it to name the pg/swap_failed class", err.Error())
		}
	})
}

func TestAtomicStaging_ListShapeSubClaimCommitAndAbandon_BypassSwapPath_UnderStagedAsync(t *testing.T) {
	_, st := bootStagingStore(t)
	ctx := context.Background()

	listShapeSubClaimScope := json.RawMessage(`{"parent_row_id":"row-parent","key":"k-1"}`)
	listShapeSubClaimAddress := json.RawMessage(nil)

	commitClaimID := uuid.NewString()
	if err := st.Commit(ctx, commitClaimID, listShapeSubClaimScope, listShapeSubClaimAddress); err != nil {
		t.Fatalf("Commit of list-shape sub-claim under staged_async failed (regression: was routed into swap path and decodeSchemaName errored on JSON object): %v", err)
	}

	abandonClaimID := uuid.NewString()
	if err := st.Abandon(ctx, abandonClaimID, listShapeSubClaimScope, listShapeSubClaimAddress); err != nil {
		t.Fatalf("Abandon of list-shape sub-claim under staged_async failed (regression: was routed into swap path and decodeSchemaName errored on JSON object): %v", err)
	}
}
