// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Subscription-cascade + wait-set scenario coverage for the
// post-2026-05-14 subscription model. The wait-set ledger drives
// dispatch eligibility (rather than the retired dependencies-all-fresh
// predicate); these tests exercise the discipline end-to-end.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestSubscriptionCascade_MultipleInvalidatorDrain covers the
// multiple-senders → single receiver case: R subscribes to A, B, C; all
// three are invalidated in one frame; R waits for all three to settle
// before dispatching.
func TestSubscriptionCascade_MultipleInvalidatorDrain(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c")
	h.Stub.WhenType("r").Success(map[string]any{"r": 1}, true, "r")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-multi", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "b", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "c", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "b", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-multi-invalidator", map[string]any{})
	r := h.FindNode(iid, "r")
	require.NotNil(t, r)

	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should reach fresh after a, b, c settle")
}

// TestSubscriptionCascade_EligibilityRespectsMultipleSenders pins the
// upstream-gating eligibility condition within ONE frame: a stale
// receiver is not dispatch-eligible while ANY subscribed upstream has
// an in-flight run in the same frame, regardless of how the
// receiver's staleness arrived.
//
// Shape: a diamond — B and C subscribe to A; R subscribes to B and C.
// One invalidation of A opens one frame; A's settlement marks B and C
// stale in that frame (so both senders are in-flight together —
// serial_queue invalidates of B and C directly would each open their
// own frame, which is why this test invalidates only A).
//
// The load-bearing midpoint is after B settles while C is still held
// in-flight: B's settlement walk marks R stale WITHOUT seeding a
// wait-set gate for C (the settlement walk seeds no next-tier gates —
// only the invalidation walk does). Eligibility therefore cannot come
// from the wait-set alone; it is the two-condition dispatch-time
// predicate: no undrained wait-set rows AND no subscribed upstream
// with an in-flight run in the frame. With the gate absent, R would
// dispatch here and compute the frame's result from a half-settled
// upstream set (fresh B, stale C).
//
// Both senders are held open via deterministic stub holds (not
// wall-clock delays), so each midpoint assertion observes a pinned
// in-flight set rather than racing the executor.
func TestSubscriptionCascade_EligibilityRespectsMultipleSenders(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: Initial runs are fast so the instance settles promptly.
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c")
	h.Stub.WhenType("r").Success(map[string]any{"r": 1}, true, "r")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-eligibility", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "b", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-eligibility-multi", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	c := h.FindNode(iid, "c")
	r := h.FindNode(iid, "r")
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.NotNil(t, c)
	require.NotNil(t, r)

	// @deliberate: Initial settle: R reaches fresh after a → (b, c) → r.
	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should reach fresh initially")

	countRuns := func(nodeType string) int {
		n := 0
		for _, obs := range h.Stub.Observed() {
			if obs.NodeType == nodeType {
				n++
			}
		}
		return n
	}
	baselineRuns := countRuns("r")
	require.GreaterOrEqual(t, baselineRuns, 1, "r should have run at least once initially")

	// @deliberate: Re-script: A stays fast; B and C are held in-flight until the
	// test releases them, pinning the in-flight set at each midpoint.
	releaseB := make(chan struct{})
	releaseC := make(chan struct{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 2}, true, "a")
	h.Stub.WhenType("b").HoldUntil(releaseB).Success(map[string]any{"b": 2}, true, "b")
	h.Stub.WhenType("c").HoldUntil(releaseC).Success(map[string]any{"c": 2}, true, "c")
	h.Stub.WhenType("r").Success(map[string]any{"r": 2}, true, "r")

	// @deliberate: One invalidation, one frame: A re-runs; its settlement marks B
	// and C stale in the same frame and both dispatch into the holds.
	h.InvalidateNode(iid, a.ID)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should re-reach fresh")

	// @deliberate: Wait until both senders are actually in-flight (dispatched into
	// the stub holds) so the first midpoint observes two held runs.
	require.Eventually(t, func() bool {
		return countRuns("b") >= 2 && countRuns("c") >= 2
	}, 30*time.Second, 25*time.Millisecond, "b and c should both dispatch into their holds")

	// @constraint: assertReceiverNotDispatchEligible holds the midpoint for a
	// window and asserts R is never claimed for dispatch: its
	// in-flight run row (when present) stays pending and unclaimed —
	// settled rows persist with phase='completed' and are out of
	// scope — and the stub never observes another `r` execution.
	assertReceiverNotDispatchEligible := func(midpoint string) {
		t.Helper()
		deadline := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(deadline) {
			var ineligibleRowViolations int
			h.QueryRowSQL(
				`SELECT COUNT(*) FROM rimsky_node_runs
				  WHERE node_id = $1
				    AND phase IN ('pending','active','held','parked')
				    AND (claimed_by IS NOT NULL OR phase <> 'pending')`,
				[]any{r.ID}, &ineligibleRowViolations)
			require.Zerof(t, ineligibleRowViolations,
				"%s: r's run row was claimed or transitioned out of pending while a subscribed upstream was in-flight", midpoint)
			require.Equalf(t, baselineRuns, countRuns("r"),
				"%s: r dispatched while a subscribed upstream was in-flight", midpoint)
			time.Sleep(50 * time.Millisecond)
		}
	}

	// @constraint: Midpoint 1: both B and C in-flight. R must not be dispatch-
	// eligible (its staleness arrived via the invalidation walk; both
	// senders are in-flight in the frame).
	assertReceiverNotDispatchEligible("midpoint 1 (b and c in-flight)")

	// @deliberate: Release B; C stays held. B's settlement marks R stale via the
	// settlement walk — the propagation path that seeds NO next-tier
	// wait-set gate for C. This midpoint is the regression pin: only
	// the in-flight-upstream eligibility condition keeps R parked
	// here.
	close(releaseB)
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b should re-reach fresh after release")

	assertReceiverNotDispatchEligible("midpoint 2 (c in-flight after b settled)")

	// @deliberate: Release C: the last in-flight upstream settles, R becomes
	// eligible, dispatches, and the frame resolves.
	close(releaseC)
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 30*time.Second),
		"c should re-reach fresh after release")
	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should re-reach fresh after both senders settle")

	// @constraint: R ran exactly once for the whole diamond re-run: never against a
	// half-settled upstream set, and not re-fired per sender.
	require.Eventually(t, func() bool { return countRuns("r") == baselineRuns+1 },
		10*time.Second, 25*time.Millisecond, "r should run exactly once after the last upstream settles")
	// @constraint: Grace window: no straggler second dispatch.
	time.Sleep(1 * time.Second)
	require.Equal(t, baselineRuns+1, countRuns("r"),
		"r must run exactly once per frame, not once per settling sender")
}

// TestSubscriptionCascade_CrossCuttingPositive covers cross-cutting
// (`instance: true`) subscriptions: a monitor node M subscribes to
// "any node failing with error_class=X" across the instance; a sender
// node fails with that class, M wakes via the cascade walker's
// in-tx in-frame path.
func TestSubscriptionCascade_CrossCuttingPositive(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("rate_limited", []byte(`{"hint":"backoff"}`))
	h.Stub.WhenType("monitor").Success(map[string]any{"observed": 1}, true, "mon")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-crosscut-positive", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/rate_limited": {Policy: []node.PolicyAction{{Action: "give_up"}}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "monitor", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance:             true,
					Type:                 "terminal/error/stub/rate_limited",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-crosscut-pos", map[string]any{})
	worker := h.FindNode(iid, "worker")
	monitor := h.FindNode(iid, "monitor")
	require.NotNil(t, worker)
	require.NotNil(t, monitor)

	// @deliberate: Worker reaches failed via give_up; cross-cutting cascade walk
	// stale-marks the monitor in the worker's frame, monitor dispatches
	// and reaches fresh.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 30*time.Second),
		"worker should reach failed via give_up")
	require.True(t, h.WaitForNodeState(monitor.ID, cascade.NodeStateFresh, 30*time.Second),
		"monitor should reach fresh after cross-cutting cascade fires")
}

// TestSubscriptionCascade_CrossCuttingNegative covers the no-coupling
// baseline: a monitor with NO subscription to the worker does not
// dispatch when the worker transitions. Asserts the inverse-edge map
// honors absence — no orphan-wake from an unrelated sender.
//
// Note: per the pessimistic-invalidate rule and `concept:wait-set`
// invariant "Bulk-delete on sender resolution covers every topic kind
// uniformly," a cross-cutting state subscription would fire on every
// state transition (filter matching is observation-time, not
// insertion-time). The genuine negative case is simply absence of any
// subscription edge tying the monitor to the worker.
func TestSubscriptionCascade_CrossCuttingNegative(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "w-ok")
	h.Stub.WhenType("monitor").Success(map[string]any{"observed": 1}, true, "mon")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-crosscut-negative", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
			// @deliberate: Monitor declares no subscriptions: it's a stand-alone
			// root node. Its initial frame will fire it once, then
			// nothing the worker does should re-fire it.
			scenario.MakeNode(node.TemplateNodeDef{Type: "monitor", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-crosscut-neg", map[string]any{})
	worker := h.FindNode(iid, "worker")
	monitor := h.FindNode(iid, "monitor")
	require.NotNil(t, worker)
	require.NotNil(t, monitor)

	// @deliberate: Both nodes complete their initial frame; record monitor's
	// terminal-complete count so a future re-dispatch is detectable.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should reach fresh")
	require.True(t, h.WaitForNodeState(monitor.ID, cascade.NodeStateFresh, 30*time.Second),
		"monitor should reach fresh from its own initial frame")

	// @deliberate: Snapshot the monitor's ledger before the invalidate (it ran once
	// in the initial frame). The steady-state sampler below can miss a
	// spurious re-run that starts and settles between samples — run
	// rows leave the in-flight phase set at terminal — so quiescence is
	// additionally asserted on the append-only event log afterwards
	// (2026-06-11 polling audit).
	monitorID := monitor.ID
	monitorDispatchEvents := func() int {
		return len(eventwait.Events(h.Ctx, t, h.Persist,
			eventwait.Matcher{NodeID: &monitorID, Kind: "work_started", KindPrefix: "terminal/"}))
	}
	monitorEventsBefore := monitorDispatchEvents()

	// @constraint: Invalidate worker; monitor MUST NOT re-fire because no edge
	// connects them.
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "w-ok-2")
	h.InvalidateNode(iid, worker.ID)

	// @constraint: Worker re-reaches fresh.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should re-reach fresh")

	// @constraint: Monitor must stay fresh; never transition to running. Allow
	// 3 seconds for any spurious cascade. Post-stage-3: state lives on
	// the in-flight run row; no row = fresh.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var state cascade.NodeState
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT COALESCE(r.state, 'fresh')
			   FROM rimsky_nodes n
			   LEFT JOIN rimsky_node_runs r
			          ON r.node_id = n.id
			         AND r.phase IN ('pending','active','held','parked')
			  WHERE n.id = $1`, monitor.ID).Scan(&state)
		require.NoError(t, err)
		if state != cascade.NodeStateFresh {
			t.Fatalf("monitor should remain fresh (no subscription edge to worker); observed state=%s", state)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// @constraint: Durable-record check: the monitor gained no dispatch/terminal
	// events across the window — a transient run that slipped between
	// samples cannot hide from the append-only ledger.
	require.Equal(t, monitorEventsBefore, monitorDispatchEvents(),
		"monitor must gain no dispatch/terminal events from the worker invalidate (no subscription edge)")
}

// TestSubscriptionCascade_FrameEndCleansWaitSet verifies that after
// the frame ends, every wait-set row is drained (drained_at IS NOT NULL).
// Under per-run keying (2026-05-20), drain MARKS rows rather than
// deleting them — the substitution-context builder reads drained rows
// to populate the receiver's Deps map. The eligibility predicate
// updates to "no rows with drained_at IS NULL," so post-frame the
// invariant we care about is "no undrained rows" (not "no rows").
// Frame-level cleanup happens via the ON DELETE CASCADE relationship
// with rimsky_frames when frames are eventually pruned for retention.
func TestSubscriptionCascade_FrameEndCleansWaitSet(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("r").Success(map[string]any{"r": 1}, true, "r")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-frame-end-clean", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-frame-end-clean", map[string]any{})
	r := h.FindNode(iid, "r")
	require.NotNil(t, r)

	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should reach fresh after initial settle")

	// @constraint: After the frame closes, every wait-set row for that instance's
	// frames must have drained_at IS NOT NULL. Allow up to 5 seconds
	// for frame-end detection to land.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var undrained int
		err := h.Pool.QueryRow(h.Ctx, `
			SELECT count(*) FROM rimsky_wait_set w
			 JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
			 JOIN rimsky_nodes n ON n.id = r.node_id
			 WHERE n.instance_id = $1
			   AND w.drained_at IS NULL
		`, iid).Scan(&undrained)
		require.NoError(t, err)
		if undrained == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	var leftover int
	err := h.Pool.QueryRow(h.Ctx, `
		SELECT count(*) FROM rimsky_wait_set w
		 JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
		 JOIN rimsky_nodes n ON n.id = r.node_id
		 WHERE n.instance_id = $1
		   AND w.drained_at IS NULL
	`, iid).Scan(&leftover)
	require.NoError(t, err)
	require.Equal(t, 0, leftover,
		"every wait-set row should be drained (drained_at IS NOT NULL) by frame end")
}

// TestSubscriptionCascade_SelfCycleAdvances covers the
// "drain my own queue" idiom: a node with
// `subscribes: { node: <self-type>, type: terminal/success,
// when: payload.changed }` re-fires after every fresh_changed-equivalent
// commit. The cascade walker's insert-then-drain-in-same-tx pattern
// keeps iteration inside the current frame — the supervisor picks up
// each new pending self-run as it lands.
//
// Asserts:
//   - the node is dispatched at least twice (initial + at least one
//     self-cycle iteration);
//   - the cycle terminates cleanly when the stub flips to
//     changed=false (the receiver-side CEL `when: payload.changed`
//     filter prevents unchanged commits from re-firing the self-edge).
func TestSubscriptionCascade_SelfCycleAdvances(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("drain").Success(map[string]any{"k": 1}, true, "changed")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-self-cycle", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "drain", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "drain",
					Type:                 "terminal/success",
					When:                 "payload.changed",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-self-cycle", map[string]any{})
	d := h.FindNode(iid, "drain")
	require.NotNil(t, d)

	// @constraint: Self-cycle should produce multiple dispatches in the same frame.
	require.Eventually(t, func() bool {
		return len(h.Stub.Observed()) >= 2
	}, 10*time.Second, 25*time.Millisecond,
		"self-subscription must re-fire the node after a fresh_changed commit")

	// @deliberate: Flip the stub: subsequent commits report changed=false → the
	// receiver-side CEL `when: payload.changed` predicate evaluates
	// false against the `terminal/success` envelope → the self-
	// subscription doesn't fire → no new pending self-run → frame
	// ends, node settles at fresh.
	h.Stub.WhenType("drain").Success(map[string]any{"k": 2}, false, "no_change")
	require.True(t, h.WaitForNodeState(d.ID, cascade.NodeStateFresh, 30*time.Second),
		"node should settle at fresh once the stub stops reporting changed=true")

	var prev int
	require.Eventually(t, func() bool {
		now := len(h.Stub.Observed())
		stable := now == prev
		prev = now
		return stable
	}, 5*time.Second, 200*time.Millisecond,
		"dispatch count should stabilize after the changed=false flip")
}
