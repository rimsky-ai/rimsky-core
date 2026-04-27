// Spec §19.1 — `{{params.<key>}}` substitution at dispatch.
//
// Single-node template; the node's attribute schema declares a
// source-driven field with `source: "{{params.region}}"`. The instance
// is created with `params: {region: "south"}`. At dispatch the runner
// substitutes the param value into the node's attributes.data.
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestAttributesSubstitutionFromParams(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-params", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region": map[string]any{"type": "string", "source": "{{params.region}}"},
					},
					"required": []any{"region"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-params", map[string]any{"region": "south"})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	row, err := h.Storage.NodeAttributes().Get(h.Ctx, worker.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "south", row.Data["region"], "expected attributes.region substituted from params.region")
}
