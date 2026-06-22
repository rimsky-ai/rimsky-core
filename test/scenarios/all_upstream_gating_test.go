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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAllUpstreamGating_DiamondSettlementPropagated(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "a-1"}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c_value": "from-c-1"}, true, "c")
	h.Stub.WhenType("d").Success(map[string]any{}, true, "d")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "all-upstream-gating-diamond", Version: "1",
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
					"type": "object",
					"properties": map[string]any{
						"a_value": map[string]any{"type": "string"},
					},
					"required": []any{"a_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"c_value": map[string]any{"type": "string"},
					},
					"required": []any{"c_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "d", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "b", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "b", Type: "attribute/b_value/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "c", Type: "attribute/c_value/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.b.attribute.b_value}}",
						},
						"c_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.c.attribute.c_value}}",
						},
					},
					"required": []any{"b_val", "c_val"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-all-upstream-gating", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	c := h.FindNode(iid, "c")
	d := h.FindNode(iid, "d")
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.NotNil(t, c)
	require.NotNil(t, d)
	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	require.True(t, h.WaitForNodeState(d.ID, cascade.NodeStateFresh, 30*time.Second),
		"d should reach fresh after the initial frame settles")

	countRuns := func(nodeType string) int {
		n := 0
		for _, obs := range h.Stub.Observed() {
			if obs.NodeType == nodeType {
				n++
			}
		}
		return n
	}
	baselineDRuns := countRuns("d")
	require.GreaterOrEqual(t, baselineDRuns, 1, "d should have run at least once initially")

	releaseC := make(chan struct{})
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "a-2"}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "b")
	h.Stub.WhenType("c").HoldUntil(releaseC).Success(map[string]any{"c_value": "from-c-2"}, true, "c")
	h.Stub.WhenType("d").Success(map[string]any{}, true, "d")

	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should re-reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b should re-reach fresh while c is held")
	require.Eventually(t, func() bool { return countRuns("c") >= 2 },
		30*time.Second, 25*time.Millisecond, "c should dispatch into its hold")

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		var ineligibleRowViolations int
		h.QueryRowSQL(
			`SELECT COUNT(*) FROM rimsky_node_runs
			  WHERE node_id = $1
			    AND state IN ('stale','running','held','parked')`,
			[]any{d.ID}, &ineligibleRowViolations)
		require.Zero(t, ineligibleRowViolations,
			"d's run row was claimed or left dispatch-eligible while subscribed upstream c was in-flight in the frame")
		require.Equal(t, baselineDRuns, countRuns("d"),
			"d dispatched while subscribed upstream c was still in-flight in the frame")
		time.Sleep(50 * time.Millisecond)
	}

	close(releaseC)
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 30*time.Second),
		"c should re-reach fresh after release")
	require.True(t, h.WaitForNodeState(d.ID, cascade.NodeStateFresh, 30*time.Second),
		"d should re-reach fresh after the last upstream settles")

	require.Eventually(t, func() bool { return countRuns("d") == baselineDRuns+1 },
		10*time.Second, 25*time.Millisecond,
		"d should run exactly once after the last upstream settles")
	time.Sleep(1 * time.Second)
	require.Equal(t, baselineDRuns+1, countRuns("d"),
		"d must run exactly once per frame, not once per settling sender")

	var lastD *struct {
		bVal, cVal any
	}
	for _, obs := range h.Stub.Observed() {
		if obs.NodeType == "d" {
			lastD = &struct{ bVal, cVal any }{obs.Attributes["b_val"], obs.Attributes["c_val"]}
		}
	}
	require.NotNil(t, lastD, "stub should have observed d's dispatch")
	require.Equal(t, "from-b-2", lastD.bVal,
		"d's substitution context must carry b's this-frame contribution")
	require.Equal(t, "from-c-2", lastD.cVal,
		"d's substitution context must carry c's this-frame contribution")
}
