// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package per_run_attributes

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestPerRunAttributes_FallbackOperator_LiteralFires(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-fallback", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"resolved": map[string]any{
							"type":   "string",
							"source": `{{params.absent | "fallback-fired"}}`,
						},
					},
					"required": []any{"resolved"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fallback", map[string]any{})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)

	h.WaitForNodeState(w.ID, cascade.NodeStateFresh)

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, "fallback-fired", row.Data["resolved"],
		"fallback literal should resolve into attributes.data")
}
