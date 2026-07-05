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
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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
					node.SubscriptionEntry{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "b", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
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

func TestSubscriptionCascade_EligibilityRespectsMultipleSenders(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{"c": 1}, true, "c")
	h.Stub.WhenType("r").Success(map[string]any{"r": 1}, true, "r")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-eligibility", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/a"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/a", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "r", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "b", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
					node.SubscriptionEntry{Node: "c", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-eligibility-multi", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	c := h.FindNode(iid, "c")
	r := h.FindNode(iid, "r")
	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.NotNil(t, c)
	require.NotNil(t, r)

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

	releaseB := make(chan struct{})
	releaseC := make(chan struct{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 2}, true, "a")
	h.Stub.WhenType("b").HoldUntil(releaseB).Success(map[string]any{"b": 2}, true, "b")
	h.Stub.WhenType("c").HoldUntil(releaseC).Success(map[string]any{"c": 2}, true, "c")
	h.Stub.WhenType("r").Success(map[string]any{"r": 2}, true, "r")

	h.PostInstanceMessage(iid, "test/wake/a", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should re-reach fresh")

	require.Eventually(t, func() bool {
		return countRuns("b") >= 2 && countRuns("c") >= 2
	}, 30*time.Second, 25*time.Millisecond, "b and c should both dispatch into their holds")

	assertReceiverNotDispatchEligible := func(midpoint string) {
		t.Helper()
		deadline := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(deadline) {
			var ineligibleRowViolations int
			h.QueryRowSQL(
				`SELECT COUNT(*) FROM rimsky_node_runs
				  WHERE node_id = $1
				    AND state IN ('pending','stale','running','held','parked')
				    AND (claimed_by IS NOT NULL OR state <> 'pending')`,
				[]any{r.ID}, &ineligibleRowViolations)
			require.Zerof(t, ineligibleRowViolations,
				"%s: r's run row was claimed or transitioned out of pending while a subscribed upstream was in-flight", midpoint)
			require.Equalf(t, baselineRuns, countRuns("r"),
				"%s: r dispatched while a subscribed upstream was in-flight", midpoint)
			time.Sleep(50 * time.Millisecond)
		}
	}

	assertReceiverNotDispatchEligible("midpoint 1 (b and c in-flight)")

	close(releaseB)
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b should re-reach fresh after release")

	assertReceiverNotDispatchEligible("midpoint 2 (c in-flight after b settled)")

	close(releaseC)
	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 30*time.Second),
		"c should re-reach fresh after release")
	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should re-reach fresh after both senders settle")

	require.Eventually(t, func() bool { return countRuns("r") == baselineRuns+1 },
		10*time.Second, 25*time.Millisecond, "r should run exactly once after the last upstream settles")
	time.Sleep(1 * time.Second)
	require.Equal(t, baselineRuns+1, countRuns("r"),
		"r must run exactly once per frame, not once per settling sender")
}

func TestSubscriptionCascade_TerminalErrorPrefixMatchesPerSender(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("rate_limited", []byte(`{"hint":"backoff"}`))
	h.Stub.WhenType("monitor").Success(map[string]any{"observed": 1}, true, "mon")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-terminal-error-prefix", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/rate_limited": {Action: "give_up"},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "monitor", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "worker",
					Type:                 "terminal/error/stub/rate_limited",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-term-err-prefix", map[string]any{})
	worker := h.FindNode(iid, "worker")
	monitor := h.FindNode(iid, "monitor")
	require.NotNil(t, worker)
	require.NotNil(t, monitor)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 30*time.Second),
		"worker should reach failed via give_up")
	require.True(t, h.WaitForNodeState(monitor.ID, cascade.NodeStateFresh, 30*time.Second),
		"monitor should reach fresh after per-sender terminal/error/<class> cascade fires")
}

func TestSubscriptionCascade_UnsubscribedNodeStaysIdle(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "w-ok")
	h.Stub.WhenType("monitor").Success(map[string]any{"observed": 1}, true, "mon")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subscription-cascade-crosscut-negative", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/worker"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/worker", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
			scenario.MakeNode(node.TemplateNodeDef{Type: "monitor", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-crosscut-neg", map[string]any{})
	worker := h.FindNode(iid, "worker")
	monitor := h.FindNode(iid, "monitor")
	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	require.NotNil(t, worker)
	require.NotNil(t, monitor)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should reach fresh")
	require.True(t, h.WaitForNodeState(monitor.ID, cascade.NodeStateFresh, 30*time.Second),
		"monitor should reach fresh on the harness-injected empty wake")

	monitorID := monitor.ID
	monitorDispatchEvents := func() int {
		return len(eventwait.Events(h.Ctx, t, h.Persist,
			eventwait.Matcher{NodeID: &monitorID, Kind: "work_started", KindPrefix: "terminal/"}))
	}
	monitorEventsBefore := monitorDispatchEvents()

	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "w-ok-2")
	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should re-reach fresh")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var state cascade.NodeState
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT COALESCE(r.state, 'fresh')
			   FROM rimsky_nodes n
			   LEFT JOIN rimsky_node_runs r
			          ON r.node_id = n.id
			         AND r.state IN ('pending','stale','running','held','parked')
			  WHERE n.id = $1`, monitor.ID).Scan(&state)
		require.NoError(t, err)
		if state != cascade.NodeStateFresh {
			t.Fatalf("monitor should remain fresh (no subscription edge to worker); observed state=%s", state)
		}
		time.Sleep(100 * time.Millisecond)
	}

	require.Equal(t, monitorEventsBefore, monitorDispatchEvents(),
		"monitor must gain no dispatch/terminal events from the worker invalidate (no subscription edge)")
}

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
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-frame-end-clean", map[string]any{})
	r := h.FindNode(iid, "r")
	require.NotNil(t, r)

	require.True(t, h.WaitForNodeState(r.ID, cascade.NodeStateFresh, 30*time.Second),
		"r should reach fresh after initial settle")

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
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-self-cycle", map[string]any{})
	d := h.FindNode(iid, "drain")
	require.NotNil(t, d)

	require.Eventually(t, func() bool {
		return len(h.Stub.Observed()) >= 2
	}, 10*time.Second, 25*time.Millisecond,
		"self-subscription must re-fire the node after a fresh_changed commit")

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
