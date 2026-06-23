// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: executor-reads-dispatch-context

package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestExecutorReadsDispatchContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	executorEndpoint := harness.StartClaudeAgentFakeOnNetwork(
		ctx, t, netName, "claude-agent-fake-dispatch-context",
		harness.ClaudeAgentFakeOptions{},
	)

	rimskyHandle := harness.BringUpRimskyHandle(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("claude-agent", executorEndpoint),
	)
	ep := rimskyHandle.Endpoint
	t.Cleanup(func() {
		if t.Failed() {
			rimskyHandle.DumpRimskyLogs(t)
		}
	})

	pgPool := connectStatePostgres(ctx, t, ep.HostDSN)
	t.Cleanup(pgPool.Close)

	tid := deployScenarioTemplate(t, ep, buildDispatchContextProbeTemplate())
	iid := createScenarioInstance(t, ep, tid, "ck-executor-reads-dispatch-context")
	workerID := resolveWorkerNodeID(t, ep, iid, "worker")

	postWorkerInvalidate(t, ep, iid, "executor-reads-dispatch-context-1")
	waitForWorkerDispatchCount(t, ctx, pgPool, workerID, 1, 90*time.Second)
	fresh := getWorkerDispatchesInOrder(t, ctx, pgPool, workerID)[0]
	requireFreshDispatchContext(t, fresh)
}

func TestExecutorReadsDispatchContextOnRetryAfterError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	executorEndpoint := harness.StartClaudeAgentFakeOnNetwork(
		ctx, t, netName, "claude-agent-fake-dispatch-context-retry",
		harness.ClaudeAgentFakeOptions{},
	)

	rimskyHandle := harness.BringUpRimskyHandle(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("claude-agent", executorEndpoint),
	)
	ep := rimskyHandle.Endpoint
	t.Cleanup(func() {
		if t.Failed() {
			rimskyHandle.DumpRimskyLogs(t)
		}
	})

	pgPool := connectStatePostgres(ctx, t, ep.HostDSN)
	t.Cleanup(pgPool.Close)

	tid := deployScenarioTemplate(t, ep, buildDispatchContextRetryProbeTemplate())
	iid := createScenarioInstance(t, ep, tid, "ck-executor-reads-dispatch-context-retry")
	workerID := resolveWorkerNodeID(t, ep, iid, "worker")

	postWorkerInvalidate(t, ep, iid, "executor-reads-dispatch-context-retry-1")
	waitForWorkerDispatchCount(t, ctx, pgPool, workerID, 2, 120*time.Second)
	dispatches := getWorkerDispatchesInOrder(t, ctx, pgPool, workerID)
	if len(dispatches) < 2 {
		t.Fatalf("expected at least 2 dispatches (error + retry), got %d", len(dispatches))
	}
	first := dispatches[0]
	retry := dispatches[1]
	requireRetryDispatchContext(t, first, retry)
}

func buildDispatchContextProbeTemplate() map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":             "executor-reads-dispatch-context",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"messages": []map[string]any{
				{
					"type": "operator/worker-rerun",
					"body_schema": map[string]any{
						"type": "object",
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "claude-agent",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"model": map[string]any{
									"type":    "string",
									"default": "claude-sonnet-4-5",
								},
								"system_prompt": map[string]any{
									"type":    "string",
									"default": "you are a dispatch-context-read proof stub. follow the scenario hint in the user prompt verbatim.",
								},
								"user_prompt": map[string]any{
									"type":    "string",
									"default": "scenario:dispatch_context_probe",
								},
								"dispatch_context": map[string]any{
									"type":     "object",
									"readOnly": true,
									"default":  map[string]any{},
								},
								"cli": map[string]any{
									"type":       "object",
									"properties": map[string]any{},
									"default":    map[string]any{},
								},
							},
						},
					},
					"subscribes": []map[string]any{
						{
							"node":                   "operator/worker-rerun",
							"type":                   "terminal/success",
							"force_upstream_refresh": false,
						},
					},
				},
			},
		},
	}
}

func buildDispatchContextRetryProbeTemplate() map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":             "executor-reads-dispatch-context-retry",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"messages": []map[string]any{
				{
					"type": "operator/worker-rerun",
					"body_schema": map[string]any{
						"type": "object",
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "claude-agent",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"model": map[string]any{
									"type":    "string",
									"default": "claude-sonnet-4-5",
								},
								"system_prompt": map[string]any{
									"type":    "string",
									"default": "you are a dispatch-context-read retry proof stub. follow the scenario hint in the user prompt verbatim.",
								},
								"user_prompt": map[string]any{
									"type":    "string",
									"default": "scenario:dispatch_context_retry_probe",
								},
								"dispatch_context": map[string]any{
									"type":     "object",
									"readOnly": true,
									"default":  map[string]any{},
								},
								"cli": map[string]any{
									"type":       "object",
									"properties": map[string]any{},
									"default":    map[string]any{},
								},
							},
						},
					},
					"error_types": map[string]any{
						"stub/forced_retry": map[string]any{
							"policy": []map[string]any{
								{
									"action":        "retry",
									"count":         1,
									"backoff":       "fixed",
									"base_delay_ms": 100,
								},
							},
						},
					},
					"subscribes": []map[string]any{
						{
							"node":                   "operator/worker-rerun",
							"type":                   "terminal/success",
							"force_upstream_refresh": false,
						},
					},
				},
			},
		},
	}
}

func requireRetryDispatchContext(t *testing.T, first, retry workerDispatch) {
	t.Helper()
	retryCtx, ok := retry.attributes["dispatch_context"].(map[string]any)
	if !ok {
		t.Fatalf("retry dispatch %s: latest_attributes missing dispatch_context, got %v", retry.runID, retry.attributes)
	}
	gotDispatchID, _ := retryCtx["dispatch_id"].(string)
	gotRunScopeID, _ := retryCtx["run_scope_id"].(string)
	gotPriorID, _ := retryCtx["prior_dispatch_id"].(string)
	gotDisposition, _ := retryCtx["prior_dispatch_disposition"].(string)
	if gotDispatchID != retry.runID {
		t.Fatalf("retry dispatch %s: dispatch_context.dispatch_id %q does not match the persisted retry run id",
			retry.runID, gotDispatchID)
	}
	if gotRunScopeID != retry.runScopeID {
		t.Fatalf("retry dispatch %s: dispatch_context.run_scope_id %q does not match the persisted run_scope_id %q",
			retry.runID, gotRunScopeID, retry.runScopeID)
	}
	if gotPriorID != first.runID {
		t.Fatalf("retry dispatch %s: prior_dispatch_id %q does not match first dispatch run id %q",
			retry.runID, gotPriorID, first.runID)
	}
	if gotDisposition != "retry_after_error" {
		t.Fatalf("retry dispatch %s: prior_dispatch_disposition = %q, want %q",
			retry.runID, gotDisposition, "retry_after_error")
	}
}

func requireFreshDispatchContext(t *testing.T, d workerDispatch) {
	t.Helper()
	got, ok := d.attributes["dispatch_context"].(map[string]any)
	if !ok {
		t.Fatalf("dispatch %s: latest_attributes missing dispatch_context, got %v", d.runID, d.attributes)
	}
	gotDispatchID, _ := got["dispatch_id"].(string)
	gotRunScopeID, _ := got["run_scope_id"].(string)
	if gotDispatchID == "" {
		t.Fatalf("dispatch %s: dispatch_id empty in dispatch_context: %v", d.runID, got)
	}
	if gotRunScopeID == "" {
		t.Fatalf("dispatch %s: run_scope_id empty in dispatch_context: %v", d.runID, got)
	}
	if got["prior_dispatch_id"] != nil {
		t.Fatalf("dispatch %s: expected prior_dispatch_id nil on fresh dispatch, got %v", d.runID, got["prior_dispatch_id"])
	}
	if got["prior_dispatch_disposition"] != nil {
		t.Fatalf("dispatch %s: expected prior_dispatch_disposition nil on fresh dispatch, got %v", d.runID, got["prior_dispatch_disposition"])
	}
	if gotDispatchID != d.runID {
		t.Fatalf("dispatch %s: dispatch_context.dispatch_id %q does not match the persisted run id (the wire field is supposed to round-trip exactly)",
			d.runID, gotDispatchID)
	}
	if gotRunScopeID != d.runScopeID {
		t.Fatalf("dispatch %s: dispatch_context.run_scope_id %q does not match the persisted run_scope_id %q",
			d.runID, gotRunScopeID, d.runScopeID)
	}
}
