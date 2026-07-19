// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestDispatchInputBagSurvivesExecutorWriteback(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("seed").Success(map[string]any{"marker": "seed-value"}, true, "seed-ok")
	h.Stub.WhenType("b").Success(map[string]any{"executor_extra": "exec-value"}, true, "b-ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "dispatch-input-bag-survives-writeback", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "seed", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"marker": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"marker"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "seed", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "seed", Type: "attribute/marker/changed", ForceUpstreamRefresh: node.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"carried": map[string]any{
							"type":   "string",
							"source": "{{nodes.seed.attribute.marker}}",
						},
						"executor_extra": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"carried"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-dispatch-input-bag-survives-writeback", map[string]any{})
	b := h.FindNode(iid, "b")
	require.NotNil(t, b)

	h.WaitForNodeState(b.ID, cascade.NodeStateFresh)

	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		latest, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, b.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest)

		dispatchBag, err := h.Persist.NodeAttributes().GetDispatchInputBag(h.Ctx, tx, latest.NodeRunID)
		if err != nil {
			return err
		}
		require.NotNil(t, dispatchBag, "dispatch_input_bag must be set at gate-eval time")
		require.Equal(t, map[string]any{"carried": "seed-value"}, dispatchBag,
			"dispatch_input_bag must hold exactly the gate-eval-resolved bag (carried only) and must "+
				"never be overwritten by the executor's own writeback (executor_extra), proving "+
				"dispatch_input_bag lives in its own column separate from the live data column")

		full, err := h.Persist.NodeAttributes().GetByRun(h.Ctx, latest.NodeRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, full)
		require.Equal(t, "seed-value", full.Data["carried"],
			"live data column must retain the gate-eval-seeded carried value across the writeback merge")
		require.Equal(t, "exec-value", full.Data["executor_extra"],
			"live data column must be updated by the executor's own writeback (executor_extra), "+
				"proving data and dispatch_input_bag genuinely diverge after writeback: data picks up "+
				"executor_extra, dispatch_input_bag does not")
		return nil
	}))
}
