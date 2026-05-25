// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// S4 must-pass scenario — subgraph_cascade_through_exit_e2e.
//
// End-to-end coverage of cascade-mark-stale walking across the sub-
// graph's exit boundary under the RunScope-first reshape per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Test coverage matrix / S4":
//
//   - A main-graph node `downstream` subscribes to the calling node.
//     The cascade walker MUST reach it after the sub-graph's exit
//     fires the carry-rule and the calling node's state advances.
//
// Pins the load-bearing property: cascade traversal correctly walks
// back through the carry-rule's bridge between the sub-graph's exit
// run (in the sub-graph RunScope) and the calling node's run (in the
// main RunScope), so a main-graph subscriber receives the cascade
// without being skipped.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	tmplspec "github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

func TestSubgraphCascadeThroughExitE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Stub scripts.
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")
	h.Stub.WhenType("downstream").Success(map[string]any{"ok": true}, true, "down")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-cascade-through-exit", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
					),
					// downstream subscribes to the calling node — the
					// cascade walker must reach it after the sub-graph's
					// exit carry-rule transitions the caller's state.
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "downstream", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "caller", Type: "terminal/*"}},
						},
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
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*"}},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-subgraph-cascade-through-exit", map[string]any{})

	exitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, exitNode, "inner-exit node missing")
	downstreamNode := h.FindNode(iid, "downstream")
	require.NotNil(t, downstreamNode, "downstream node missing")

	// Exit must complete first (its terminal fires the carry-rule).
	require.True(t,
		h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh, 30*time.Second),
		"inner-exit must reach fresh — its terminal fires the carry-rule")

	// downstream subscribes to caller; the cascade walker must reach
	// it across the carry-rule's bridge between the sub-graph RunScope
	// (where exit lives) and the main RunScope (where caller +
	// downstream live).
	if !h.WaitForNodeState(downstreamNode.ID, cascade.NodeStateFresh, 60*time.Second) {
		t.Logf("downstream did not reach fresh; dumping current state:")
		h.QuerySQL(`
			SELECT n.node_type, COALESCE(r.state::text, 'fresh'), COALESCE(r.phase::text, 'no-run'),
			       r.run_scope_id::text, rs.graph_name, rs.partition_key, rs.closed_at IS NOT NULL
			  FROM rimsky_nodes n
			  LEFT JOIN LATERAL (
			    SELECT state, phase, run_scope_id
			      FROM rimsky_node_runs
			     WHERE node_id = n.id
			     ORDER BY enqueued_at DESC
			     LIMIT 1
			  ) r ON true
			  LEFT JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE n.instance_id = $1
		`, []any{iid}, func(scan func(...any) error) error {
			var typ, state, phase, scopeID, graphName, pk *string
			var closed bool
			if err := scan(&typ, &state, &phase, &scopeID, &graphName, &pk, &closed); err != nil {
				return err
			}
			t.Logf("  node type=%v state=%v phase=%v scope=%v graph=%v partition=%v closed=%v",
				strDeref(typ), strDeref(state), strDeref(phase), strDeref(scopeID),
				strDeref(graphName), strDeref(pk), closed)
			return nil
		})
		t.Fatalf("downstream must reach fresh via cascade traversal back through the calling node")
	}

	// Downstream's run lives in the MAIN RunScope (not the sub-graph
	// scope). Verifies the cascade walker correctly resolved the
	// receiver's RunScope as main, not as the exit's sub-graph scope.
	mainScopeID := h.GetMainRunScopeID(iid)
	var inMain int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_node_runs
		 WHERE node_id = $1 AND run_scope_id = $2
	`, []any{downstreamNode.ID, mainScopeID}, &inMain)
	require.GreaterOrEqual(t, inMain, 1,
		"downstream's run must live in the main RunScope, not the sub-graph scope")
}
