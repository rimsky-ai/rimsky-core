// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins the L5 `graph:` matcher key end-to-end against a
// sub-graph delegation. The flat-template counterpart at
// `attribute_overrides_match_overlay_flat_template_graph_resolution_e2e_test.go`
// pins the acquisition-time fallback; THIS scenario exercises actual
// sub-graph routing — a `graph: "main"` matcher must fire for
// outer-graph dispatches (including the entry-absorbed calling node,
// which reports `graph: "main"` per concept:delegation) but must NOT
// fire for sub-graph internal dispatches; symmetrically, a
// `graph: "worker"` matcher must fire only on sub-graph internal
// dispatches.
//
// What this pins (per the matcher-overlay design):
//   - `runtime/runner_acquire.go::lookupGraphName` correctly resolves
//     the dispatch-time graph name for both outer and inner runs.
//   - The entry-absorbed disposition: even though the calling node
//     delegates to `worker`, the calling node lives in `main`, so a
//     matcher targeting `graph: "worker"` does NOT fire on the
//     calling node's own dispatch (the dispatch is the absorbed
//     entry's executor running under the calling node's identity).
//   - The per-entry match counters advance correctly across multiple
//     dispatches in a single instance.
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAttributeOverridesMatchOverlaySubgraph_GraphMatcherRoutesByDispatchGraph(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cli": map[string]any{"type": "object"},
			"ok":  map[string]any{"type": "boolean", "readOnly": true},
		},
	})
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-graph-subgraph", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					// @deliberate: caller's executor is absorbed from inner-entry (the
					// worker sub-graph's entry). Declaring no executor
					// here exercises the absorption merge.
					{Type: "caller", Delegate: "worker"},
				},
			},
			{
				Name:  "worker",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-entry", Executor: "stub"},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{
							Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
				},
			},
		},
	})

	overrides := map[string]any{
		"by_match": []any{
			map[string]any{
				"matcher": map[string]any{"graph": "main"},
				"overlay": map[string]any{"cli": map[string]any{"where": "outer"}},
			},
			map[string]any{
				"matcher": map[string]any{"graph": "worker"},
				"overlay": map[string]any{"cli": map[string]any{"where": "inner"}},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-subgraph", map[string]any{}, overrides)

	// @deliberate: Wait for both dispatches to reach fresh — the inner-exit can
	// only fire after the caller's parent run terminates successfully
	// (per the sub-graph internal-cascade rule).
	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing from instance")
	innerExitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, innerExitNode, "inner-exit node missing from instance")

	// @deliberate: The caller's dispatch runs the absorbed inner-entry executor.
	// Stub records it under NodeType="caller" (the row's own type).
	require.True(t,
		waitForObservedAttrs(h, "caller", 15*time.Second) != nil,
		"caller dispatch not observed by stub")
	callerAttrs := waitForObservedAttrs(h, "caller", 100*time.Millisecond)
	require.NotNil(t, callerAttrs, "caller attributes missing from stub observation")
	callerCLI, ok := callerAttrs["cli"].(map[string]any)
	require.True(t, ok, "caller attributes.cli missing or wrong shape: %#v", callerAttrs)
	require.Equal(t, "outer", callerCLI["where"],
		"caller lives in graph=main per concept:delegation; matcher graph=main MUST fire and NOT graph=worker")

	// @constraint: inner-exit lives in the worker sub-graph; matcher graph=worker
	// fires. The graph=main matcher must NOT fire on this dispatch.
	require.True(t,
		waitForObservedAttrs(h, "inner-exit", 15*time.Second) != nil,
		"inner-exit dispatch not observed by stub")
	innerAttrs := waitForObservedAttrs(h, "inner-exit", 100*time.Millisecond)
	require.NotNil(t, innerAttrs, "inner-exit attributes missing from stub observation")
	innerCLI, ok := innerAttrs["cli"].(map[string]any)
	require.True(t, ok, "inner-exit attributes.cli missing or wrong shape: %#v", innerAttrs)
	require.Equal(t, "inner", innerCLI["where"],
		"inner-exit lives in graph=worker; matcher graph=worker MUST fire")

	// @constraint: Counter assertion: two by_match entries, both fire exactly once
	// (matcher[0] fires on caller; matcher[1] fires on inner-exit).
	// inner-entry is absorbed into caller and never dispatches as a
	// standalone child, so it contributes no additional counter
	// increments.
	require.Eventually(t, func() bool {
		var inst *persistence.InstanceRow
		err := h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.Instances().Get(ctx, iid, tx)
			inst = r
			return err
		})
		if err != nil || inst == nil {
			return false
		}
		c := inst.AttributeOverridesMatchCounts
		return len(c) == 2 && c[0] == 1 && c[1] == 1
	}, 10*time.Second, 50*time.Millisecond,
		"AttributeOverridesMatchCounts mismatch (want [1, 1])")
}

// waitForObservedAttrs polls the stub's Observed log for the first
// dispatch matching nodeType, returning the captured attribute bag or
// nil on timeout. Used because the scenario harness drives dispatches
// asynchronously through the supervisor + frame scheduler.
func waitForObservedAttrs(h *scenario.Harness, nodeType string, timeout time.Duration) map[string]any {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, o := range h.Stub.Observed() {
			if o.NodeType == nodeType {
				return o.Attributes
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}
