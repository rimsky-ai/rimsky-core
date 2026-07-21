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

	holdMid := make(chan struct{})
	holdExit := make(chan struct{})
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-mid").
		Success(map[string]any{"ok": true}, true, "mid").
		HoldUntil(holdMid)
	h.Stub.WhenType("inner-exit").
		Success(map[string]any{"done": true}, true, "exit").
		HoldUntil(holdExit)

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
								{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-mid", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
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
	entryNode := h.FindNode(iid, "inner-entry")
	require.NotNil(t, entryNode, "inner-entry node missing")

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

	callerState := func() string {
		var state string
		h.QueryRowSQL(`
			SELECT COALESCE(state, 'fresh')
			  FROM rimsky_node_runs
			 WHERE node_id = $1
			 ORDER BY enqueued_at DESC
			 LIMIT 1
		`, []any{callerNode.ID}, &state)
		return state
	}
	requireCallerHeld := func(reason string) {
		state := callerState()
		require.NotEqual(t, string(cascade.NodeStateFresh), state,
			"calling-node run row settled to 'fresh' while %s — delegate must NOT settle before the sub-graph does", reason)
		require.NotEqual(t, string(cascade.NodeStateFailed), state,
			"calling-node run row settled to 'failed' while %s — delegate must NOT settle before the sub-graph does", reason)
	}
	nodeInFlight := func(nodeID shared.UUID) bool {
		var n int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs
			 WHERE node_id = $1 AND state IN ('pending','stale','running','held','parked')
		`, []any{nodeID}, &n)
		return n >= 1
	}

	require.Eventually(t, func() bool { return nodeInFlight(midNode.ID) },
		60*time.Second, 25*time.Millisecond, "inner-mid never reached an in-flight run state")
	requireCallerHeld("inner-mid is held in flight")
	close(holdMid)

	h.WaitForNodeState(midNode.ID, cascade.NodeStateFresh)

	require.Eventually(t, func() bool { return nodeInFlight(exitNode.ID) },
		60*time.Second, 25*time.Millisecond, "inner-exit never reached an in-flight run state")
	requireCallerHeld("inner-exit is held in flight")
	close(holdExit)

	h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh)

	var entryRuns int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1`, []any{entryNode.ID}, &entryRuns)
	require.Zero(t, entryRuns,
		"inner-entry must never itself dispatch — its Executor is absorbed into the delegating caller "+
			"node at canonicalization, so the sub-graph's entry terminal is synthesized from the "+
			"caller's own run rather than a separate execution")

	require.Eventually(t, func() bool {
		return callerState() == string(cascade.NodeStateFresh)
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
								{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-mid", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
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

	h.WaitForNodeState(midNode.ID, cascade.NodeStateFailed)

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
