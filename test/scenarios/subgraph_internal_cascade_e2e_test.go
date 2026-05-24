// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// S1 must-pass scenario — subgraph_internal_cascade_e2e.
//
// End-to-end coverage of sub-graph internal cascade firing under the
// RunScope-first reshape per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Test coverage matrix / S1":
//
//   - A calling node delegates to a sub-graph.
//   - At calling-node Success terminal, the supervisor creates a
//     sub-graph RunScope (per
//     code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller).
//   - The internal cascade propagates state to the sub-graph's internal
//     nodes (non-entry).
//   - Internal nodes dispatch in the sub-graph RunScope, NOT in the
//     instance's main RunScope.
//
// Pins the sub-graph RunScope creation + internal-dispatch routing
// load-bearing property of the reshape.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	tmplspec "github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestSubgraphInternalCascadeE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Stub scripts: caller (absorbing inner-entry) succeeds, then
	// inner-mid and inner-exit each fire via internal cascade.
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-mid").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-internal-cascade", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
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
						node.TemplateNodeDef{Type: "inner-mid", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*"}},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-mid", Type: "terminal/*"}},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-subgraph-internal-cascade", map[string]any{})

	innerMidNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, innerMidNode, "inner-mid node missing")
	innerExitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, innerExitNode, "inner-exit node missing")

	// Wait for inner-mid + inner-exit to reach fresh — these only
	// dispatch after the caller's Success terminal fires the internal
	// cascade per applyTerminalCompleteSubgraphCaller.
	require.True(t,
		h.WaitForNodeState(innerMidNode.ID, cascade.NodeStateFresh, 30*time.Second),
		"inner-mid must reach fresh via internal cascade")
	require.True(t,
		h.WaitForNodeState(innerExitNode.ID, cascade.NodeStateFresh, 30*time.Second),
		"inner-exit must reach fresh via internal cascade")

	// Sub-graph RunScope created with graph_name = "worker". The
	// internal dispatches live in this RunScope (NOT in the main scope).
	mainScopeID := h.GetMainRunScopeID(iid)
	var subgraphScopes int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_run_scopes
		 WHERE instance_id = $1
		   AND graph_name = 'worker'
		   AND id <> $2
		   AND partition_key = ''
	`, []any{iid, mainScopeID}, &subgraphScopes)
	require.GreaterOrEqual(t, subgraphScopes, 1,
		"applyTerminalCompleteSubgraphCaller must create a sub-graph RunScope")

	// Inner-mid + inner-exit runs live in the sub-graph RunScope.
	for _, internal := range []struct {
		typ    string
		nodeID interface{}
	}{
		{"inner-mid", innerMidNode.ID},
		{"inner-exit", innerExitNode.ID},
	} {
		var inSubgraph int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.node_id = $1
			   AND rs.graph_name = 'worker'
		`, []any{internal.nodeID}, &inSubgraph)
		require.GreaterOrEqual(t, inSubgraph, 1,
			"%s run must live in the sub-graph (graph_name='worker') RunScope", internal.typ)
	}
}
