// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Atomic-staging Commit/Abandon end-to-end against the fused
// `pkg:stores/postgres/`. The fused store serves both
// `concept:claim-producer` (Open / Commit / Abandon) and
// `concept:executor` (the SQL-substrate verifier role) on the same
// gRPC endpoint per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6.
//
// Coverage gap (per the reviewer's note on the original plan task 6.6):
// the substrate-level executor tests at
// `pkg:stores/postgres/server/executor_test.go` pin the SQL semantics
// of the verifier role, and `pg_verifier_test.go` pins the terminal-
// event SHAPE. This scenario closes the wire end of the loop:
//
//  1. Boot a fused-store gRPC server (lib/services
//     `stores/postgres/server.Run` directly, ClaimProducer + Executor
//     on one endpoint) against a real testcontainers Postgres.
//  2. Pre-create the CANONICAL schema named by the claim selector (the
//     operator's target view). It starts empty — the atomic swap renames
//     the per-claim staging schema into it.
//  3. Open the claim against the canonical selector. The staged_async
//     producer reserves a PER-CLAIM staging schema and returns it as the
//     claim Address (distinct from the canonical ClaimScope).
//  4. Write the produced rows into the staging schema (the Address) and
//     drive the verifier role against that staging data with
//     `ExecuteRequest`; assert Success on all-checks-pass and Error on
//     any-check-fail (hierarchical `pg/verifier_check_failed/<kind>`).
//  5. Drive the producer-side Commit / Abandon. Commit performs the
//     atomic schema swap (canonical reflects the staged rows; staging
//     gone); Abandon discards the staging schema and leaves the canonical
//     untouched — the producer-side staging-schema lifecycle shipped by
//     spec:2026-06-06-comprehensive-gap-closure (story
//     S-pgstore-atomic-staging-substrate). The test pins the swap end to
//     end across the wire so the supervisor's terminal-routing contract
//     holds.
//
// The pre-2026-05-24 version of this test imported
// `pkg:foundation/persistence`, `pkg:internal/pgtest`, and the
// in-rimsky `pkg:stores/postgres/testfixture` — all now unreachable
// from lib/services. The rewrite uses `harness.StartFreshPostgres`
// for the postgres testcontainer, a direct pgxpool for SQL seeding /
// inspection (the white-box DB access is the test's own opinion, not
// rimsky's), and `stores/postgres/server.Run` to boot the fused
// binary in-process.
package atomicstaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	pgstoreserver "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/server"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// fusedHarness holds the per-test state for the fused pg-store
// scenarios: a real postgres DSN, a pgx pool for SQL fixtures, and a
// gRPC client connection to the fused binary.
type fusedHarness struct {
	pool *pgxpool.Pool
	conn *grpc.ClientConn
}

func bootFusedStore(t *testing.T) *fusedHarness {
	t.Helper()
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	endpoint, teardown := startPgStore(t, dsn, true)
	t.Cleanup(teardown)

	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &fusedHarness{pool: pool, conn: conn}
}

// startPgStore boots `stores/postgres/server.Run` in-process bound to
// loopback. Returns the gRPC dial address and a teardown.
func startPgStore(t *testing.T, dsn string, enableExecutor bool) (grpcAddr string, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pg store grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("pg store http listen: %v", err)
	}
	addr := grpcLis.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = pgstoreserver.Run(ctx, pgstoreserver.Config{
			Connection:     dsn,
			WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
			EnableExecutor: enableExecutor,
		}, grpcLis, httpLis, nil)
		close(done)
	}()
	return addr, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

// createCanonicalSchema creates the empty canonical schema the claim
// selector names — the operator's target view the atomic swap renames
// the staging schema into. Empty at cutover time is the substrate's
// contract (the swap's RESTRICT drop of the canonical refuses to
// silently clobber a populated/depended-upon canonical).
func (h *fusedHarness) createCanonicalSchema(t *testing.T, canonical string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		"CREATE SCHEMA IF NOT EXISTS "+canonical); err != nil {
		t.Fatalf("CREATE SCHEMA %s: %v", canonical, err)
	}
}

// writeStagedRows materializes the executor's produced rows into the
// per-claim staging schema (the claim Address). This stands in for the
// real executor's data-path writes against the address it was handed.
func (h *fusedHarness) writeStagedRows(t *testing.T, staging, table string, rows []map[string]any) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx,
		"CREATE TABLE "+staging+"."+table+" (id TEXT, payload TEXT)",
	); err != nil {
		t.Fatalf("CREATE TABLE staging: %v", err)
	}
	for _, r := range rows {
		if _, err := h.pool.Exec(ctx,
			"INSERT INTO "+staging+"."+table+" (id, payload) VALUES ($1, $2)",
			r["id"], r["payload"],
		); err != nil {
			t.Fatalf("INSERT staging: %v", err)
		}
	}
}

func (h *fusedHarness) schemaRowCount(t *testing.T, schema, table string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM "+schema+"."+table,
	).Scan(&n); err != nil {
		t.Fatalf("count %s.%s: %v", schema, table, err)
	}
	return n
}

// decodeAddressSchema decodes the Acquired.Address (a JSON-string of the
// per-claim staging schema name) the staged_async producer returns at
// Open.
func decodeAddressSchema(t *testing.T, addr []byte) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(addr, &s); err != nil {
		t.Fatalf("decode Address %q as schema name: %v", string(addr), err)
	}
	if s == "" {
		t.Fatalf("Open returned an empty Address")
	}
	return s
}

func (h *fusedHarness) stagingSchemaExists(t *testing.T, schema string) bool {
	t.Helper()
	var exists sql.NullBool
	if err := h.pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
		schema,
	).Scan(&exists); err != nil {
		t.Fatalf("schema exists: %v", err)
	}
	return exists.Valid && exists.Bool
}

// runVerifier dispatches an ExecuteRequest against the fused store's
// Executor role and returns the StreamClose event.
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
// end-to-end. The staged_async producer reserves a per-claim staging
// schema at Open; the verifier passes on the staged rows; the
// supervisor's terminal-routing contract calls Commit, which performs
// the atomic schema swap — the canonical (selector) schema reflects the
// staged rows and the staging schema is gone.
func TestAtomicStaging_VerifierSuccess_DrivesCommit(t *testing.T) {
	t.Parallel()
	h := bootFusedStore(t)
	const canonicalSchema, table = "production_e2e_ok", "items"
	h.createCanonicalSchema(t, canonicalSchema)

	producer := genv1.NewClaimProducerClient(h.conn)
	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer openCancel()
	claimID := "scenario-success-" + table
	openResp, err := producer.Open(openCtx, &genv1.OpenRequest{
		ClaimId:  claimID,
		Selector: canonicalSchema,
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	acquired := openResp.GetAcquired()
	if acquired == nil {
		t.Fatalf("expected Acquired, got %+v", openResp.GetResult())
	}
	staging := decodeAddressSchema(t, acquired.GetAddress())
	if staging == canonicalSchema {
		t.Fatalf("Open returned the canonical schema as the write target; expected a distinct staging schema")
	}
	if !h.stagingSchemaExists(t, staging) {
		t.Fatalf("Open did not reserve staging schema %q", staging)
	}

	// The executor writes its produced rows into the staging schema.
	h.writeStagedRows(t, staging, table, []map[string]any{
		{"id": "a", "payload": "x"},
		{"id": "b", "payload": "y"},
		{"id": "c", "payload": "z"},
	})

	sc := h.runVerifier(t, staging, table, []any{
		map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 1}},
		map[string]any{"kind": "no_nulls", "config": map[string]any{"fields": []any{"id", "payload"}}},
		map[string]any{"kind": "pk_unique", "config": map[string]any{"fields": []any{"id"}}},
	})
	if sc.GetSuccess() == nil {
		t.Fatalf("expected Success, got %+v", sc.GetOutcome())
	}

	commitCtx, commitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer commitCancel()
	if _, err := producer.Commit(commitCtx, &genv1.CommitRequest{
		ClaimId:    claimID,
		Address:    acquired.GetAddress(),
		ClaimScope: acquired.GetClaimScope(),
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Atomic swap landed the staged rows into the canonical view...
	if got := h.schemaRowCount(t, canonicalSchema, table); got != 3 {
		t.Errorf("canonical rows post-Commit: got %d want 3 (atomic swap did not land staged rows)", got)
	}
	// ...and consumed the staging schema (no orphaned staging).
	if h.stagingSchemaExists(t, staging) {
		t.Errorf("staging schema %q still exists after Commit; the swap must consume it", staging)
	}
}

// TestAtomicStaging_VerifierFailure_DrivesAbandon pins the failure
// path. Any check fails → verifier emits Error with a hierarchical
// `pg/verifier_check_failed/<kind>` class → supervisor calls Abandon,
// which discards the staging schema and leaves the canonical untouched.
func TestAtomicStaging_VerifierFailure_DrivesAbandon(t *testing.T) {
	t.Parallel()
	h := bootFusedStore(t)
	const canonicalSchema, table = "production_e2e_fail", "items"
	h.createCanonicalSchema(t, canonicalSchema)
	// Pre-seed a canonical row the Abandon must NOT touch.
	if _, err := h.pool.Exec(context.Background(),
		"CREATE TABLE "+canonicalSchema+"."+table+" (id TEXT, payload TEXT)"); err != nil {
		t.Fatalf("CREATE TABLE canonical: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		"INSERT INTO "+canonicalSchema+"."+table+" (id, payload) VALUES ('keep','me')"); err != nil {
		t.Fatalf("seed canonical row: %v", err)
	}

	producer := genv1.NewClaimProducerClient(h.conn)
	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer openCancel()
	claimID := "scenario-failure-" + table
	openResp, err := producer.Open(openCtx, &genv1.OpenRequest{
		ClaimId:  claimID,
		Selector: canonicalSchema,
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	acquired := openResp.GetAcquired()
	if acquired == nil {
		t.Fatalf("expected Acquired, got %+v", openResp.GetResult())
	}
	staging := decodeAddressSchema(t, acquired.GetAddress())
	h.writeStagedRows(t, staging, table, []map[string]any{
		{"id": "a", "payload": "x"},
		{"id": "b", "payload": "y"},
	})

	sc := h.runVerifier(t, staging, table, []any{
		map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 100}},
	})
	errOutcome := sc.GetError()
	if errOutcome == nil {
		t.Fatalf("expected Error outcome, got %+v", sc.GetOutcome())
	}
	// Post-2026-05-23 signal-taxonomy reshape (Pass 6): the verifier
	// emits hierarchical `pg/verifier_check_failed/<kind>` classes,
	// not the flat `verifier_failed` of the original test. The
	// semantic the supervisor's terminal routing rides on is
	// "Error outcome with a non-empty error_class"; the prefix
	// pins the executor identity per `concept:signal`.
	const wantPrefix = "pg/verifier_check_failed/"
	if got := errOutcome.GetErrorClass(); got == "" || got[:len(wantPrefix)] != wantPrefix {
		t.Errorf("error_class: got %q want prefix %q", got, wantPrefix)
	}

	abandonCtx, abandonCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer abandonCancel()
	if _, err := producer.Abandon(abandonCtx, &genv1.AbandonRequest{
		ClaimId:    claimID,
		Address:    acquired.GetAddress(),
		ClaimScope: acquired.GetClaimScope(),
	}); err != nil {
		t.Fatalf("Abandon: %v", err)
	}

	// Staging discarded; canonical untouched (still the one pre-seeded row).
	if h.stagingSchemaExists(t, staging) {
		t.Errorf("staging schema %q still exists after Abandon; it must be dropped", staging)
	}
	if got := h.schemaRowCount(t, canonicalSchema, table); got != 1 {
		t.Errorf("canonical rows post-Abandon: got %d want 1 (canonical must be unchanged)", got)
	}
}

// _ guards unused-import.
var _ = fmt.Sprintf
