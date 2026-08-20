// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestMultiHardDepRendezvous(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("trigger").Success(map[string]any{"t_value": "t-1"}, true, "ok")
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-1"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "ok").Delay(300 * time.Millisecond)
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "multi-hard-dep-rendezvous", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/trigger"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "trigger", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/trigger", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"t_value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"t_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"a_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b_value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"b_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "trigger", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "a", Type: "attribute/a_value/changed", ForceUpstreamRefresh: node.BoolPtr(true)},
					node.SubscriptionEntry{Node: "b", Type: "attribute/b_value/changed", ForceUpstreamRefresh: node.BoolPtr(true)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.a_value}}",
						},
						"b_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.b.attribute.b_value}}",
						},
					},
					"required": []any{"a_val", "b_val"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-multi-hard-dep", map[string]any{})
	trigN := h.FindNode(iid, "trigger")
	aN := h.FindNode(iid, "a")
	bN := h.FindNode(iid, "b")
	cN := h.FindNode(iid, "c")
	require.NotNil(t, trigN)
	require.NotNil(t, aN)
	require.NotNil(t, bN)
	require.NotNil(t, cN)
	h.PostInstanceMessage(iid, "test/wake/trigger", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	h.WaitForNodeState(cN.ID, cascade.NodeStateFresh)

	var cRow *persistence.NodeAttributesRow
	awaited.Until(t, "c to see both upstreams' first-fire values", func() bool {
		cRow = latestAttrRowMultiHardDep(t, h, iid, cN.ID)
		return cRow != nil && cRow.Data["a_val"] == "from-a-1" && cRow.Data["b_val"] == "from-b-1"
	})

	bootCounts := awaitStableDispatchCounts(t, h)
	require.GreaterOrEqual(t, bootCounts["c"], 1, "boot: c must have dispatched")

	h.Stub.WhenType("trigger").Success(map[string]any{"t_value": "t-2"}, true, "ok")
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-2"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "ok").Delay(300 * time.Millisecond)
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	h.PostInstanceMessage(iid, "test/wake/trigger", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	awaited.Until(t, "frame 2 to terminate with c seeing both upstreams' second-fire values", func() bool {
		cRow = latestAttrRowMultiHardDep(t, h, iid, cN.ID)
		return cRow != nil && cRow.Data["a_val"] == "from-a-2" && cRow.Data["b_val"] == "from-b-2"
	})
	require.Equal(t, "from-a-2", cRow.Data["a_val"],
		"frame 2 must terminate with c seeing a's second-fire value")
	require.Equal(t, "from-b-2", cRow.Data["b_val"],
		"frame 2 must terminate with c seeing b's second-fire value")

	frame2Counts := awaitStableDispatchCounts(t, h)
	for _, typ := range []string{"trigger", "a", "b", "c"} {
		require.Equal(t, bootCounts[typ]+1, frame2Counts[typ],
			"acceptance frame: node type %q must run exactly once — "+
				"a settled force_upstream_refresh upstream must not be re-affirmed (mutual re-seeding)", typ)
	}

	aIdx := lastDispatchIndex(h, "a")
	bIdx := lastDispatchIndex(h, "b")
	cIdx := lastDispatchIndex(h, "c")
	require.Greater(t, cIdx, aIdx, "receiver c must dispatch after upstream a settles")
	require.Greater(t, cIdx, bIdx, "receiver c must dispatch after upstream b settles")
}

func dispatchCountsByType(h *scenario.Harness) map[string]int {
	got := map[string]int{}
	for _, o := range h.Stub.Observed() {
		got[o.NodeType]++
	}
	return got
}

func awaitStableDispatchCounts(t *testing.T, h *scenario.Harness) map[string]int {
	t.Helper()
	h.WaitForSchedulerQuiescence()
	return dispatchCountsByType(h)
}

func lastDispatchIndex(h *scenario.Harness, typ string) int {
	idx := -1
	for i, o := range h.Stub.Observed() {
		if o.NodeType == typ {
			idx = i
		}
	}
	return idx
}

func latestAttrRowMultiHardDep(t *testing.T, h *scenario.Harness, instanceID, nodeID shared.UUID) *persistence.NodeAttributesRow {
	t.Helper()
	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, e := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, nodeID, h.GetLatestFrameRootRunScopeID(instanceID), tx)
		row = r
		return e
	}))
	return row
}
