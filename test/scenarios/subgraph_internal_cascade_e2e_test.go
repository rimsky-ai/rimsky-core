// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSubgraphInternalCascadeE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

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
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-mid", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
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

	h.WaitForNodeState(innerMidNode.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(innerExitNode.ID, cascade.NodeStateFresh)

	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
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
