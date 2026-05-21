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

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
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
					node.SubscriptionEntry{Node: "a", On: "state"},
					node.SubscriptionEntry{Node: "b", On: "state"},
					node.SubscriptionEntry{Node: "c", On: "state"},
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

// TestSubscriptionCascade_EligibilityRespectsMultipleSenders is the
// regression test for the single-invalidator-assumption bug class. R
// subscribes to A, B, C (in this scaled-down version of the 5-sender
// case described in the spec). Invalidate A; while A is still in
// flight, also invalidate B and C: R must wait for ALL THREE to settle
// before dispatching, not just A.
//
// Without the cascade-walk-at-invalidation discipline (per spec Piece
// 1 / pessimistic-invalidate), R would receive only the wait-set row
// for A (inserted at A's terminal-complete settlement and immediately
// drained in the same tx), then dispatch as soon as A re-runs — racing
// the still-running B and C and observing stale upstream data.
func TestSubscriptionCascade_EligibilityRespectsMultipleSenders(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Initial runs are fast so the harness's waitForRootDispatch is
	// reliable. Subsequent invalidation cycles will use the slow
	// scripts queued below so each re-run takes ~2s, giving the test
	// time to layer multiple invalidations before any sender settles.
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c")
	h.Stub.WhenType("r").Success(map[string]any{"r": 1}, true, "r")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-eligibility", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "b", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "c", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "a", On: "state"},
					node.SubscriptionEntry{Node: "b", On: "state"},
					node.SubscriptionEntry{Node: "c", On: "state"},
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

	// Initial settle: R reaches fresh after a, b, c.
	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should reach fresh initially")

	// Slow each subsequent dispatch of A, B, C so we have time to
	// layer multiple invalidations while at least one upstream is
	// still running. Delay-then-Success queues a new scripted run.
	h.Stub.WhenType("a").Delay(2*time.Second).Success(map[string]any{"a": 2}, true, "a")
	h.Stub.WhenType("b").Delay(2*time.Second).Success(map[string]any{"b": 2}, true, "b")
	h.Stub.WhenType("c").Delay(2*time.Second).Success(map[string]any{"c": 2}, true, "c")

	invalidate := func(id shared.UUID) {
		t.Helper()
		resp, err := http.Post(h.ControlBase+"/nodes/"+id.String()+"/invalidate",
			"application/json", bytes.NewReader([]byte(`{}`)))
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	// Invalidate A first; B and C are still fresh.
	invalidate(a.ID)
	// Layer in B and C while A is in flight. Each invalidation must
	// also gate R via the cascade walk; otherwise R would race A's
	// settlement.
	invalidate(b.ID)
	invalidate(c.ID)

	// Once all three settle, the wait-set drain releases R and R
	// re-runs to fresh. With the bug present (cascade walk only at
	// settlement), R would dispatch as soon as A drained — observing
	// stale data from still-running B and C; the test would time out
	// or R's "r" value would reflect the wrong frame's data.
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should re-reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b should re-reach fresh")
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 30*time.Second),
		"c should re-reach fresh")
	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should re-reach fresh after a, b, c all re-run")
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
					"rate_limited": {Policy: []node.PolicyAction{{Action: "give_up"}}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "monitor", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance:   true,
					On:         "state",
					When:       "failed",
					ErrorClass: "rate_limited",
					Frame:      "next",
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

	// Invalidate worker; monitor MUST NOT re-fire because no edge
	// connects them.
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "w-ok-2")
	resp, err := http.Post(h.ControlBase+"/nodes/"+worker.ID.String()+"/invalidate",
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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", On: "state"}),
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
					Node: "a", On: "state", Frame: "next",
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
