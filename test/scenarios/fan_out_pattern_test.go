// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
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

	for _, typ := range []string{"child_a", "child_b", "child_c"} {
		c := h.FindNode(iid, typ)
		require.NotNil(t, c, "missing %s", typ)
		h.WaitForNodeState(c.ID, cascade.NodeStateFresh)
	}
}
