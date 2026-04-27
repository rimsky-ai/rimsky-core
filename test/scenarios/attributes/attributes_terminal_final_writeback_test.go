// Spec §19.1 — terminal-final writeback path: the executor accumulates
// fields in-process and emits a single Complete{attributes_delta} as
// the terminal event. The supervisor merges the delta into the resolved
// attribute object and persists per spec §17.1 step 6c.
//
// Multi-field delta to verify shallow merge semantics (top-level keys
// overwrite; this delta carries no overlap with the substituted source
// fields, so all four keys land in the row).
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestAttributesTerminalFinalWriteback(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("upstream").Complete(map[string]any{"area": "north"}, true, "u")
	// Executor's terminal Complete carries a multi-field attributes_delta.
	h.Stub.WhenType("worker").Complete(map[string]any{
		"score":  87,
		"label":  "approved",
		"tags":   []any{"a", "b"},
		"detail": map[string]any{"why": "ok"},
	}, true, "w")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-final", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "upstream", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"area": map[string]any{"type": "string"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub", Dependencies: []string{"upstream"}},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// Source-driven from upstream.
						"area": map[string]any{"type": "string", "source": "{{deps.upstream.area}}"},
						// Executor-populated.
						"score":  map[string]any{"type": "integer"},
						"label":  map[string]any{"type": "string"},
						"tags":   map[string]any{"type": "array"},
						"detail": map[string]any{"type": "object"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-final", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	row, err := h.Storage.NodeAttributes().Get(h.Ctx, worker.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	// Substituted source-driven field preserved.
	require.Equal(t, "north", row.Data["area"], "source-driven field missing")
	// Executor-populated fields all merged.
	require.Equal(t, float64(87), row.Data["score"], "score field missing from terminal delta")
	require.Equal(t, "approved", row.Data["label"], "label field missing from terminal delta")
	require.NotNil(t, row.Data["tags"], "tags field missing from terminal delta")
	require.NotNil(t, row.Data["detail"], "detail field missing from terminal delta")
}
