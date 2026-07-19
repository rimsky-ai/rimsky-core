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
			map[string]any{
				"matcher": map[string]any{"graph": "main"},
				"overlay": map[string]any{"cli": map[string]any{"where": "outer"}},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-graph", map[string]any{}, overrides)

	n := h.FindNode(iid, "pass")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

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

	require.Eventually(t, func() bool {
		c := attributeOverrideMatchCounts(t, h, iid, 1)
		return len(c) == 1 && c[0] == 1
	}, 5*time.Second, 50*time.Millisecond, "match-counts should be [1] after dispatch")
}
