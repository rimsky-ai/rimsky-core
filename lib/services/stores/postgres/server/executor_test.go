// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		t.Skipf("postgres testcontainer unavailable (requires Docker): %v", err)
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
	outcome, err := ex.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success, got %T", outcome.GetOutcome())
	}
	delta := outcome.GetSuccess().GetAttributesDelta().AsMap()
	if delta["verifier_pass"] != true {
		t.Errorf("expected verifier_pass=true, got %+v", delta)
	}
}

func TestExecutor_RowCountFails(t *testing.T) {
	pool, ex := bootExecutor(t)
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
	outcome, err := ex.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	errOutcome := outcome.GetError()
	if errOutcome == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
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
	outcome, err := ex.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.GetError() == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
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
	outcome, err := ex.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.GetError() == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
}

func TestExecutor_RowCountRatio(t *testing.T) {
	pool, ex := bootExecutor(t)

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
		outcome, err := ex.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if outcome.GetSuccess() == nil {
			t.Fatalf("expected Success for in-bounds row_count_ratio, got %T", outcome.GetOutcome())
		}
	})

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
		outcome, err := ex.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		errOutcome := outcome.GetError()
		if errOutcome == nil {
			t.Fatalf("expected Error for out-of-bounds row_count_ratio, got %T", outcome.GetOutcome())
		}
		if got := errOutcome.GetErrorClass(); got != "pg/verifier_check_failed/row_count_ratio" {
			t.Fatalf("error_class: %q want pg/verifier_check_failed/row_count_ratio", got)
		}
		ratio, ok := ratioFromFailurePayload(errOutcome.GetPayload())
		if !ok {
			t.Fatalf("computed ratio not present in failure payload: %v", errOutcome.GetPayload().AsMap())
		}
		if ratio != 4.0 {
			t.Errorf("ratio = %v, want 4.0", ratio)
		}
	})
}

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
			outcome, err := ex.Execute(context.Background(), &genv1.ExecuteRequest{Attributes: ud})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			errOutcome := outcome.GetError()
			if errOutcome == nil {
				t.Fatalf("expected Error")
			}
			if errOutcome.GetErrorClass() != "pg/attribute_invalid" {
				t.Errorf("error_class: %q want pg/attribute_invalid", errOutcome.GetErrorClass())
			}
		})
	}
}

func TestExecutor_Capabilities(t *testing.T) {
	obs := NewExecutorObservabilityServer()
	caps, err := obs.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	got := caps.GetDeclaredErrorClasses()
	if len(got) == 0 {
		t.Fatal("expected declared_error_classes to be non-empty")
	}
	have := map[string]bool{}
	for _, c := range got {
		have[c] = true
	}
	for _, must := range []string{
		"pg/attribute_invalid",
		"pg/verifier_check_failed/*",
	} {
		if !have[must] {
			t.Errorf("declared_error_classes missing %q (got %v)", must, got)
		}
	}
}
