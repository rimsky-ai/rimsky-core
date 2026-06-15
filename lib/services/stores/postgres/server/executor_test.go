// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Tests for the verifier-role executor wired into stores/postgres/.
// Boots a real Postgres via testcontainers and drives executeCore
// directly so the assertions can pin the terminal-event shape and the
// SQL-side semantics. Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6.

package server

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/types/known/structpb"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/store"
)

// bootExecutor stands up a fresh Postgres + pgsstore.Store + ExecutorServer,
// returns the connection so the test can seed staging data. The test
// is responsible for creating the staging schema and table.
func bootExecutor(t *testing.T) (*pgxpool.Pool, *ExecutorServer) {
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

	st, err := pgsstore.New(context.Background(), pgsstore.Config{
		Connection:     dsn,
		WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
	})
	if err != nil {
		t.Fatalf("pgsstore.New: %v", err)
	}
	t.Cleanup(st.Close)
	return pool, NewExecutorServer(st)
}

// seedStagingTable creates a staging schema + items table and inserts
// rows. The verifier role expects schema/table to exist; the producer
// role normally creates them via Open, but for unit scope we seed
// directly.
func seedStagingTable(t *testing.T, pool *pgxpool.Pool, schema, table string, rows []map[string]any) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"CREATE TABLE "+schema+"."+table+" (id TEXT, payload TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, r := range rows {
		id := r["id"]
		payload := r["payload"]
		if _, err := pool.Exec(ctx,
			"INSERT INTO "+schema+"."+table+" (id, payload) VALUES ($1, $2)", id, payload); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
}

// captureSend collects ExecuteEvents emitted by executeCore so the
// test can inspect the terminal verdict.
func captureSend() (func(*genv1.ExecuteEvent) error, *[]*genv1.ExecuteEvent) {
	out := &[]*genv1.ExecuteEvent{}
	return func(ev *genv1.ExecuteEvent) error {
		*out = append(*out, ev)
		return nil
	}, out
}

func TestExecutor_AllChecksPass(t *testing.T) {
	pool, ex := bootExecutor(t)
	seedStagingTable(t, pool, "staging_ok", "items", []map[string]any{
		{"id": "a", "payload": "x"},
		{"id": "b", "payload": "y"},
		{"id": "c", "payload": "z"},
	})

	ud, _ := structpb.NewStruct(map[string]any{
		"schema": "staging_ok",
		"table":  "items",
		"checks": []any{
			map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 1}},
			map[string]any{"kind": "no_nulls", "config": map[string]any{"fields": []any{"id", "payload"}}},
			map[string]any{"kind": "pk_unique", "config": map[string]any{"fields": []any{"id"}}},
		},
	})
	send, out := captureSend()
	if err := ex.executeCore(context.Background(), &genv1.ExecuteRequest{Attributes: ud}, send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	if len(*out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(*out))
	}
	sc := (*out)[0].GetStreamClose()
	if sc == nil {
		t.Fatal("expected StreamClose")
	}
	if sc.GetSuccess() == nil {
		t.Fatalf("expected Success, got %+v", sc.GetOutcome())
	}
}

func TestExecutor_RowCountFails(t *testing.T) {
	pool, ex := bootExecutor(t)
	// @deliberate: seed 2 rows against min=100 so row_count_absolute must fail.
	seedStagingTable(t, pool, "staging_low", "items", []map[string]any{
		{"id": "a", "payload": "x"},
		{"id": "b", "payload": "y"},
	})
	ud, _ := structpb.NewStruct(map[string]any{
		"schema": "staging_low",
		"table":  "items",
		"checks": []any{
			map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 100}},
		},
	})
	send, out := captureSend()
	if err := ex.executeCore(context.Background(), &genv1.ExecuteRequest{Attributes: ud}, send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	sc := (*out)[0].GetStreamClose()
	errOutcome := sc.GetError()
	if errOutcome == nil {
		t.Fatalf("expected Error, got %+v", sc.GetOutcome())
	}
	if errOutcome.GetErrorClass() != "pg/verifier_check_failed/row_count_absolute" {
		t.Errorf("error_class: %q want pg/verifier_check_failed/row_count_absolute", errOutcome.GetErrorClass())
	}
}

func TestExecutor_PKUniqueFails(t *testing.T) {
	pool, ex := bootExecutor(t)
	seedStagingTable(t, pool, "staging_dupe", "items", []map[string]any{
		{"id": "a", "payload": "x"},
		{"id": "a", "payload": "y"},
	})
	ud, _ := structpb.NewStruct(map[string]any{
		"schema": "staging_dupe",
		"table":  "items",
		"checks": []any{
			map[string]any{"kind": "pk_unique", "config": map[string]any{"fields": []any{"id"}}},
		},
	})
	send, out := captureSend()
	if err := ex.executeCore(context.Background(), &genv1.ExecuteRequest{Attributes: ud}, send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	sc := (*out)[0].GetStreamClose()
	if sc.GetError() == nil {
		t.Fatalf("expected Error, got %+v", sc.GetOutcome())
	}
}

func TestExecutor_NoNullsFails(t *testing.T) {
	pool, ex := bootExecutor(t)
	if _, err := pool.Exec(context.Background(), "CREATE SCHEMA staging_nulls"); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(context.Background(), "CREATE TABLE staging_nulls.items (id TEXT, payload TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := pool.Exec(context.Background(), "INSERT INTO staging_nulls.items VALUES ('a', 'x'), ('b', NULL)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	ud, _ := structpb.NewStruct(map[string]any{
		"schema": "staging_nulls",
		"table":  "items",
		"checks": []any{
			map[string]any{"kind": "no_nulls", "config": map[string]any{"fields": []any{"payload"}}},
		},
	})
	send, out := captureSend()
	if err := ex.executeCore(context.Background(), &genv1.ExecuteRequest{Attributes: ud}, send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	sc := (*out)[0].GetStreamClose()
	if sc.GetError() == nil {
		t.Fatalf("expected Error, got %+v", sc.GetOutcome())
	}
}

// TestExecutor_RowCountRatio drives the SQL-store verifier with a
// `row_count_ratio` check — the check kind the in-process verifier
// (verifier-shape-checks) already ships but the SQL substrate does not
// yet compile. The store must run it as an aggregate-only count query,
// compute ratio = row_count / baseline, and partition the terminal:
// Success when the ratio is within [low, high], Error
// (`pg/verifier_check_failed/row_count_ratio`) when it is not, carrying
// the computed ratio in the failure payload so an operator subscribing
// on that class can read the offending number.
//
// RED today: `sqlchecks.Compile` has no `row_count_ratio` arm, so the
// kind falls through to `unknown check kind` → mapped to
// `pg/attribute_invalid` at executor.go. The in-bounds subtest below
// therefore observes an Error (pg/attribute_invalid) where it asserts
// Success, and the out-of-bounds subtest observes pg/attribute_invalid
// where it asserts pg/verifier_check_failed/row_count_ratio with a
// ratio. Both fail until AUTHSTORES-18 adds the compiler arm.
func TestExecutor_RowCountRatio(t *testing.T) {
	pool, ex := bootExecutor(t)

	// @deliberate: 4 rows against baseline 4 yields ratio 1.0, inside
	// [0.5, 2.0], so row_count_ratio must terminate Success.
	t.Run("in_bounds_success", func(t *testing.T) {
		seedStagingTable(t, pool, "staging_ratio_ok", "items", []map[string]any{
			{"id": "a", "payload": "x"},
			{"id": "b", "payload": "y"},
			{"id": "c", "payload": "z"},
			{"id": "d", "payload": "w"},
		})
		ud, _ := structpb.NewStruct(map[string]any{
			"schema": "staging_ratio_ok",
			"table":  "items",
			"checks": []any{
				map[string]any{"kind": "row_count_ratio", "config": map[string]any{
					"baseline": 4, "low": 0.5, "high": 2.0,
				}},
			},
		})
		send, out := captureSend()
		if err := ex.executeCore(context.Background(), &genv1.ExecuteRequest{Attributes: ud}, send); err != nil {
			t.Fatalf("executeCore: %v", err)
		}
		if len(*out) != 1 {
			t.Fatalf("expected 1 event, got %d", len(*out))
		}
		sc := (*out)[0].GetStreamClose()
		if sc == nil {
			t.Fatal("expected StreamClose")
		}
		if sc.GetSuccess() == nil {
			t.Fatalf("expected Success for in-bounds row_count_ratio, got %+v", sc.GetOutcome())
		}
	})

	// @deliberate: 4 rows against baseline 1 yields ratio 4.0, above
	// high=2.0, so row_count_ratio must terminate Error and carry the
	// computed ratio in the failure payload.
	t.Run("out_of_bounds_error", func(t *testing.T) {
		seedStagingTable(t, pool, "staging_ratio_high", "items", []map[string]any{
			{"id": "a", "payload": "x"},
			{"id": "b", "payload": "y"},
			{"id": "c", "payload": "z"},
			{"id": "d", "payload": "w"},
		})
		ud, _ := structpb.NewStruct(map[string]any{
			"schema": "staging_ratio_high",
			"table":  "items",
			"checks": []any{
				map[string]any{"kind": "row_count_ratio", "config": map[string]any{
					"baseline": 1, "low": 0.5, "high": 2.0,
				}},
			},
		})
		send, out := captureSend()
		if err := ex.executeCore(context.Background(), &genv1.ExecuteRequest{Attributes: ud}, send); err != nil {
			t.Fatalf("executeCore: %v", err)
		}
		if len(*out) != 1 {
			t.Fatalf("expected 1 event, got %d", len(*out))
		}
		sc := (*out)[0].GetStreamClose()
		errOutcome := sc.GetError()
		if errOutcome == nil {
			t.Fatalf("expected Error for out-of-bounds row_count_ratio, got %+v", sc.GetOutcome())
		}
		if got := errOutcome.GetErrorClass(); got != "pg/verifier_check_failed/row_count_ratio" {
			t.Fatalf("error_class: %q want pg/verifier_check_failed/row_count_ratio", got)
		}
		// @constraint: the computed ratio must be present in the failure
		// payload so a subscriber on this error_class can read the
		// offending number.
		ratio, ok := ratioFromFailurePayload(errOutcome.GetPayload())
		if !ok {
			t.Fatalf("computed ratio not present in failure payload: %v", errOutcome.GetPayload().AsMap())
		}
		if ratio != 4.0 {
			t.Errorf("ratio = %v, want 4.0", ratio)
		}
	})
}

// ratioFromFailurePayload digs the computed `ratio` out of the verifier
// failure payload shape built by buildVerifierFailurePayload: a top-level
// `failures` array whose entries each carry a `counts` map. Returns the
// first ratio found and whether one was present.
func ratioFromFailurePayload(payload *structpb.Struct) (float64, bool) {
	if payload == nil {
		return 0, false
	}
	m := payload.AsMap()
	failures, ok := m["failures"].([]any)
	if !ok {
		return 0, false
	}
	for _, f := range failures {
		entry, ok := f.(map[string]any)
		if !ok {
			continue
		}
		counts, ok := entry["counts"].(map[string]any)
		if !ok {
			continue
		}
		if r, ok := counts["ratio"].(float64); ok {
			return r, true
		}
	}
	return 0, false
}

func TestExecutor_InvalidAttributes(t *testing.T) {
	_, ex := bootExecutor(t)
	tests := map[string]map[string]any{
		"missing schema": {"table": "items", "checks": []any{
			map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 1}}}},
		"missing table": {"schema": "s", "checks": []any{
			map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": 1}}}},
		"empty checks": {"schema": "s", "table": "t", "checks": []any{}},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			ud, _ := structpb.NewStruct(in)
			send, out := captureSend()
			if err := ex.executeCore(context.Background(), &genv1.ExecuteRequest{Attributes: ud}, send); err != nil {
				t.Fatalf("executeCore: %v", err)
			}
			sc := (*out)[0].GetStreamClose()
			errOutcome := sc.GetError()
			if errOutcome == nil {
				t.Fatalf("expected Error")
			}
			if errOutcome.GetErrorClass() != "pg/attribute_invalid" {
				t.Errorf("error_class: %q want pg/attribute_invalid", errOutcome.GetErrorClass())
			}
		})
	}
}
