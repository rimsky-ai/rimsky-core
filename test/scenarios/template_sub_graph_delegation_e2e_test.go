// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTemplateSubGraphDelegation_SuccessPropagates(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	const innerDelay = 600 * time.Millisecond
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-mid").
		Success(map[string]any{"ok": true}, true, "mid").
		Delay(innerDelay)
	h.Stub.WhenType("inner-exit").
		Success(map[string]any{"done": true}, true, "exit").
		Delay(innerDelay)

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-subgraph-delegation-success", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
					),
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
						node.TemplateNodeDef{Type: "inner-mid", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-mid", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-story-subgraph-delegation-success", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	exitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, exitNode, "inner-exit node missing")
	midNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, midNode, "inner-mid node missing")

	mainScopeID := h.GetMainRunScopeID(iid)
	require.Eventually(t, func() bool {
		var subScopes int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_run_scopes
			 WHERE instance_id = $1
			   AND graph_name = 'worker'
			   AND id <> $2
		`, []any{iid, mainScopeID}, &subScopes)
		return subScopes >= 1
	}, 60*time.Second, 50*time.Millisecond,
		"sub-graph RunScope (graph_name='worker') must be created at the calling-node entry-success terminal "+
			"(applyTerminalCompleteSubgraphCaller's RunScope INSERT)")

	for _, internal := range []struct {
		typ    string
		nodeID shared.UUID
	}{
		{"inner-mid", midNode.ID},
		{"inner-exit", exitNode.ID},
	} {
		require.Eventually(t, func() bool {
			var inSubgraph int
			h.QueryRowSQL(`
				SELECT COUNT(*) FROM rimsky_node_runs r
				  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
				 WHERE r.node_id = $1 AND rs.graph_name = 'worker'
			`, []any{internal.nodeID}, &inSubgraph)
			return inSubgraph >= 1
		}, 60*time.Second, 50*time.Millisecond,
			"%s must run inside the sub-graph RunScope (graph_name='worker') — "+
				"the sub-graph is the delegating node's execution unit", internal.typ)
	}

	heldWitnessed := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var inflightInternals int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE rs.graph_name = 'worker'
			   AND rs.instance_id = $1
			   AND r.phase IN ('pending','active','held','parked')
		`, []any{iid}, &inflightInternals)

		var exitTerminal int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs
			 WHERE node_id = $1
			   AND phase = 'completed'
		`, []any{exitNode.ID}, &exitTerminal)

		if inflightInternals >= 1 && exitTerminal == 0 {
			var callerState string
			h.QueryRowSQL(`
				SELECT COALESCE(state, 'fresh')
				  FROM rimsky_node_runs
				 WHERE node_id = $1
				 ORDER BY enqueued_at DESC
				 LIMIT 1
			`, []any{callerNode.ID}, &callerState)
			require.NotEqual(t, string(cascade.NodeStateFresh), callerState,
				"calling-node run row settled to 'fresh' while a sub-graph internal was still in flight "+
					"(%d in-flight internals) — delegate must NOT settle before the sub-graph does",
				inflightInternals)
			require.NotEqual(t, string(cascade.NodeStateFailed), callerState,
				"calling-node run row settled to 'failed' while a sub-graph internal was still in flight "+
					"(%d in-flight internals) — delegate must NOT settle before the sub-graph does",
				inflightInternals)
			heldWitnessed = true
		}
		if exitTerminal >= 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.True(t, heldWitnessed,
		"never observed the calling node held non-settled while sub-graph internals were in flight; "+
			"the %s per-leaf inner-delay should have created a wide enough window for the 25ms poll. "+
			"If this fails without a delegate-pre-settles bug, widen innerDelay.", innerDelay)

	require.True(t,
		h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"sub-graph exit must reach fresh — its terminal fires the carry-rule "+
			"that propagates the sub-graph's outcome to the calling node")

	require.Eventually(t, func() bool {
		var callerState string
		h.QueryRowSQL(`
			SELECT COALESCE(state, 'fresh')
			  FROM rimsky_node_runs
			 WHERE node_id = $1
			 ORDER BY enqueued_at DESC
			 LIMIT 1
		`, []any{callerNode.ID}, &callerState)
		return callerState == string(cascade.NodeStateFresh)
	}, 60*time.Second, 100*time.Millisecond,
		"calling node's run row must aggregate to 'fresh' after the sub-graph settles — "+
			"sub-graph's success outcome must propagate to the parent via "+
			"runtime/state_propagation.go::walkUpwards under strict aggregation")
}

func TestTemplateSubGraphDelegation_ErrorPropagates(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-mid").Error("subgraph_doom", map[string]any{"why": "internal failure"})
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-subgraph-delegation-error", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
					),
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
						node.TemplateNodeDef{Type: "inner-mid", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-mid", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-story-subgraph-delegation-error", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	midNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, midNode, "inner-mid node missing")
	require.NotNil(t, h.FindNode(iid, "inner-exit"), "inner-exit node missing")

	require.True(t,
		h.WaitForNodeState(midNode.ID, cascade.NodeStateFailed, 60*time.Second),
		"inner-mid must reach NodeStateFailed (default give_up policy on unknown error class)")

	require.Eventually(t, func() bool {
		var callerState string
		h.QueryRowSQL(`
			SELECT COALESCE(state, 'fresh')
			  FROM rimsky_node_runs
			 WHERE node_id = $1
			 ORDER BY enqueued_at DESC
			 LIMIT 1
		`, []any{callerNode.ID}, &callerState)
		return callerState == string(cascade.NodeStateFailed)
	}, 90*time.Second, 100*time.Millisecond,
		"calling node's run row must aggregate to 'failed' — the sub-graph's terminal-error "+
			"outcome must propagate to the parent via strict aggregation "+
			"(runtime/state_propagation.go::walkUpwards + runtime/run_tree.go::aggregateStrict)")

	var callerSettlingSig string
	h.QueryRowSQL(`
		SELECT COALESCE(settling_signal_type, '')
		  FROM rimsky_node_runs
		 WHERE node_id = $1
		 ORDER BY enqueued_at DESC
		 LIMIT 1
	`, []any{callerNode.ID}, &callerSettlingSig)
	require.Equal(t,
		"terminal/error/aggregate/strict_failed", callerSettlingSig,
		"caller run row's settling_signal_type must carry the strict_failed aggregate signal "+
			"(aggregateStrict's projection from inner-mid's failure → parent Failed)")
}
