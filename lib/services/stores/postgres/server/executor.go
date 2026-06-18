// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/store"
	sqlchecks "github.com/rimsky-ai/rimsky-core/lib/services/stores/shared/sql-checks"
)

// @concept: executor
type ExecutorServer struct {
	genv1.UnimplementedExecutorServer
	store *pgsstore.Store
}

func NewExecutorServer(st *pgsstore.Store) *ExecutorServer {
	return &ExecutorServer{store: st}
}

type ExecutorObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func NewExecutorObservabilityServer() *ExecutorObservabilityServer {
	return &ExecutorObservabilityServer{}
}

func declaredErrorClasses() []string {
	return []string{
		"pg/attribute_invalid",
		"pg/claim_unavailable",
		"pg/connection_lost",
		"pg/swap_failed",
		"pg/verifier_check_failed/*",
	}
}

func (o *ExecutorObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		DeclaredErrorClasses:          declaredErrorClasses(),
	}, nil
}

func (e *ExecutorServer) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return e.executeCore(ctx, req)
}

func (e *ExecutorServer) executeCore(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	ud := req.GetAttributes().AsMap()
	schema, table, specs, err := parseVerifierAttributes(ud)
	if err != nil {
		return verifierErrorOutcome("pg/attribute_invalid", err.Error(), nil), nil
	}
	pool := e.store.Pool()
	if pool == nil {
		// @concept: signal — no live pool is a transient connection-state
		// defect, not an attribute defect, so emit `pg/connection_lost`.
		return verifierErrorOutcome("pg/connection_lost", "postgres store has no live connection pool", nil), nil
	}
	conn := pgxPoolConn{pool: pool}
	results, err := sqlchecks.Run(ctx, conn, schema, table, specs)
	if err != nil {
		return verifierErrorOutcome("pg/attribute_invalid", err.Error(), nil), nil
	}
	if anyFailed(results) {
		failedKind := firstFailedCheckKind(results)
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: "pg/verifier_check_failed/" + failedKind,
			Payload:    buildVerifierFailurePayload(results),
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: buildVerifierSuccessDelta(results),
		Changed:         false,
		ChangeSummary:   fmt.Sprintf("postgres-store verifier: %d checks passed on %s.%s", len(results), schema, table),
	}}}, nil
}

func firstFailedCheckKind(results []sqlchecks.Result) string {
	for _, r := range results {
		if !r.Pass {
			return r.Kind
		}
	}
	return "unknown"
}

func parseVerifierAttributes(ud map[string]any) (string, string, []sqlchecks.CheckSpec, error) {
	schema, _ := ud["schema"].(string)
	if schema == "" {
		return "", "", nil, fmt.Errorf("attributes.schema (string) required")
	}
	table, _ := ud["table"].(string)
	if table == "" {
		return "", "", nil, fmt.Errorf("attributes.table (string) required")
	}
	raw, ok := ud["checks"].([]any)
	if !ok || len(raw) == 0 {
		return "", "", nil, fmt.Errorf("attributes.checks (non-empty array) required")
	}
	out := make([]sqlchecks.CheckSpec, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return "", "", nil, fmt.Errorf("checks[%d] must be an object", i)
		}
		kind, _ := obj["kind"].(string)
		if kind == "" {
			return "", "", nil, fmt.Errorf("checks[%d].kind required", i)
		}
		cfg, _ := obj["config"].(map[string]any)
		out = append(out, sqlchecks.CheckSpec{Kind: kind, Config: cfg})
	}
	return schema, table, out, nil
}

func anyFailed(results []sqlchecks.Result) bool {
	for _, r := range results {
		if !r.Pass {
			return true
		}
	}
	return false
}

func buildVerifierFailurePayload(results []sqlchecks.Result) *structpb.Struct {
	failures := make([]any, 0)
	for _, r := range results {
		if r.Pass {
			continue
		}
		entry := map[string]any{
			"kind":    r.Kind,
			"message": r.Message,
		}
		if len(r.Counts) > 0 {
			entry["counts"] = r.Counts
		}
		failures = append(failures, entry)
	}
	st, _ := structpb.NewStruct(map[string]any{
		"failures": failures,
		"summary":  summarizeVerifier(results),
	})
	return st
}

func buildVerifierSuccessDelta(results []sqlchecks.Result) *structpb.Struct {
	per := make([]any, 0, len(results))
	for _, r := range results {
		entry := map[string]any{"kind": r.Kind, "pass": r.Pass}
		if len(r.Counts) > 0 {
			entry["counts"] = r.Counts
		}
		per = append(per, entry)
	}
	st, _ := structpb.NewStruct(map[string]any{
		"verifier_pass":   true,
		"verifier_checks": float64(len(results)),
		"results":         per,
	})
	return st
}

func summarizeVerifier(results []sqlchecks.Result) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		mark := "OK"
		if !r.Pass {
			mark = "FAIL"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", r.Kind, mark))
	}
	return strings.Join(parts, ", ")
}

func verifierErrorOutcome(class, msg string, payload *structpb.Struct) *genv1.Outcome {
	if payload == nil {
		p, _ := structpb.NewStruct(map[string]any{"message": msg})
		payload = p
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: class, Payload: payload,
	}}}
}

type pgxPoolConn struct {
	pool *pgxpool.Pool
}

func (c pgxPoolConn) Query(ctx context.Context, sql string, args ...any) (sqlchecks.Rows, error) {
	rows, err := c.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{rows: rows}, nil
}

type pgxRows struct {
	rows pgx.Rows
}

func (r *pgxRows) Next() bool             { return r.rows.Next() }
func (r *pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *pgxRows) Close()                 { r.rows.Close() }
func (r *pgxRows) Err() error             { return r.rows.Err() }
