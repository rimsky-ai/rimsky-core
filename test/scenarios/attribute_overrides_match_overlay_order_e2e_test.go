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

func TestAttributeOverridesMatchOverlayOrder_LaterWins(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-order", Version: "1",
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
				"overlay": map[string]any{
					"cli": map[string]any{
						"shared":     "first",
						"first-only": "yes",
					},
				},
			},
			map[string]any{
				"matcher": map[string]any{"node_type": "worker"},
				"overlay": map[string]any{
					"cli": map[string]any{
						"shared":      "second",
						"second-only": "yes",
					},
				},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-order", map[string]any{}, overrides)

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	got := waitForObservedAttrs(h, "worker")

	cli, ok := got["cli"].(map[string]any)
	require.True(t, ok, "attributes.cli missing: %#v", got)
	require.Equal(t, "second", cli["shared"], "later entry must win on conflicting path")
	require.Equal(t, "yes", cli["first-only"], "non-conflicting path from first entry must apply")
	require.Equal(t, "yes", cli["second-only"], "non-conflicting path from second entry must apply")

	require.Eventually(t, func() bool {
		c := attributeOverrideMatchCounts(t, h, iid, 2)
		return len(c) == 2 && c[0] == 1 && c[1] == 1
	}, 5*time.Second, 50*time.Millisecond, "match-counts should be [1, 1]")
}
