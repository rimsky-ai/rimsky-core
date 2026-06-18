// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package per_run_attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestPerRunAttributes_EmbeddedSource_LiteralAndDirectives(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-embedded-source", Version: "1",
		ParamsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"domain": map[string]any{"type": "string"},
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":   "string",
							"source": `Generate config for {{params.domain}}. Notes: {{params.notes | "none"}}. Done.`,
						},
					},
					"required": []any{"prompt"},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-embedded", map[string]any{
		"domain": "alpha",
	})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)

	require.True(t, h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker should reach fresh — embedded source + fallback should compose cleanly")

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetMainRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, "Generate config for alpha. Notes: none. Done.", row.Data["prompt"],
		"embedded source should resolve to the composed string with the fallback firing for the missing param")
}
