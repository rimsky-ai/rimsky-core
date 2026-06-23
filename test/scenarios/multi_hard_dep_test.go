// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
						"t_value": map[string]any{"type": "string"},
					},
					"required": []any{"t_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_value": map[string]any{"type": "string"},
					},
					"required": []any{"a_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b_value": map[string]any{"type": "string"},
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

	frameDone := h.WaitForNodeState(cN.ID, cascade.NodeStateFresh, 30*time.Second)
	require.True(t, frameDone,
		"boot must terminate: c should reach fresh after both force_upstream_refresh upstreams settle "+
			"(a never-fresh c means the frame is livelocked on mutual upstream re-seeding); "+
			"per-type dispatch counts at timeout: %v", dispatchCountsByType(h))

	bootCounts := awaitStableDispatchCounts(t, h)
	require.GreaterOrEqual(t, bootCounts["c"], 1, "boot: c must have dispatched")

	cRow := latestAttrRowMultiHardDep(t, h, iid, cN.ID)
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-1", cRow.Data["a_val"], "c should see a's first-fire value")
	require.Equal(t, "from-b-1", cRow.Data["b_val"], "c should see b's first-fire value")

	h.Stub.WhenType("trigger").Success(map[string]any{"t_value": "t-2"}, true, "ok")
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-2"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "ok").Delay(300 * time.Millisecond)
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	h.PostInstanceMessage(iid, "test/wake/trigger", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cRow = latestAttrRowMultiHardDep(t, h, iid, cN.ID)
		if cRow != nil && cRow.Data["a_val"] == "from-a-2" && cRow.Data["b_val"] == "from-b-2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, cRow)
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
	const quiesce = 2 * time.Second
	deadline := time.Now().Add(20 * time.Second)
	last := len(h.Stub.Observed())
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		cur := len(h.Stub.Observed())
		if cur != last {
			last = cur
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= quiesce {
			return dispatchCountsByType(h)
		}
	}
	require.FailNow(t, "dispatch count never stabilized",
		"dispatches kept arriving past the deadline — mutual re-seeding tail; per-type counts: %v",
		dispatchCountsByType(h))
	return nil
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
		r, e := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, nodeID, h.GetMainRunScopeID(instanceID), tx)
		row = r
		return e
	}))
	return row
}
