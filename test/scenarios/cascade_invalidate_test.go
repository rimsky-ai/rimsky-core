// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestCascadeInvalidate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "chain", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/a"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/a", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "attribute/a/changed", ForceUpstreamRefresh: node.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a": map[string]any{"type": "integer", "source": "{{nodes.a.attribute.a}}"},
						"b": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "b", Type: "attribute/b/changed", ForceUpstreamRefresh: node.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b": map[string]any{"type": "integer", "source": "{{nodes.b.attribute.b}}"},
						"c": map[string]any{"type": "integer"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-chain", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	c := h.FindNode(iid, "c")
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.NotNil(t, c)
	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	h.WaitForNodeState(a.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(b.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(c.ID, cascade.NodeStateFresh)

	var bRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, b.ID, h.GetMainRunScopeID(iid), tx)
		bRow = r
		return err
	}))
	require.NotNil(t, bRow, "b should have a node_attributes row after fresh")
	require.Contains(t, bRow.Data, "a", "b.attributes.data should contain `a` from nodes.a.attribute.a")
	require.Contains(t, bRow.Data, "b", "b.attributes.data should contain `b` from executor delta")

	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	h.WaitForNodeState(a.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(b.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(c.ID, cascade.NodeStateFresh)
}
