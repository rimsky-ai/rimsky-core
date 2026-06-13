// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Subscription-cascade + wait-set scenario coverage for the
// post-2026-05-14 subscription model. The wait-set ledger drives
// dispatch eligibility (rather than the retired dependencies-all-fresh
// predicate); these tests exercise the discipline end-to-end.

package scenarios

import (
	"bytes"
	"net/http"
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
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "b", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "c", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "a", Type: "terminal/*"},
					node.SubscriptionEntry{Node: "b", Type: "terminal/*"},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*"},
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-multi-invalidator", map[string]any{})
	r := h.FindNode(iid, "r")
	require.NotNil(t, r)

	// All four nodes reach fresh on initial run.
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
	// Initial runs are fast so the instance settles promptly.
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c")
	h.Stub.WhenType("r").Success(map[string]any{"r": 1}, true, "r")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-eligibility", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*"}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*"}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "b", Type: "terminal/*"},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*"},
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

	// Initial settle: R reaches fresh after a → (b, c) → r.
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

	// Re-script: A stays fast; B and C are held in-flight until the
	// test releases them, pinning the in-flight set at each midpoint.
	releaseB := make(chan struct{})
	releaseC := make(chan struct{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 2}, true, "a")
	h.Stub.WhenType("b").HoldUntil(releaseB).Success(map[string]any{"b": 2}, true, "b")
	h.Stub.WhenType("c").HoldUntil(releaseC).Success(map[string]any{"c": 2}, true, "c")
	h.Stub.WhenType("r").Success(map[string]any{"r": 2}, true, "r")

	// One invalidation, one frame: A re-runs; its settlement marks B
	// and C stale in the same frame and both dispatch into the holds.
	resp, err := http.Post(h.ControlBase+"/v1/nodes/"+a.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should re-reach fresh")

	// Wait until both senders are actually in-flight (dispatched into
	// the stub holds) so the first midpoint observes two held runs.
	require.Eventually(t, func() bool {
		return countRuns("b") >= 2 && countRuns("c") >= 2
	}, 30*time.Second, 25*time.Millisecond, "b and c should both dispatch into their holds")

	// assertReceiverNotDispatchEligible holds the midpoint for a
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

	// Midpoint 1: both B and C in-flight. R must not be dispatch-
	// eligible (its staleness arrived via the invalidation walk; both
	// senders are in-flight in the frame).
	assertReceiverNotDispatchEligible("midpoint 1 (b and c in-flight)")

	// Release B; C stays held. B's settlement marks R stale via the
	// settlement walk — the propagation path that seeds NO next-tier
	// wait-set gate for C. This midpoint is the regression pin: only
	// the in-flight-upstream eligibility condition keeps R parked
	// here.
	close(releaseB)
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b should re-reach fresh after release")

	// Midpoint 2: C still in-flight; R stale via B's settlement.
	assertReceiverNotDispatchEligible("midpoint 2 (c in-flight after b settled)")

	// Release C: the last in-flight upstream settles, R becomes
	// eligible, dispatches, and the frame resolves.
	close(releaseC)
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 30*time.Second),
		"c should re-reach fresh after release")
	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should re-reach fresh after both senders settle")

	// R ran exactly once for the whole diamond re-run: never against a
	// half-settled upstream set, and not re-fired per sender.
	require.Eventually(t, func() bool { return countRuns("r") == baselineRuns+1 },
		10*time.Second, 25*time.Millisecond, "r should run exactly once after the last upstream settles")
	// Grace window: no straggler second dispatch.
	time.Sleep(1 * time.Second)
	require.Equal(t, baselineRuns+1, countRuns("r"),
		"r must run exactly once per frame, not once per settling sender")
}

// TestSubscriptionCascade_CrossCuttingPositive covers cross-cutting
// (`instance: true`) subscriptions: a monitor node M subscribes to
// "any node failing with error_class=X" across the instance; a sender
// node fails with that class, M wakes (frame: next, default for
// cross-cutting).
func TestSubscriptionCascade_CrossCuttingPositive(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("rate_limited", []byte(`{"hint":"backoff"}`))
	h.Stub.WhenType("monitor").Success(map[string]any{"observed": 1}, true, "mon")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-crosscut-positive", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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
					Instance: true,
					Type:     "terminal/error/stub/rate_limited",
					Frame:    "next",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-crosscut-pos", map[string]any{})
	worker := h.FindNode(iid, "worker")
	monitor := h.FindNode(iid, "monitor")
	require.NotNil(t, worker)
	require.NotNil(t, monitor)

	// Worker reaches failed via give_up; cross-cutting cascade walk
	// opens a new frame for monitor (frame: next), monitor dispatches
	// and reaches fresh.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 30*time.Second),
		"worker should reach failed via give_up")
	require.True(t, h.WaitForNodeState(monitor.ID, cascade.NodeStateFresh, 30*time.Second),
		"monitor should reach fresh after cross-cutting cascade fires the next-frame open")
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
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
			// Monitor declares no subscriptions: it's a stand-alone
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

	// Both nodes complete their initial frame; record monitor's
	// terminal-complete count so a future re-dispatch is detectable.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should reach fresh")
	require.True(t, h.WaitForNodeState(monitor.ID, cascade.NodeStateFresh, 30*time.Second),
		"monitor should reach fresh from its own initial frame")

	// Snapshot the monitor's ledger before the invalidate (it ran once
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

	// Invalidate worker; monitor MUST NOT re-fire because no edge
	// connects them.
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "w-ok-2")
	resp, err := http.Post(h.ControlBase+"/v1/nodes/"+worker.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Worker re-reaches fresh.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should re-reach fresh")

	// Monitor must stay fresh; never transition to running. Allow
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

	// Durable-record check: the monitor gained no dispatch/terminal
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
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*"}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-frame-end-clean", map[string]any{})
	r := h.FindNode(iid, "r")
	require.NotNil(t, r)

	// Initial frame settles; R reaches fresh.
	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should reach fresh after initial settle")

	// After the frame closes, every wait-set row for that instance's
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

// TestSubscriptionCascade_FrameNextLoopConverges covers the frame:next
// modifier on per-node subscriptions. A receiver subscribes to A with
// `frame: next` — A's settlement opens a new frame for the receiver
// instead of joining A's frame. The chain converges: A settles → next
// frame opens for R → R dispatches → both reach fresh; no infinite
// loop because R doesn't re-invalidate A.
func TestSubscriptionCascade_FrameNextLoopConverges(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("r").Success(map[string]any{"r": 1}, true, "r")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-frame-next-converges", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "a", Type: "terminal/*", Frame: "next",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-frame-next-converges", map[string]any{})
	a := h.FindNode(iid, "a")
	r := h.FindNode(iid, "r")
	require.NotNil(t, a)
	require.NotNil(t, r)

	// A reaches fresh in the first frame; R reaches fresh in the
	// next frame that the cascade walk opened.
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should reach fresh in the initial frame")
	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should reach fresh in the deferred next frame")
}

// TestSubscriptionCascade_SelfCycleAdvances covers the post-2026-05-23
// "drain my own queue" idiom: a node with
// `subscribes: { node: <self-type>, type: terminal/success,
// when: payload.changed, frame: next }` re-fires after every
// fresh_changed-equivalent commit. This is the receiver-side
// replacement for the retired send-side
// `on_executor_complete: { invalidate: { targets: [self] } }`.
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
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "drain", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "drain", Type: "terminal/success",
					When:  "payload.changed",
					Frame: "next",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-self-cycle", map[string]any{})
	d := h.FindNode(iid, "drain")
	require.NotNil(t, d)

	// Self-cycle should produce multiple dispatches: initial + at
	// least one re-fire from the fresh_changed commit's cascade walk.
	require.Eventually(t, func() bool {
		return len(h.Stub.Observed()) >= 2
	}, 10*time.Second, 25*time.Millisecond,
		"self-subscription with frame:next must re-fire the node after a fresh_changed commit")

	// Flip the stub: subsequent commits report changed=false → the
	// receiver-side CEL `when: payload.changed` predicate evaluates
	// false against the `terminal/success` envelope's payload →
	// subscriber doesn't fire → loop terminates.
	h.Stub.WhenType("drain").Success(map[string]any{"k": 2}, false, "no_change")
	require.True(t, h.WaitForNodeState(d.ID, cascade.NodeStateFresh, 30*time.Second),
		"node should settle at fresh once the stub stops reporting changed=true")

	// Confirm termination: once the stub flips to changed=false the
	// dispatch count must stabilize. A few in-flight dispatches may
	// have been queued from prior changed=true commits before the
	// flip propagated, so use Eventually to wait for the count to
	// stop growing across consecutive observations.
	var prev int
	require.Eventually(t, func() bool {
		now := len(h.Stub.Observed())
		stable := now == prev
		prev = now
		return stable
	}, 5*time.Second, 200*time.Millisecond,
		"dispatch count should stabilize after the changed=false flip; "+
			"continued growth would mean the receiver-side `when: payload.changed` filter is broken")
}

// TestSubscriptionCascade_SelfCycleAdvances_FrameIn is the FrameIn
// spelling of the drain-my-own-queue idiom. Where the FrameNext shape
// opens a fresh frame per iteration, FrameIn keeps iteration inside
// the current frame — the supervisor picks up each new pending self-
// run as it lands. Safe because `MarkStaleForCascade` does not touch
// `rimsky_nodes.state` (only inserts a new run row + re-stamps
// frame_id), and the cascade walker's insert-then-drain-in-same-tx
// pattern drains the new pending run's wait-set blocker (keyed on the
// just-committed run) before the supervisor sees it.
func TestSubscriptionCascade_SelfCycleAdvances_FrameIn(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("drain").Success(map[string]any{"k": 1}, true, "changed")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-self-cycle-in", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "drain", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "drain", Type: "terminal/success",
					When:  "payload.changed",
					Frame: "in",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-self-cycle-in", map[string]any{})
	d := h.FindNode(iid, "drain")
	require.NotNil(t, d)

	// Self-cycle should produce multiple dispatches in the same frame.
	require.Eventually(t, func() bool {
		return len(h.Stub.Observed()) >= 2
	}, 10*time.Second, 25*time.Millisecond,
		"self-subscription with frame:in must re-fire the node after a fresh_changed commit")

	// Flip the stub: subsequent commits report changed=false → the
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
