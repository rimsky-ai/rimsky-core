// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAttributeOverridesMatchOverlayUnused_CounterZeroForNonFiringEntries(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-unused", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cli": map[string]any{"type": "object"},
						"ok":  map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	overrides := map[string]any{
		"by_match": []any{
			map[string]any{
				"matcher": map[string]any{"node_type": "worker"},
				"overlay": map[string]any{"cli": map[string]any{"tag-0": "fired"}},
			},
			map[string]any{
				"matcher": map[string]any{"node_type": "worker", "child_key": "k1"},
				"overlay": map[string]any{"cli": map[string]any{"tag-1": "fired"}},
			},
			map[string]any{
				"matcher": map[string]any{},
				"overlay": map[string]any{"cli": map[string]any{"tag-2": "fired"}},
			},
			map[string]any{
				"matcher": map[string]any{"child_key": "k2"},
				"overlay": map[string]any{"cli": map[string]any{"tag-3": "fired"}},
			},
			map[string]any{
				"matcher": map[string]any{"executor": "stub", "child_key": "k3"},
				"overlay": map[string]any{"cli": map[string]any{"tag-4": "fired"}},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-unused", map[string]any{}, overrides)

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	var lastCounts []int64
	require.Eventually(t, func() bool {
		lastCounts = attributeOverrideMatchCounts(t, h, iid, 5)
		return len(lastCounts) == 5 && lastCounts[0] == 1 && lastCounts[1] == 0 && lastCounts[2] == 1 && lastCounts[3] == 0 && lastCounts[4] == 0
	}, 5*time.Second, 50*time.Millisecond,
		"match-counts should be [1, 0, 1, 0, 0]; got=%v", &lastCounts)
}
