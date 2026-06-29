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
