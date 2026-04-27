// Spec §19.1 — optional source-directive missing → field omitted.
//
// Per spec §10.3, a substitution miss on an optional (non-required)
// schema field is silently dropped — the field is absent from the
// resolved attributes, and the node continues to run. Contrast with the
// required-missing path which raises `template_resolution_failed`.
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestAttributesOptionalMissingOmitted(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// upstream writes "value" but downstream's source asks for "absent" too.
	h.Stub.WhenType("upstream").Complete(map[string]any{"value": "present"}, true, "u")
	h.Stub.WhenType("downstream").Complete(map[string]any{}, true, "d")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-optional", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "upstream", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"value": map[string]any{"type": "string"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream", Executor: "stub", Dependencies: []string{"upstream"}},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"present": map[string]any{"type": "string", "source": "{{deps.upstream.value}}"},
						// not in `required` and source missing → silently omitted.
						"absent": map[string]any{"type": "string", "source": "{{deps.upstream.absent_field}}"},
					},
					"required": []any{"present"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-optional", map[string]any{})

	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, downstream)
	// Downstream reaches fresh (the optional miss is not fatal).
	require.True(t, h.WaitForNodeState(downstream.ID, shared.NodeStateFresh, 20*time.Second),
		"downstream did not reach fresh")

	row, err := h.Storage.NodeAttributes().Get(h.Ctx, downstream.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "present", row.Data["present"], "expected required field substituted")
	_, hasAbsent := row.Data["absent"]
	require.False(t, hasAbsent, "expected optional missing field to be omitted from attributes.data")
}
