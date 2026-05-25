// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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

	corestore "github.com/fallguyconsulting/rimsky/foundation/locks"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	pgsstore "github.com/fallguyconsulting/rimsky/stores/postgres/store"
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
		WriteSemantics: corestore.WriteSemanticsStagedAsync,
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
	// Only 2 rows, but min is 100 → fail.
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
	// Two rows with the same id.
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
	// payload is NULL on one row.
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
