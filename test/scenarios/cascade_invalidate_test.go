// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 5 — A → B → C all run to fresh; invalidating A cascades to
// B and C, both re-run.
//
// Migrated to the post-2026-05-14 subscription-cascade template grammar:
// nodes are built via scenario.MakeNode + scenario.WithAttributes. Data
// flow between nodes uses the substitution grammar
// `source: "{{nodes.<X>.attribute.<Y>}}"` to read upstream values; the
// cascade coupling itself is declared explicitly via the receiver's
// `subscribes:` block (the 2026-06-14 explicit-substitution-cascade
// spec retired the auto-subscribe-from-substitution-ref inference and
// added a registration-time coverage check that rejects substitution
// refs without a covering subscription entry).
//
// The behavioural intent (chain reaches fresh; invalidate-A cascades to
// B and C) is preserved; the redesign-shaped assertion ("this node's
// attributes.data contains field X") replaces the legacy
// "this resource has version N" pattern.
package scenarios

import (
	"fmt"
	"testing"
	"time"

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
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "attribute/a/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "b", Type: "attribute/b/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
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
	// @constraint: a was previously a structural root; the subscribes:
	// entry added for the typed-message wake demoted it from root, so
	// the harness's empty-wake doesn't fire it. Emit the typed message
	// here to drive the initial cascade the test assertions expect.
	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 15*time.Second))

	var bRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, b.ID, h.GetMainRunScopeID(iid), tx)
		bRow = r
		return err
	}))
	require.NotNil(t, bRow, "b should have a node_attributes row after fresh")
	require.Contains(t, bRow.Data, "a", "b.attributes.data should contain `a` from nodes.a.attribute.a")
	require.Contains(t, bRow.Data, "b", "b.attributes.data should contain `b` from executor delta")

	// @constraint: invalidate A via a typed-message wake — the universal
	// trigger for in-test invalidation post-spec.
	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 20*time.Second),
		"a did not re-reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 20*time.Second),
		"b did not re-reach fresh")
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 20*time.Second),
		"c did not re-reach fresh")
}
