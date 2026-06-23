// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSubgraphExitCarryE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

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
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
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

	require.True(t,
		h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh, 30*time.Second),
		"inner-exit must reach fresh")

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
