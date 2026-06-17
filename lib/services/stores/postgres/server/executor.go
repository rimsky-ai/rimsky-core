// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// ExecutorServer adapts the postgres store's connection pool to the
// rimsky Executor protocol. Attribute shape (verifier role):
//
//	{
//	  "schema": "<schema-name>",
//	  "table":  "<table-name>",
//	  "checks": [{kind, config}, ...]
//	}
//
// Schema is typically the staging-claim address resolved via
// `{{claim.<alias>.address}}` substitution at dispatch time. The
// executor never reads row data; only counts and existence.
//
// Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6.

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

// ExecutorServer implements proto:executor.proto::Executor for the
// postgres store's verifier role.
//
// @concept: executor
type ExecutorServer struct {
	genv1.UnimplementedExecutorServer
	store *pgsstore.Store
}

// NewExecutorServer constructs an ExecutorServer wired to the store's
// connection pool.
func NewExecutorServer(st *pgsstore.Store) *ExecutorServer {
	return &ExecutorServer{store: st}
}

// ExecutorObservabilityServer is the postgres-store verifier
// executor's ExecutorObservability handshake surface. The store-side
// observability surface (ClaimProducerObservability) is separate and
// scoped to claim-producer activity; this surface advertises the
// executor's hierarchical error-class vocabulary per `concept:signal`
// so the operator's `error_types:` keys can be range-checked against
// it at template registration.
type ExecutorObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// NewExecutorObservabilityServer constructs the handshake surface.
func NewExecutorObservabilityServer() *ExecutorObservabilityServer {
	return &ExecutorObservabilityServer{}
}

// declaredErrorClasses is the hierarchical error vocabulary the
// verifier executor advertises. Entries ending in `*` are prefix
// patterns; exact strings are fixed leaves. Per `concept:signal`
// hierarchical error_class rule.
func declaredErrorClasses() []string {
	return []string{
		"pg/attribute_invalid",
		"pg/claim_unavailable",
		"pg/connection_lost",
		"pg/swap_failed",
		"pg/verifier_check_failed/*",
	}
}

// Capabilities reports the executor handshake. Trace get/stream are
// not implemented by the verifier executor (it has no per-dispatch
// trace ledger); the handshake exists primarily to surface the
// hierarchical error-class vocabulary.
func (o *ExecutorObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		DeclaredErrorClasses:          declaredErrorClasses(),
	}, nil
}

// Execute is the gRPC entrypoint. Per TD-execute-rpc-unary the RPC
// returns the settling Outcome directly; the verifier never carries
// async work so it always returns a Success or Error variant.
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
			// @constraint: per-check-kind leaf under the
			// `pg/verifier_check_failed/*` prefix so subscribers can match
			// the prefix for any verifier failure or pin to a specific
			// check kind by leaf — the hierarchical-error-class pattern the
			// signal taxonomy applies across all bundled executors.
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

// firstFailedCheckKind returns the kind of the first failing check
// result in scan order. Used to construct the per-check-kind error
// class leaf for `pg/verifier_check_failed/<kind>`. Callers only
// invoke this when at least one result is failing.
func firstFailedCheckKind(results []sqlchecks.Result) string {
	for _, r := range results {
		if !r.Pass {
			return r.Kind
		}
	}
	return "unknown"
}

// parseVerifierAttributes extracts and validates the attribute fields.
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

// anyFailed reports whether any check Result has Pass=false.
func anyFailed(results []sqlchecks.Result) bool {
	for _, r := range results {
		if !r.Pass {
			return true
		}
	}
	return false
}

// buildVerifierFailurePayload aggregates failed results into the
// Error.payload Struct surfaced upstream.
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

// buildVerifierSuccessDelta carries the per-check counts on a passing
// terminal so operators can see the aggregate numbers the verifier saw.
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

// verifierErrorOutcome builds a one-shot Error outcome; payload may be nil.
func verifierErrorOutcome(class, msg string, payload *structpb.Struct) *genv1.Outcome {
	if payload == nil {
		p, _ := structpb.NewStruct(map[string]any{"message": msg})
		payload = p
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: class, Payload: payload,
	}}}
}

// pgxPoolConn adapts a pgxpool.Pool to the sqlchecks.Conn interface.
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

// pgxRows adapts pgx.Rows to sqlchecks.Rows.
type pgxRows struct {
	rows pgx.Rows
}

func (r *pgxRows) Next() bool             { return r.rows.Next() }
func (r *pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *pgxRows) Close()                 { r.rows.Close() }
func (r *pgxRows) Err() error             { return r.rows.Err() }
