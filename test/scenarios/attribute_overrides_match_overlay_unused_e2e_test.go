// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
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
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	awaited.Until(t, "the five override match-counts to read [1, 0, 1, 0, 0]", func() bool {
		counts := attributeOverrideMatchCounts(t, h, iid, 5)
		return len(counts) == 5 && counts[0] == 1 && counts[1] == 0 && counts[2] == 1 && counts[3] == 0 && counts[4] == 0
	})
}
