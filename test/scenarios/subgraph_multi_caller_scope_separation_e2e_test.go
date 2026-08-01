// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func TestSubgraphMultiCaller_SharedInternalsScopeSeparated(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("caller-a").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("caller-b").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-mid").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-multi-caller-scope-separation", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					{Type: "caller-a", Delegate: "worker"},
					{Type: "caller-b", Delegate: "worker"},
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
	iid := h.CreateInstance(tid, "ck-subgraph-multi-caller-scope-separation", map[string]any{})

	callerA := h.FindNode(iid, "caller-a")
	callerB := h.FindNode(iid, "caller-b")
	innerExitNode := h.FindNode(iid, "inner-exit")
	innerMidNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, callerA, "caller-a node missing")
	require.NotNil(t, callerB, "caller-b node missing")
	require.NotNil(t, innerExitNode, "inner-exit node missing")
	require.NotNil(t, innerMidNode, "inner-mid node missing")

	var innerExitNodeRows, innerMidNodeRows int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_nodes WHERE instance_id = $1 AND node_type = $2`,
		[]any{iid, "inner-exit"}, &innerExitNodeRows)
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_nodes WHERE instance_id = $1 AND node_type = $2`,
		[]any{iid, "inner-mid"}, &innerMidNodeRows)
	require.Equal(t, 1, innerExitNodeRows,
		"the sub-graph's internal node types must be flattened to exactly one rimsky_nodes row per "+
			"instance regardless of how many callers delegate to the sub-graph — the row is shared, "+
			"not duplicated per caller")
	require.Equal(t, 1, innerMidNodeRows,
		"the sub-graph's internal node types must be flattened to exactly one rimsky_nodes row per "+
			"instance regardless of how many callers delegate to the sub-graph — the row is shared, "+
			"not duplicated per caller")

	waitForLatestRunState(t, h, callerA.ID, cascade.NodeStateFresh)
	waitForLatestRunState(t, h, callerB.ID, cascade.NodeStateFresh)

	waitForRunCount(t, h, innerExitNode.ID, "fresh", 2)
	waitForRunCount(t, h, innerMidNode.ID, "fresh", 2)

	var callerARunID, callerBRunID shared.UUID
	h.QueryRowSQL(`
		SELECT id FROM rimsky_node_runs
		 WHERE node_id = $1 AND state = 'fresh'
		 ORDER BY enqueued_at DESC LIMIT 1`,
		[]any{callerA.ID}, &callerARunID)
	h.QueryRowSQL(`
		SELECT id FROM rimsky_node_runs
		 WHERE node_id = $1 AND state = 'fresh'
		 ORDER BY enqueued_at DESC LIMIT 1`,
		[]any{callerB.ID}, &callerBRunID)
	require.NotEqual(t, callerARunID, callerBRunID)

	var scopeCountForA, scopeCountForB int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND graph_name = 'worker' AND parent_run_id = $2`,
		[]any{iid, callerARunID}, &scopeCountForA)
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND graph_name = 'worker' AND parent_run_id = $2`,
		[]any{iid, callerBRunID}, &scopeCountForB)
	require.Equal(t, 1, scopeCountForA,
		"caller-a's invocation of the shared 'worker' sub-graph must have created its own RunScope, "+
			"parented by caller-a's own node_run_id")
	require.Equal(t, 1, scopeCountForB,
		"caller-b's invocation of the shared 'worker' sub-graph must have created its own RunScope, "+
			"parented by caller-b's own node_run_id, separate from caller-a's")

	var distinctScopesForExit int
	h.QueryRowSQL(`
		SELECT COUNT(DISTINCT r.run_scope_id) FROM rimsky_node_runs r
		 WHERE r.node_id = $1 AND r.state = 'fresh'`,
		[]any{innerExitNode.ID}, &distinctScopesForExit)
	require.Equal(t, 2, distinctScopesForExit,
		"inner-exit's two terminal runs (one per caller invocation) must sit in two DISTINCT RunScopes — "+
			"per-invocation scope separation over the shared internal node row, not one run reused or "+
			"merged across both callers")
}

func waitForRunCount(t *testing.T, h *scenario.Harness, nodeID shared.UUID, state string, want int) {
	t.Helper()
	for {
		var n int
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1 AND state = $2`,
			[]any{nodeID, state}, &n)
		if n >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
