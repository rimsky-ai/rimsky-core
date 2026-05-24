// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// S2 must-pass scenario — subgraph_exit_carry_e2e.
//
// End-to-end coverage of the sub-graph exit writeback carry-rule under
// the RunScope-first reshape per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Test coverage matrix / S2":
//
//   - A sub-graph runs end-to-end: caller → internal cascade → exit.
//   - At exit's terminal, the carry-rule fires per
//     code:runtime/subgraph_dispatch.go::CarryExitWriteback — exit's
//     attributes_delta is copied to the calling node's attributes row.
//   - The sub-graph RunScope is closed at the same tx (closed_at IS
//     NOT NULL).
//
// Pins the sub-graph RunScope closure semantics + writeback-carry
// load-bearing property of the reshape.
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	tmplspec "github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestSubgraphExitCarryE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Stub: caller (entry-absorbed) succeeds. Exit returns a Success
	// with attributes_delta = {result: "subgraph-done"} — the carry-
	// rule copies this onto the calling node's attribute row.
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-exit").Success(map[string]any{"result": "subgraph-done"}, true, "exit-out")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":     map[string]any{"type": "boolean", "readOnly": true},
			"result": map[string]any{"type": "string"},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-exit-carry", Version: "1",
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
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*"}},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-subgraph-exit-carry", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	exitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, exitNode, "inner-exit node missing")

	// Wait for the sub-graph exit to reach fresh — its terminal is the
	// trigger for the carry-rule. The calling node's leaf-run stays in
	// `running` state until the carry-rule's parent-state transition
	// fires; that aggregation is the witness for the carry-rule.
	require.True(t,
		h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh, 30*time.Second),
		"inner-exit must reach fresh")

	// Sub-graph RunScope is closed in the same tx as exit's terminal
	// (carry-rule's closure semantics per Task 36).
	require.Eventually(t, func() bool {
		var closed int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_run_scopes
			 WHERE instance_id = $1
			   AND graph_name = 'worker'
			   AND closed_at IS NOT NULL
		`, []any{iid}, &closed)
		return closed >= 1
	}, 30*time.Second, 100*time.Millisecond,
		"sub-graph RunScope (graph_name='worker') must close after exit terminates")

	// Carry-rule witness: the calling node's attributes row carries
	// the exit's attributes_delta. Verified via the node-attributes
	// accessor (scoped on the main RunScope since the caller lives
	// there).
	mainScopeID := h.GetMainRunScopeID(iid)
	require.Eventually(t, func() bool {
		var row *persistence.NodeAttributesRow
		err := h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(ctx, callerNode.ID, mainScopeID, tx)
			row = r
			return err
		})
		if err != nil || row == nil {
			return false
		}
		v, ok := row.Data["result"].(string)
		return ok && v == "subgraph-done"
	}, 30*time.Second, 100*time.Millisecond,
		"caller's node-attributes must carry exit's writeback (result=subgraph-done) per the carry-rule")
}
