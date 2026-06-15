// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executable proof for STORY-all-upstream-gating: in a fan-in shape, a
// receiver dispatches only after ALL of its in-flight upstreams in the
// frame have resolved — regardless of how the upstreams' staleness
// arrived — and its substitution context carries the full upstream set.
//
// Shape: a diamond A → (B, C) → D. One invalidation of A opens one
// frame; A's *settlement* walk (not an invalidation walk) marks B and C
// stale in that frame. The settlement walk seeds no next-tier wait-set
// gates, so when B settles while C is still in-flight, D's eligibility
// cannot come from the wait-set ledger alone — only the propagation-
// path-independent dispatch-eligibility condition ("a stale run is not
// eligible while any subscribed upstream has an in-flight run in the
// same frame") keeps D parked. With the gate absent, D would dispatch
// at that midpoint and compute the frame's result from a half-settled
// upstream set (fresh B, stale C).
//
// C is held in-flight via the stub's deterministic channel hold (the
// injection hook the story's proof names), so the midpoint assertion
// observes a pinned in-flight set rather than racing wall-clock delays.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestAllUpstreamGating_DiamondSettlementPropagated drives the story's
// acceptance: D runs exactly once for the frame, after the last
// in-flight upstream (the held C) resolves, and D's dispatch-time
// substitution context contains both B's and C's contributions.
func TestAllUpstreamGating_DiamondSettlementPropagated(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: Initial frame runs fast so the instance settles promptly.
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "a-1"}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c_value": "from-c-1"}, true, "c")
	h.Stub.WhenType("d").Success(map[string]any{}, true, "d")

	// @deliberate: The diamond. B and C subscribe to A; D subscribes to B and C AND
	// pulls one attribute from each (both required), so D's dispatch
	// carries the full upstream set in its substitution context — and a
	// premature dispatch against a half-settled set is independently
	// detectable as a missing-source failure, not just a count drift.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "all-upstream-gating-diamond", Version: "1",
		Nodes: []node.TemplateNodeDef{
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
					// @deliberate: Cover the substitution reads explicitly (today-equivalent flags).
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

	// @deliberate: Re-script: A and B stay fast; C is held in-flight until the test
	// releases it (the deterministic injection hook). D records its
	// dispatch-time attribute bag for the substitution-context check.
	releaseC := make(chan struct{})
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "a-2"}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "b")
	h.Stub.WhenType("c").HoldUntil(releaseC).Success(map[string]any{"c_value": "from-c-2"}, true, "c")
	h.Stub.WhenType("d").Success(map[string]any{}, true, "d")

	// @deliberate: One invalidation of A, one frame. A re-runs; B's and C's staleness
	// arrives via A's SETTLEMENT walk — the propagation path that seeds
	// no next-tier wait-set gates for D.
	h.InvalidateNode(iid, a.ID)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should re-reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b should re-reach fresh while c is held")
	require.Eventually(t, func() bool { return countRuns("c") >= 2 },
		30*time.Second, 25*time.Millisecond, "c should dispatch into its hold")

	// @constraint: THE midpoint: B settled (its settlement walk marked D stale), C
	// still in-flight. D must not be dispatch-eligible — its in-flight
	// run row (when present) stays pending and unclaimed (settled rows
	// persist with phase='completed' and are out of scope), and the stub
	// never observes another `d` execution. Hold the midpoint for a
	// window so a late premature dispatch cannot slip past one sample.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		var ineligibleRowViolations int
		h.QueryRowSQL(
			`SELECT COUNT(*) FROM rimsky_node_runs
			  WHERE node_id = $1
			    AND phase IN ('pending','active','held','parked')
			    AND (claimed_by IS NOT NULL OR phase <> 'pending')`,
			[]any{d.ID}, &ineligibleRowViolations)
		require.Zero(t, ineligibleRowViolations,
			"d's run row was claimed or left pending while subscribed upstream c was in-flight in the frame")
		require.Equal(t, baselineDRuns, countRuns("d"),
			"d dispatched while subscribed upstream c was still in-flight in the frame")
		time.Sleep(50 * time.Millisecond)
	}

	// @deliberate: Release the held upstream: the last in-flight upstream resolves,
	// D becomes eligible, dispatches, and the frame resolves.
	close(releaseC)
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 30*time.Second),
		"c should re-reach fresh after release")
	require.True(t, h.WaitForNodeState(d.ID, cascade.NodeStateFresh, 30*time.Second),
		"d should re-reach fresh after the last upstream settles")

	// @constraint: Single dispatch: exactly one new `d` run for the whole diamond
	// re-run — not re-fired per settling sender, never run early.
	require.Eventually(t, func() bool { return countRuns("d") == baselineDRuns+1 },
		10*time.Second, 25*time.Millisecond,
		"d should run exactly once after the last upstream settles")
	// @constraint: Grace window: no straggler second dispatch.
	time.Sleep(1 * time.Second)
	require.Equal(t, baselineDRuns+1, countRuns("d"),
		"d must run exactly once per frame, not once per settling sender")

	// @deliberate: Full upstream set in the substitution context: D's dispatch-time
	// attribute bag (recorded by the stub off the wire) carries BOTH
	// this-frame contributions — fresh B and the released C — proving
	// the run was computed against the fully-settled upstream set.
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
