// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario test for the per-subscription `force_upstream_refresh: true`
// cascade flag under per-run attribute keying. When receiver C declares
// a `subscribes:` entry naming B with `force_upstream_refresh: true`
// (covering its `{{nodes.B.attribute.b_value}}` substitution read), an
// invalidation that reaches C drags B into the same frame so B's value
// is freshly produced before C dispatches — the upstream-refresh pull.
//
// Per spec
// .ok-planner/specs/2026-06-14-explicit-substitution-cascade-behavior-design.md
// (which retired the attribute-field `hard_dep: true` flag in favor of
// the per-subscription `force_upstream_refresh: true` flag).
package per_run_attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestPerRunAttributes_HardDepPullsUpstream is the canonical executable
// proof for STORY-pull-upstream-fresh-on-read (spec
// 2026-06-14-explicit-substitution-cascade-behavior).
//
// As a template author, I can declare on a subscription that the
// sender be brought current before my receiver dispatches, so the
// receiver's substitution context at dispatch contains the sender's
// freshest value. The scenario:
//
//   - Receiver C subscribes to B with force_upstream_refresh: true and
//     reads {{nodes.b.attribute.b_value}}.
//   - First frame settles all three nodes; C reads B's first-fire value
//     ("from-b-1") into its attribute ledger.
//   - The test invalidates A (the trigger), NOT B directly. Per the
//     force_upstream_refresh: true edge, B is dragged into the same
//     frame, re-runs, and produces its second-fire value ("from-b-2").
//     C's substitution context at dispatch contains the freshest value.
//
// Falsifier (per spec): C's substitution context at dispatch contains
// a stale value for B (matching B's prior run rather than a value
// produced this frame), or C's dispatch fails because B's value is
// absent. The pre/post assertion at lines reading
// `from-b-1` (line 116) and `from-b-2` (line 142) catches a stale
// read — not just any read — because a missing pull would leave the
// post-invalidation row carrying B's first-fire value.
//
//	@story: upstream-pull-on-invalidate
func TestPerRunAttributes_HardDepPullsUpstream(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-1"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-hard-dep", Version: "1",
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
					node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					// @deliberate: Covers the {{nodes.a.attribute.a_value}} substitution read.
					node.SubscriptionEntry{Node: "a", Type: "attribute/a_value/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					// @deliberate: Migrated from attribute-field hard_dep: true on b_val
					// reading {{nodes.b.attribute.b_value}}. Carries
					// force_upstream_refresh: true so c's invalidation drags
					// b into the same frame for re-evaluation.
					node.SubscriptionEntry{Node: "b", Type: "attribute/b_value/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(true)},
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
	iid := h.CreateInstance(tid, "ck-hard-dep", map[string]any{})
	aN := h.FindNode(iid, "a")
	bN := h.FindNode(iid, "b")
	cN := h.FindNode(iid, "c")
	require.NotNil(t, aN)
	require.NotNil(t, bN)
	require.NotNil(t, cN)

	require.True(t, h.WaitForNodeState(cN.ID, cascade.NodeStateFresh, 15*time.Second),
		"c should reach fresh after upstream-refresh cascade")

	var cRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetMainRunScopeID(iid), tx)
		cRow = r
		return err
	}))
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-1", cRow.Data["a_val"], "c should see a's first-fire value")
	require.Equal(t, "from-b-1", cRow.Data["b_val"], "c should see b's first-fire value (upstream-refresh pulled)")

	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-2"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	// @deliberate: Invalidate a (the trigger, NOT b). The
	// force_upstream_refresh: true edge from c to b drags b into
	// the same frame so b's second-fire value is produced before c
	// dispatches. h.InvalidateNode is the in-process supervisor
	// invalidation — the admin-route invalidate was retired with
	// the typed-message schema layer; the debug-channel override
	// is for paused-instance / breakpoint-hit flows, not the
	// running-instance invalidation under test here.
	h.InvalidateNode(iid, aN.ID)

	// @deliberate: Wait until C's latest attribute row reflects both A's and B's second-fire values.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetMainRunScopeID(iid), tx)
			cRow = r
			return err
		})
		if cRow != nil && cRow.Data["a_val"] == "from-a-2" && cRow.Data["b_val"] == "from-b-2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-2", cRow.Data["a_val"], "c should see a's second-fire value")
	require.Equal(t, "from-b-2", cRow.Data["b_val"],
		"c should see b's second-fire value (upstream-refresh cascade re-fired b)")
}

// @deliberate: Direct coverage of the upstream-refresh parked-upstream wake lives
// in the unit test `runtime.TestPullHardDepUpstreams_WakesParkedUpstream`
// (file:runtime/hard_dep_cascade_test.go). It sets up a parked
// upstream via direct SQL and invokes the cascade walk in isolation,
// avoiding the scenario.Harness's race-prone timing constraints
// (the cascade walk's `ListByInstance` snapshot is taken before
// any concurrent park terminal commits, so an external
// `WaitForNodeState` poll cannot reliably sequence a "B parked → A's
// cascade walks" ordering through the harness).

// TestPerRunAttributes_HardDepPullsUpstream_DirectInvalidateOfReceiver
// is the regression pin for the direct-invalidate branch of
// `runtime.walkCascadeForInvalidatedNode` (the upstream-pull site
// added by spec 2026-06-14-explicit-substitution-cascade-behavior).
// Unlike TestPerRunAttributes_HardDepPullsUpstream which invalidates
// `a` (the upstream) and exercises the cascaded-invalidate path via
// `cascadeSubscribersStaleInTx::pullForceRefreshUpstreams`, this test
// invalidates `c` (the receiver) DIRECTLY via the admin invalidate
// API — the only path that lands in `walkCascadeForInvalidatedNode`
// for c without first cascading through another node.
//
// Without the upstream-pull at the direct-invalidate site, c's
// dispatch after the invalidate would see b's first-fire value
// because b would not have been dragged into the frame. The proof
// is the assertion that c's substitution context at re-dispatch
// contains b's second-fire value.
//
//	@story: upstream-pull-on-invalidate
func TestPerRunAttributes_HardDepPullsUpstream_DirectInvalidateOfReceiver(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-1"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "per-run-hard-dep-direct", Version: "1",
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
					node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "a", Type: "attribute/a_value/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "b", Type: "attribute/b_value/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(true)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// @deliberate: a_val source uses the `?` lenient
						// marker — c's direct invalidate drags only b
						// (force_upstream_refresh: true) into the new
						// frame, NOT a, so a's drained wait-set row is
						// absent at c's re-dispatch. Without lenient
						// recovery the strict source would fail c with
						// `template_resolution_failed` before the b_val
						// assertion below could fire. Carry-forward of
						// a_val's prior value is the substitution
						// engine's lenient-recovery behaviour.
						"a_val": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.a_value?}}",
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
	iid := h.CreateInstance(tid, "ck-hard-dep-direct", map[string]any{})
	aN := h.FindNode(iid, "a")
	bN := h.FindNode(iid, "b")
	cN := h.FindNode(iid, "c")
	require.NotNil(t, aN)
	require.NotNil(t, bN)
	require.NotNil(t, cN)

	require.True(t, h.WaitForNodeState(cN.ID, cascade.NodeStateFresh, 15*time.Second),
		"c should reach fresh after initial cascade")

	var cRow *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetMainRunScopeID(iid), tx)
		cRow = r
		return err
	}))
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-1", cRow.Data["a_val"], "c should see a's first-fire value")
	require.Equal(t, "from-b-1", cRow.Data["b_val"], "c should see b's first-fire value")

	// @deliberate: Re-prime stubs for second fire — but only b's value will be
	// dragged. a will NOT re-fire (it has no force_upstream_refresh
	// edge from c into a; the c→a edge is wake-on-change only).
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "ok")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	// @deliberate: DIRECT invalidate of c (the receiver). This routes through
	// runtime.InvalidateNode → invalidateInFrame /
	// invalidateNextFrame → walkCascadeForInvalidatedNode for c,
	// which is the load-bearing site under test (the added
	// upstream-pull at lib/runtime/cascade_invalidate.go that
	// drags c's own force_upstream_refresh upstreams into the
	// frame BEFORE c dispatches). h.InvalidateNode is the
	// in-process supervisor invalidation — the admin-route
	// invalidate was retired with the typed-message schema layer.
	h.InvalidateNode(iid, cN.ID)

	// @deliberate: Diagnostic wait for b to re-run and produce its second-fire
	// value. If this never happens, the upstream-pull failed to
	// drag b into the new frame.
	bDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(bDeadline) {
		var bRow *persistence.NodeAttributesRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, bN.ID, h.GetMainRunScopeID(iid), tx)
			bRow = r
			return err
		})
		if bRow != nil && bRow.Data["b_value"] == "from-b-2" {
			t.Logf("b re-ran successfully with from-b-2")
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// @deliberate: Wait until c's latest attribute row reflects b's second-fire
	// value. The proof is the assertion that b_val == "from-b-2"
	// — only achievable if c's direct invalidate dragged b into
	// the frame and forced b to re-run.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, cN.ID, h.GetMainRunScopeID(iid), tx)
			cRow = r
			return err
		})
		if cRow != nil && cRow.Data["b_val"] == "from-b-2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, cRow)
	require.Equal(t, "from-b-2", cRow.Data["b_val"],
		"c's direct invalidate must pull b (force_upstream_refresh: true) into the frame; "+
			"b's second-fire value is the load-bearing observable for the direct-invalidate "+
			"branch in walkCascadeForInvalidatedNode")
}
