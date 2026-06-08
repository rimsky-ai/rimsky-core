// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// TestAtomicStaging_SchemaSwap proves the postgres store is a real
// atomic-staging substrate for a staged-write scope-bytes claim, per
// concept:atomic-staging ("Postgres schema swap: atomic via
// transaction") and spec story S-pgstore-atomic-staging-substrate:
//
//   - Open reserves a per-claim STAGING schema (distinct from the
//     canonical schema named by the selector) — queryable via
//     information_schema.schemata.
//   - The executor writes rows INTO the staging schema (here, a direct
//     INSERT through the pool, as a real executor would over the data
//     path).
//   - Commit performs an ATOMIC swap: the canonical schema reflects the
//     staged rows AND the staging schema is gone afterward.
//   - Abandon discards the staging schema and leaves the canonical
//     schema unchanged.
//   - A swap collision surfaces a `pg/swap_failed`-classed error at the
//     store boundary (the class declared in the executor's
//     declaredErrorClasses but, today, never emitted).
//
// This is a PROOF-FIRST RED test (plan pass AUTHSTORES-15). It MUST
// FAIL against the current code: Store.Open echoes the selector
// verbatim as Address/ClaimScope and reserves no schema; Commit/Abandon
// are no-ops for scope-bytes claims; pg/swap_failed has zero emit
// sites. AUTHSTORES-16 implements the staging lifecycle that turns this
// green.
//
// Rigor note: the swap is asserted to be a REAL schema rename/replace
// (staging gone + canonical present after Commit), not a content copy
// that leaves staging behind — atomicity and no-orphaned-staging are
// the load-bearing properties the substrate must protect.

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

// bootStagingStore stands up a fresh Postgres + a staged_async Store and
// returns the pool (for direct staging-schema seeding) and the store.
// Mirrors server/executor_test.go::bootExecutor; lives here so the
// store-package test can drive Open/Commit/Abandon directly.
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

// schemaExists reports whether the named schema is present in
// information_schema.schemata — the observable surface the spec names
// for "a staging schema is created/reserved (queryable)".
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

// addressSchema decodes the Open result's Address (a JSON string) into
// the schema identity the executor is told to write into. In the staged
// substrate this is the per-claim staging schema; the test asserts that
// schema actually exists and is distinct from the canonical target.
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

	// The canonical schema named by the staged-write claim's selector.
	// Created up front so a successful Commit has a target to swap into,
	// and so we can prove "canonical reflects staged rows after Commit".
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

		// The staging schema MUST be a real reserved schema, and it MUST
		// NOT be the canonical schema itself (an in-place write would
		// defeat the stage-then-swap atomicity the substrate promises).
		if staging == canonical {
			t.Fatalf("Open returned the canonical schema %q as the write target; "+
				"expected a distinct staging schema (no schema reservation occurred)", canonical)
		}
		if !schemaExists(t, pool, staging) {
			t.Fatalf("Open did not reserve staging schema %q (absent from information_schema.schemata)", staging)
		}

		// Cleanly release so the reserved staging schema does not leak
		// into the later sub-tests.
		if err := st.Abandon(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("Abandon (cleanup): %v", err)
		}
	})

	t.Run("Commit swaps staged rows into canonical and discards staging", func(t *testing.T) {
		// Reset the canonical schema to a known-empty state for this case.
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

		// A real executor writes its produced rows into the staging
		// schema over the data path. Here we INSERT directly through the
		// pool, exactly as the executor would against the address it was
		// handed.
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

		// Canonical schema must now reflect the staged rows...
		var rowCount int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+canonical+".items").Scan(&rowCount); err != nil {
			t.Fatalf("count canonical rows after Commit: %v", err)
		}
		if rowCount != 3 {
			t.Fatalf("after Commit, canonical %q has %d rows; want 3 (atomic swap did not land staged rows)",
				canonical, rowCount)
		}

		// ...and the staging schema must be GONE (no orphaned staging).
		if schemaExists(t, pool, staging) {
			t.Fatalf("after Commit, staging schema %q still exists; the swap must consume it", staging)
		}
	})

	t.Run("Abandon discards staging and leaves canonical unchanged", func(t *testing.T) {
		// Reset canonical to a known content the abandon must NOT touch.
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

		// Staging schema discarded.
		if schemaExists(t, pool, staging) {
			t.Fatalf("after Abandon, staging schema %q still exists; it must be dropped", staging)
		}
		// Canonical untouched: still exactly the one pre-existing row.
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
		// Force a swap collision: place a conflicting object in the
		// canonical target so the rename/replace cannot complete. The
		// store MUST surface a `pg/swap_failed`-classed error and leave
		// the staging intact (the canonical is not corrupted).
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

		// Introduce a colliding object the atomic swap cannot rename
		// over: a schema occupying the staging→canonical rename target's
		// intermediate name, or a lock that blocks the rename. The
		// simplest substrate-level collision is a pre-existing schema at
		// the name the swap renames staging INTO. We model that by
		// pre-creating a schema that collides with the swap's rename
		// target, then forcing the rename to fail.
		collidingName := staging + "_swap_target_collision"
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+collidingName); err != nil {
			t.Fatalf("create colliding schema: %v", err)
		}
		// Also occupy the canonical name with a hard, non-droppable-by-
		// swap object dependency so the swap's drop/rename step collides.
		// A view in another schema depending on the canonical schema
		// blocks a DROP ... (without CASCADE) of the canonical, which the
		// atomic swap relies on.
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
