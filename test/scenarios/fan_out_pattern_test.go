// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFanOutPattern(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("child_a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("child_b").Success(map[string]any{"b": 2}, true, "b")
	h.Stub.WhenType("child_c").Success(map[string]any{"c": 3}, true, "c")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fan-out", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "root"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_a", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "root", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "root", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"b": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_c", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "root", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"c": map[string]any{"type": "integer"}},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fanout", map[string]any{})

	root := h.FindNode(iid, "root")
	require.NotNil(t, root)

	h.WaitForNodeState(root.ID, cascade.NodeStateFresh)
	require.Equal(t, 1, h.EventCount(root.ID, "terminal/success"),
		"root (pure-cascade) must terminate exactly once")

	wantAttr := map[string]struct {
		key  string
		want float64
	}{
		"child_a": {"a", 1},
		"child_b": {"b", 2},
		"child_c": {"c", 3},
	}

	for _, typ := range []string{"child_a", "child_b", "child_c"} {
		c := h.FindNode(iid, typ)
		require.NotNil(t, c, "missing %s", typ)
		h.WaitForNodeState(c.ID, cascade.NodeStateFresh)

		dispatches := 0
		for _, o := range h.Stub.Observed() {
			if o.NodeType == typ {
				dispatches++
			}
		}
		require.Equal(t, 1, dispatches, "%s should dispatch exactly once", typ)

		var row *persistence.NodeAttributesRow
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, c.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
			row = r
			return err
		}))
		require.NotNil(t, row, "%s should have a node_attributes row after fresh", typ)
		attrInfo := wantAttr[typ]
		require.EqualValues(t, attrInfo.want, row.Data[attrInfo.key],
			"%s.attributes.data[%q] mismatch", typ, attrInfo.key)
	}
}
