// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins the flat-template graph-resolution seam for the
// L5 `graph:` matcher key.
//
// Coverage scope. Pre-v1 templates may declare the legacy flat
// `nodes:` list (no `graphs:` block); the canonicalizer's flat-shape
// fallback maps every such node to the reserved `main` graph. This
// scenario pins that fallback end-to-end: a flat-Nodes template
// resolves to `graph: "main"`, so a matcher targeting
// `graph: "main"` fires — proving the supervisor derives the
// dispatch-time graph from the template's Graphs list (or the
// legacy-flat fallback) before evaluating L5 matchers.
//
// The sub-graph routing complement (a matcher targeting a named
// sub-graph fires only on that sub-graph's internal dispatches; the
// entry-absorbed calling node reports `graph: "main"` per
// concept:delegation) is pinned end-to-end by
// `attribute_overrides_match_overlay_subgraph_e2e_test.go`.
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestAttributeOverridesMatchOverlayFlatTemplateGraphResolution_ResolvesToMain(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("pass").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-graph-routing", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "pass", Executor: "stub"},
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
			// graph=main fires for every dispatch in a flat template.
			map[string]any{
				"matcher": map[string]any{"graph": "main"},
				"overlay": map[string]any{"cli": map[string]any{"where": "outer"}},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-graph", map[string]any{}, overrides)

	n := h.FindNode(iid, "pass")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"pass did not reach fresh")

	var got map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "pass" {
				got = o.Attributes
				break
			}
		}
		if got != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotNil(t, got, "stub did not record any pass dispatch")

	cli, ok := got["cli"].(map[string]any)
	require.True(t, ok, "attributes.cli missing: %#v", got)
	require.Equal(t, "outer", cli["where"],
		"matcher graph=main MUST fire for flat-Nodes template")

	// Counter increment landed.
	var inst *persistence.InstanceRow
	require.Eventually(t, func() bool {
		err := h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.Instances().Get(ctx, iid, tx)
			inst = r
			return err
		})
		if err != nil || inst == nil {
			return false
		}
		c := inst.AttributeOverridesMatchCounts
		return len(c) == 1 && c[0] == 1
	}, 5*time.Second, 50*time.Millisecond, "match-counts should be [1] after dispatch")
}
