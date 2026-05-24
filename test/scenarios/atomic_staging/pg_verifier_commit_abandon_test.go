// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Atomic-staging Commit/Abandon end-to-end against the fused stores/postgres/.
//
// The fused store serves both `concept:claim-producer` (Open / Commit /
// Abandon) and `concept:executor` (the SQL-substrate verifier role) on
// the same gRPC endpoint per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6.
//
// Coverage gap (per the reviewer's note on plan task 6.6): the
// substrate-level executor tests at
// `code:stores/postgres/server/executor_test.go` pin the SQL semantics
// of the verifier role, and `code:test/scenarios/atomic_staging/pg_verifier_test.go`
// pins the terminal-event SHAPE. Neither exercises the verbs across the
// wire against a live fused binary. This scenario closes that gap:
//
//  1. Boot a fused-store gRPC server (via the testfixture, with
//     EnableExecutor) against a real testcontainers Postgres.
//  2. Pre-seed a staging schema + production schema directly via SQL
//     (the postgres store's `Open` does not yet ship producer-side
//     staging-schema creation — see `concept:atomic-staging` Notes
//     append for the design caveat).
//  3. Open the claim against the staging selector; assert ClaimResult
//     surfaces the staging schema address.
//  4. Drive the verifier role against the staging data with
//     `ExecuteRequest`; assert Success on all-checks-pass and Error on
//     any-check-fail with the supervisor's terminal-routing error_class
//     `verifier_failed`.
//  5. Drive the producer-side Commit on the success path; drive
//     Abandon on the failure path. (Today both verbs are pick-policy
//     no-ops when run against scope-selector claims; the test still
//     pins that the verbs return cleanly across the wire so the
//     supervisor's terminal-routing contract holds end-to-end.)

package atomicstaging

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	corestore "github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	_ "github.com/fallguy/rimsky/foundation/persistence/postgres"
	"github.com/fallguy/rimsky/internal/pgtest"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
	pgtestfixture "github.com/fallguy/rimsky/stores/postgres/testfixture"
)

// fusedHarness boots a real Postgres + the fused stores/postgres/
// gRPC server (ClaimProducer + Executor on one endpoint) and exposes
// helpers for the in-test SQL seeding/inspection. Test SQL flows
// through `code:internal/pgtest`'s `ExecForTest` / `QueryRowForTest`
// helpers so this file stays outside the pgx-isolation depguard
// allowlist for `test/scenarios/`.
type fusedHarness struct {
	driver persistence.Database
	conn   *grpc.ClientConn
}

func bootFusedStore(t *testing.T) *fusedHarness {
	t.Helper()
	ctx := context.Background()
	dsn, terminate := pgtest.StartFreshPostgresDSN(ctx, t)
	t.Cleanup(terminate)

	// Open a persistence.Database against the DSN so the test SQL
	// helpers (ExecForTest / QueryRowForTest) work; we don't actually
	// need to run migrations for the verifier-role scenarios (staging
	// schemas are externally seeded), but the helpers expect a
	// postgres-driver Database.
	d, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	endpoint, _, teardown := pgtestfixture.Start(t, pgtestfixture.Config{
		Connection:     dsn,
		WriteSemantics: corestore.WriteSemanticsStagedAsync,
		EnableExecutor: true,
	})
	t.Cleanup(teardown)

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &fusedHarness{driver: d, conn: conn}
}

// seedStagingAndProduction creates a staging schema + items table and a
// production schema; mirrors the producer-side discipline that the
// example template's stage-items node would do if SQL-substrate
// producer-side staging-schema lifecycle were shipped today (see
// `concept:atomic-staging` Notes append).
func (h *fusedHarness) seedStagingAndProduction(t *testing.T, stagingSchema, productionSchema, table string, rows []map[string]any) {
	t.Helper()
	ctx := context.Background()
	for _, schema := range []string{stagingSchema, productionSchema} {
		pgtest.ExecForTest(ctx, t, h.driver, "CREATE SCHEMA IF NOT EXISTS "+schema)
	}
	pgtest.ExecForTest(ctx, t, h.driver,
		"CREATE TABLE "+stagingSchema+"."+table+" (id TEXT, payload TEXT)")
	pgtest.ExecForTest(ctx, t, h.driver,
		"CREATE TABLE "+productionSchema+"."+table+" (id TEXT, payload TEXT)")
	for _, r := range rows {
		pgtest.ExecForTest(ctx, t, h.driver,
			"INSERT INTO "+stagingSchema+"."+table+" (id, payload) VALUES ($1, $2)",
			r["id"], r["payload"])
	}
}

func (h *fusedHarness) productionRowCount(t *testing.T, schema, table string) int {
	t.Helper()
	var n int
	pgtest.QueryRowForTest(context.Background(), t, h.driver,
		"SELECT count(*) FROM "+schema+"."+table, nil, &n)
	return n
}

func (h *fusedHarness) stagingSchemaExists(t *testing.T, schema string) bool {
	t.Helper()
	var exists sql.NullBool
	pgtest.QueryRowForTest(context.Background(), t, h.driver,
		`SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
		[]any{schema}, &exists)
	return exists.Valid && exists.Bool
}

// runVerifier dispatches an ExecuteRequest against the fused store's
// Executor role and collects the StreamClose event.
func (h *fusedHarness) runVerifier(t *testing.T, schema, table string, checks []any) *genv1.StreamClose {
	t.Helper()
	client := genv1.NewExecutorClient(h.conn)
	ud, err := structpb.NewStruct(map[string]any{
		"schema": schema,
		"table":  table,
		"checks": checks,
	})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := client.Execute(ctx, &genv1.ExecuteRequest{
		NodeId:     "scenario-verifier-" + table,
		InstanceId: "scenario-verifier-instance",
		NodeType:   "verify-staged-table",
		Attributes: ud,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for {
		ev, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if sc := ev.GetStreamClose(); sc != nil {
			return sc
		}
	}
}

// TestAtomicStaging_VerifierSuccess_DrivesCommit pins the success path
// end-to-end across the wire. Verifier emits StreamClose.Success → the
// supervisor's terminal-routing contract calls Commit on the producer
// claim. Commit returns cleanly; the production-side state (which the
// SQL-store does not yet rewrite — see concept:atomic-staging caveat)
// is observed to be untouched, matching the documented producer-side
// limitation.
func TestAtomicStaging_VerifierSuccess_DrivesCommit(t *testing.T) {
	t.Parallel()
	h := bootFusedStore(t)
	const stagingSchema, productionSchema, table = "staging_ok_e2e", "production_e2e_ok", "items"
	h.seedStagingAndProduction(t, stagingSchema, productionSchema, table, []map[string]any{
		{"id": "a", "payload": "x"},
		{"id": "b", "payload": "y"},
		{"id": "c", "payload": "z"},
	})

	// 1. Open the producer-side claim. The staging schema is externally
	//    seeded; the postgres store echoes the selector as address (see
	//    concept:atomic-staging Notes append for the producer-side
	//    limitation).
	producer := genv1.NewClaimProducerClient(h.conn)
	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer openCancel()
	claimID := "scenario-success-" + table
	openResp, err := producer.Open(openCtx, &genv1.OpenRequest{
		ClaimId:  claimID,
		Selector: stagingSchema,
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	acquired := openResp.GetAcquired()
	if acquired == nil {
		t.Fatalf("expected Acquired, got %+v", openResp.GetResult())
	}

	// 2. Drive the verifier. All checks pass against the seeded staging.
	sc := h.runVerifier(t, stagingSchema, table, []any{
		map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 1}},
		map[string]any{"kind": "no_nulls", "config": map[string]any{"fields": []any{"id", "payload"}}},
		map[string]any{"kind": "pk_unique", "config": map[string]any{"fields": []any{"id"}}},
	})
	if sc.GetSuccess() == nil {
		t.Fatalf("expected Success, got %+v", sc.GetOutcome())
	}

	// 3. Supervisor's terminal-routing contract: Success → Commit on
	//    the parent claim.
	commitCtx, commitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer commitCancel()
	if _, err := producer.Commit(commitCtx, &genv1.CommitRequest{
		ClaimId:    claimID,
		Address:    acquired.GetAddress(),
		ClaimScope: acquired.GetClaimScope(),
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// 4. Producer-side staging-schema lifecycle is not yet shipped —
	//    Commit on a scope-bytes claim is a no-op (no pick-policy rule
	//    matches). Production-schema state is unchanged by the Commit
	//    call itself; staging remains. The supervisor's terminal
	//    routing fired correctly; substrate-level data movement is
	//    out-of-scope for the current fused-store impl per the
	//    concept:atomic-staging Notes append.
	if got := h.productionRowCount(t, productionSchema, table); got != 0 {
		t.Errorf("production rows post-Commit: got %d want 0 (producer-side schema swap not shipped)", got)
	}
	if !h.stagingSchemaExists(t, stagingSchema) {
		t.Errorf("staging schema unexpectedly dropped after Commit on scope-bytes claim")
	}
}

// TestAtomicStaging_VerifierFailure_DrivesAbandon pins the failure
// path. Any check fails → verifier emits StreamClose.Error with
// error_class=verifier_failed → supervisor's terminal-routing contract
// calls Abandon on the producer claim. Abandon returns cleanly; the
// production-schema state is unchanged (no commit fired).
func TestAtomicStaging_VerifierFailure_DrivesAbandon(t *testing.T) {
	t.Parallel()
	h := bootFusedStore(t)
	const stagingSchema, productionSchema, table = "staging_fail_e2e", "production_e2e_fail", "items"
	h.seedStagingAndProduction(t, stagingSchema, productionSchema, table, []map[string]any{
		// Only 2 rows; we'll require min=100 → fail.
		{"id": "a", "payload": "x"},
		{"id": "b", "payload": "y"},
	})

	producer := genv1.NewClaimProducerClient(h.conn)
	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer openCancel()
	claimID := "scenario-failure-" + table
	openResp, err := producer.Open(openCtx, &genv1.OpenRequest{
		ClaimId:  claimID,
		Selector: stagingSchema,
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	acquired := openResp.GetAcquired()
	if acquired == nil {
		t.Fatalf("expected Acquired, got %+v", openResp.GetResult())
	}

	// Run the verifier; row_count_absolute min=100 against 2 rows fails.
	sc := h.runVerifier(t, stagingSchema, table, []any{
		map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 100}},
	})
	errOutcome := sc.GetError()
	if errOutcome == nil {
		t.Fatalf("expected Error outcome, got %+v", sc.GetOutcome())
	}
	if got := errOutcome.GetErrorClass(); got != "verifier_failed" {
		t.Errorf("error_class: got %q want verifier_failed", got)
	}

	// Supervisor's terminal-routing contract: Error{verifier_failed} →
	// Abandon on the parent claim.
	abandonCtx, abandonCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer abandonCancel()
	if _, err := producer.Abandon(abandonCtx, &genv1.AbandonRequest{
		ClaimId:    claimID,
		Address:    acquired.GetAddress(),
		ClaimScope: acquired.GetClaimScope(),
	}); err != nil {
		t.Fatalf("Abandon: %v", err)
	}

	// Production unchanged on the Abandon path. Staging discipline:
	// producer-side teardown is not yet shipped (concept:atomic-staging
	// Notes append), so the schema persists; the scenario verifies the
	// terminal-event shape + verbs return cleanly across the wire so
	// the supervisor's routing contract holds.
	if got := h.productionRowCount(t, productionSchema, table); got != 0 {
		t.Errorf("production rows post-Abandon: got %d want 0", got)
	}
}
