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
		out, err := st.Open(ctx, claimID, canonical, claimproducer.IntentReadWrite)
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

	t.Run("Commit swaps staged rows into a populated canonical and discards staging", func(t *testing.T) {
		resetCanonicalWithRows(t, pool, canonical, "('old','stale')")

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, canonical, claimproducer.IntentReadWrite)
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
			t.Fatalf("Commit over a populated canonical: %v", err)
		}

		if got := canonicalItemCount(t, pool, canonical); got != 3 {
			t.Fatalf("after Commit, canonical %q has %d rows; want 3 (atomic swap did not land staged rows)",
				canonical, got)
		}
		var staleCount int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+canonical+".items WHERE id = 'old'").Scan(&staleCount); err != nil {
			t.Fatalf("count stale rows after Commit: %v", err)
		}
		if staleCount != 0 {
			t.Fatalf("after Commit, canonical %q still holds the pre-swap row; the swap must replace canonical wholesale",
				canonical)
		}

		if schemaExists(t, pool, staging) {
			t.Fatalf("after Commit, staging schema %q still exists; the swap must consume it", staging)
		}
	})

	t.Run("re-publish over an already-committed canonical succeeds", func(t *testing.T) {
		resetCanonicalWithRows(t, pool, canonical, "('seed','v0')")

		publish := func(rows string) string {
			t.Helper()
			claimID := uuid.NewString()
			out, err := st.Open(ctx, claimID, canonical, claimproducer.IntentReadWrite)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			staging := addressSchema(t, out.Result.Address)
			if _, err := pool.Exec(ctx, "CREATE TABLE "+staging+".items (id TEXT, payload TEXT)"); err != nil {
				t.Fatalf("create staged table: %v", err)
			}
			if _, err := pool.Exec(ctx,
				"INSERT INTO "+staging+".items (id, payload) VALUES "+rows); err != nil {
				t.Fatalf("seed staged rows: %v", err)
			}
			if err := st.Commit(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			return staging
		}

		publish("('a','v1'),('b','v1')")

		publish("('c','v2'),('d','v2'),('e','v2')")

		if got := canonicalItemCount(t, pool, canonical); got != 3 {
			t.Fatalf("after re-publish, canonical %q has %d rows; want the 3 second-publish rows", canonical, got)
		}
		var v1Count int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+canonical+".items WHERE payload = 'v1'").Scan(&v1Count); err != nil {
			t.Fatalf("count first-publish rows: %v", err)
		}
		if v1Count != 0 {
			t.Fatalf("after re-publish, canonical %q still holds %d first-publish rows; want 0", canonical, v1Count)
		}
	})

	t.Run("retried Commit after a successful swap is idempotent", func(t *testing.T) {
		resetCanonicalWithRows(t, pool, canonical, "('old','stale')")

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, canonical, claimproducer.IntentReadWrite)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		staging := addressSchema(t, out.Result.Address)
		if _, err := pool.Exec(ctx, "CREATE TABLE "+staging+".items (id TEXT, payload TEXT)"); err != nil {
			t.Fatalf("create staged table: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"INSERT INTO "+staging+".items (id, payload) VALUES ('a','x'),('b','y')"); err != nil {
			t.Fatalf("seed staged rows: %v", err)
		}

		if err := st.Commit(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("first Commit: %v", err)
		}
		if err := st.Commit(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("retried Commit after a successful swap must succeed (idempotent in the claim id): %v", err)
		}

		if got := canonicalItemCount(t, pool, canonical); got != 2 {
			t.Fatalf("after retried Commit, canonical %q has %d rows; want the 2 committed rows intact", canonical, got)
		}
		if schemaExists(t, pool, staging) {
			t.Fatalf("after retried Commit, staging schema %q exists again; retry must not resurrect staging", staging)
		}
	})

	t.Run("read-intent Open returns the canonical pre-stage snapshot, not a staging schema", func(t *testing.T) {
		resetCanonicalWithRows(t, pool, canonical, "('keep','me')")

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, canonical, claimproducer.IntentRead)
		if err != nil {
			t.Fatalf("Open with intent r: %v", err)
		}
		if !out.Available {
			t.Fatalf("Open with intent r: expected Available, got Unavailable")
		}
		addr := addressSchema(t, out.Result.Address)
		if addr != canonical {
			t.Fatalf("Open with intent r returned address %q; want the canonical schema %q (a reader must see the pre-stage snapshot)",
				addr, canonical)
		}
		if schemaExists(t, pool, stagingSchemaName(claimID)) {
			t.Fatalf("Open with intent r created staging schema %q; read claims must not reserve staging", stagingSchemaName(claimID))
		}

		if got := canonicalItemCount(t, pool, addr); got != 1 {
			t.Fatalf("reading through the returned address %q sees %d rows; want 1", addr, got)
		}

		if err := st.Commit(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("Commit of a read claim: %v", err)
		}
		if !schemaExists(t, pool, canonical) {
			t.Fatalf("Commit of a read claim removed the canonical schema %q; read terminals must not touch canonical", canonical)
		}
		if got := canonicalItemCount(t, pool, canonical); got != 1 {
			t.Fatalf("after Commit of a read claim, canonical %q has %d rows; want 1 (unchanged)", canonical, got)
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
		out, err := st.Open(ctx, claimID, canonical, claimproducer.IntentReadWrite)
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
		if err := st.Abandon(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("retried Abandon must succeed (idempotent in the claim id): %v", err)
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

	t.Run("indeterminate swap state surfaces pg/swap_failed at the store boundary", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+canonical+" CASCADE"); err != nil {
			t.Fatalf("reset canonical schema: %v", err)
		}

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, canonical, claimproducer.IntentReadWrite)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		staging := addressSchema(t, out.Result.Address)
		if _, err := pool.Exec(ctx, "DROP SCHEMA "+staging+" CASCADE"); err != nil {
			t.Fatalf("drop staging schema out-of-band: %v", err)
		}

		err = st.Commit(ctx, claimID, out.Result.ClaimScope, out.Result.Address)
		if err == nil {
			t.Fatalf("Commit succeeded with staging gone and canonical absent; expected a pg/swap_failed error")
		}
		if !strings.Contains(err.Error(), "pg/swap_failed") {
			t.Fatalf("Commit indeterminate-state error = %q; want it to name the pg/swap_failed class", err.Error())
		}
	})

	t.Run("write-intent Open rejects a canonical with an external dependent and reserves no staging", func(t *testing.T) {
		const ext = "external_dep_canonical"
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+ext+" CASCADE"); err != nil {
			t.Fatalf("reset canonical: %v", err)
		}
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+ext+"_readers CASCADE"); err != nil {
			t.Fatalf("reset readers schema: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+ext); err != nil {
			t.Fatalf("create canonical: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE TABLE "+ext+".pinned (n INT)"); err != nil {
			t.Fatalf("create pinned table: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+ext+"_readers"); err != nil {
			t.Fatalf("create readers schema: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"CREATE VIEW "+ext+"_readers.dep AS SELECT n FROM "+ext+".pinned"); err != nil {
			t.Fatalf("create external dependent view: %v", err)
		}

		claimID := uuid.NewString()
		_, err := st.Open(ctx, claimID, ext, claimproducer.IntentReadWrite)
		if err == nil {
			t.Fatalf("Open on a canonical with an external dependent succeeded; it must fail fast before staging")
		}
		if !strings.Contains(err.Error(), NotReplaceableClass) {
			t.Fatalf("Open external-dependent error = %q; want it to name the %q class", err.Error(), NotReplaceableClass)
		}
		if schemaExists(t, pool, stagingSchemaName(claimID)) {
			t.Fatalf("Open reserved staging schema %q despite the external dependent; no staging must be created on the reject path",
				stagingSchemaName(claimID))
		}
		if !schemaExists(t, pool, ext+"_readers") {
			t.Fatalf("external dependent schema was destroyed; Open must not touch it")
		}
	})

	t.Run("write-intent Open allows a canonical with only internal cross-references", func(t *testing.T) {
		const internal = "internal_dep_canonical"
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+internal+" CASCADE"); err != nil {
			t.Fatalf("reset canonical: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+internal); err != nil {
			t.Fatalf("create canonical: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE TABLE "+internal+".base (n INT)"); err != nil {
			t.Fatalf("create base table: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"CREATE VIEW "+internal+".derived AS SELECT n FROM "+internal+".base"); err != nil {
			t.Fatalf("create internal dependent view: %v", err)
		}

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, internal, claimproducer.IntentReadWrite)
		if err != nil {
			t.Fatalf("Open on a self-contained canonical with internal cross-refs must succeed: %v", err)
		}
		staging := addressSchema(t, out.Result.Address)
		if !schemaExists(t, pool, staging) {
			t.Fatalf("Open did not reserve staging schema %q for a self-contained canonical", staging)
		}
		if err := st.Abandon(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("Abandon (cleanup): %v", err)
		}
	})

	t.Run("swap-time backstop refuses a mid-flight external dependent without destroying it", func(t *testing.T) {
		const race = "race_dep_canonical"
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+race+" CASCADE"); err != nil {
			t.Fatalf("reset canonical: %v", err)
		}
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+race+"_readers CASCADE"); err != nil {
			t.Fatalf("reset readers schema: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+race); err != nil {
			t.Fatalf("create canonical: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE TABLE "+race+".pinned (n INT)"); err != nil {
			t.Fatalf("create pinned table: %v", err)
		}

		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, race, claimproducer.IntentReadWrite)
		if err != nil {
			t.Fatalf("Open on a self-contained canonical must succeed: %v", err)
		}
		staging := addressSchema(t, out.Result.Address)
		if _, err := pool.Exec(ctx, "CREATE TABLE "+staging+".pinned (n INT)"); err != nil {
			t.Fatalf("stage a table: %v", err)
		}

		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+race+"_readers"); err != nil {
			t.Fatalf("create readers schema: %v", err)
		}
		if _, err := pool.Exec(ctx,
			"CREATE VIEW "+race+"_readers.dep AS SELECT n FROM "+race+".pinned"); err != nil {
			t.Fatalf("create mid-flight external dependent view: %v", err)
		}

		err = st.Commit(ctx, claimID, out.Result.ClaimScope, out.Result.Address)
		if err == nil {
			t.Fatalf("Commit swapped a canonical that gained an external dependent; the backstop must refuse it")
		}
		if !strings.Contains(err.Error(), SwapFailedClass) {
			t.Fatalf("Commit backstop error = %q; want it to name the %q class", err.Error(), SwapFailedClass)
		}
		if !schemaExists(t, pool, race+"_readers") {
			t.Fatalf("the mid-flight external dependent was destroyed; the backstop must not cascade-drop it")
		}
		if !schemaExists(t, pool, race) {
			t.Fatalf("canonical %q was dropped by a refused swap; it must remain intact", race)
		}
		if !schemaExists(t, pool, staging) {
			t.Fatalf("staging %q was consumed by a refused swap; a refused swap must leave staging intact", staging)
		}
		if err := st.Abandon(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("Abandon (cleanup): %v", err)
		}
	})
}

func resetCanonicalWithRows(t *testing.T, pool *pgxpool.Pool, canonical, rows string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+canonical+" CASCADE"); err != nil {
		t.Fatalf("reset canonical schema: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+canonical); err != nil {
		t.Fatalf("recreate canonical schema: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE "+canonical+".items (id TEXT, payload TEXT)"); err != nil {
		t.Fatalf("create canonical table: %v", err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO "+canonical+".items (id, payload) VALUES "+rows); err != nil {
		t.Fatalf("seed canonical rows: %v", err)
	}
}

func canonicalItemCount(t *testing.T, pool *pgxpool.Pool, schema string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM "+schema+".items").Scan(&count); err != nil {
		t.Fatalf("count rows in %s.items: %v", schema, err)
	}
	return count
}

func TestAtomicStaging_TerminalVerbsRejectNonStagingSchemas(t *testing.T) {
	pool, st := bootStagingStore(t)
	ctx := context.Background()

	const victim = "victim_schema"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+victim); err != nil {
		t.Fatalf("create victim schema: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE "+victim+".items (id TEXT)"); err != nil {
		t.Fatalf("create victim table: %v", err)
	}

	scope := json.RawMessage(`"analytics_production"`)
	requireIntact := func(t *testing.T, schema string) {
		t.Helper()
		if !schemaExists(t, pool, schema) {
			t.Fatalf("schema %q was dropped or renamed via a forged non-staging address", schema)
		}
	}

	for _, forged := range []string{"public", victim} {
		forgedAddr := json.RawMessage(`"` + forged + `"`)

		t.Run("Abandon rejects forged address "+forged, func(t *testing.T) {
			if err := st.Abandon(ctx, uuid.NewString(), scope, forgedAddr); err == nil {
				t.Fatalf("Abandon accepted non-staging address %q; it must be rejected", forged)
			}
			requireIntact(t, forged)
		})

		t.Run("Release rejects forged address "+forged, func(t *testing.T) {
			if err := st.Release(ctx, uuid.NewString(), scope, forgedAddr); err == nil {
				t.Fatalf("Release accepted non-staging address %q; it must be rejected", forged)
			}
			requireIntact(t, forged)
		})

		t.Run("Commit rejects forged address "+forged, func(t *testing.T) {
			err := st.Commit(ctx, uuid.NewString(), scope, forgedAddr)
			if err == nil {
				t.Fatalf("Commit accepted non-staging address %q as a rename source; it must be rejected", forged)
			}
			if !strings.Contains(err.Error(), "pg/swap_failed") {
				t.Fatalf("Commit forged-address error = %q; want it to name the pg/swap_failed class", err.Error())
			}
			requireIntact(t, forged)
		})
	}

	t.Run("Commit rejects a staging-prefixed canonical target", func(t *testing.T) {
		claimID := uuid.NewString()
		out, err := st.Open(ctx, claimID, "analytics_production", claimproducer.IntentReadWrite)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		staging := addressSchema(t, out.Result.Address)
		otherStaging := stagingSchemaName("other_claim")
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+otherStaging); err != nil {
			t.Fatalf("create other staging schema: %v", err)
		}
		forgedScope := json.RawMessage(`"` + otherStaging + `"`)
		err = st.Commit(ctx, claimID, forgedScope, out.Result.Address)
		if err == nil {
			t.Fatalf("Commit accepted a staging-prefixed canonical target %q; it must be rejected", otherStaging)
		}
		if !strings.Contains(err.Error(), "pg/swap_failed") {
			t.Fatalf("Commit staging-target error = %q; want it to name the pg/swap_failed class", err.Error())
		}
		requireIntact(t, otherStaging)
		requireIntact(t, staging)
		if err := st.Abandon(ctx, claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
			t.Fatalf("Abandon (cleanup): %v", err)
		}
	})

	t.Run("Open rejects a selector carrying the reserved staging prefix", func(t *testing.T) {
		if _, err := st.Open(ctx, uuid.NewString(), stagingSchemaName("sneaky"), claimproducer.IntentReadWrite); err == nil {
			t.Fatalf("Open accepted a selector carrying the reserved staging prefix; it must be rejected")
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
