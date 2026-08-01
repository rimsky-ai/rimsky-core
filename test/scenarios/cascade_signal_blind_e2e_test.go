// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: cascade-signal-blind
// @concept: cascade
// @concept: terminal-tag

package scenarios

import (
	"context"
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

func waitForNodeStateNoEvent(h *scenario.Harness, nodeID shared.UUID, state cascade.NodeState) {
	for {
		var latest *persistence.NodeRunLatest
		_ = h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(ctx, nodeID, tx)
			latest = r
			return err
		})
		if latest != nil && latest.State == state {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCascadeSignalBlind_E2E(t *testing.T) {
	t.Parallel()

	t.Run("terminal_success__per_sender", testCascadeTerminalSuccessPerSender)

	t.Run("terminal_error_giveup__per_sender_prefix", testCascadeTerminalErrorGiveUpPerSender)

	t.Run("terminal_error_pass__per_sender_prefix", testCascadeTerminalErrorPassPerSender)

	t.Run("attribute_changed__per_sender", testCascadeAttributeChangedPerSender)
	t.Run("attribute_changed__diff_gate", testCascadeAttributeChangedDiffGate)

	t.Run("terminal_success_with_tag_filter__per_sender", testCascadeTerminalSuccessWithTagFilterPerSender)
	t.Run("terminal_success_with_tag_filter__absent_does_not_fire__per_sender", testCascadeTerminalSuccessWithTagFilterAbsentPerSender)
}

func testCascadeTerminalSuccessPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Success(map[string]any{"k": 1}, true, "ok")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-success-per-sender", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "sender", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-success-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	h.WaitForNodeState(sender.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh)
	h.WaitForEventCount(sender.ID, "terminal/success", 1)
}

func testCascadeTerminalErrorGiveUpPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Error("giveup_class", map[string]any{"hint": "fail"})
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-error-giveup-per-sender", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "sender",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/giveup_class": {Action: "give_up"},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "terminal/error/*",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-err-giveup-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	h.WaitForNodeState(sender.ID, cascade.NodeStateFailed)
	h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh)
	h.WaitForEventCount(sender.ID, "terminal/error/stub/giveup_class", 1)
}

func testCascadeTerminalErrorPassPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Error("pass_class", map[string]any{"hint": "absolve"})
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-error-pass-per-sender", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "sender",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/pass_class": {Action: "pass"},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "terminal/error/*",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-err-pass-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	waitForNodeStateNoEvent(h, sender.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh)
	h.WaitForEventCount(sender.ID, "terminal/error/stub/pass_class", 1)
}

func testCascadeAttributeChangedPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Success(map[string]any{"score": 42}, true, "scored")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-attribute-changed-per-sender", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "sender", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"score": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "attribute/score/changed",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-attr-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	h.WaitForNodeState(sender.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh)
	h.WaitForEventCount(sender.ID, "attribute/score/changed", 1)
}

func testCascadeAttributeChangedDiffGate(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").
		Success(map[string]any{"counter": 1, "score": 42}, true, "r1").
		Then().Success(map[string]any{"counter": 2, "score": 42}, true, "r2").
		Then().Success(map[string]any{"counter": 3, "score": 42}, true, "r3")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-attr-diff-gate", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "sender", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "test/wake", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "sender", Type: "attribute/counter/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"counter": map[string]any{"type": "integer"},
						"score":   map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "attribute/score/changed",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-attr-diff", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	countByType := func(nodeType string) int {
		n := 0
		for _, o := range h.Stub.Observed() {
			if o.NodeType == nodeType {
				n++
			}
		}
		return n
	}

	h.PostInstanceMessage(iid, "test/wake", nil, "diff-gate-kick")

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if countByType("sender") >= 3 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	require.GreaterOrEqual(t, countByType("sender"), 3,
		"sender must re-fire via its self-edge on attribute/counter/changed for three rounds "+
			"(round 1: default→{counter:1,score:42}; round 2: counter 1→2; round 3: counter 2→3); "+
			"the fourth round has no diff so cascade stops. got %d", countByType("sender"))

	time.Sleep(3 * time.Second)

	require.Equal(t, 1, countByType("receiver"),
		"diff-gate: receiver subscribes to sender's attribute/score/changed. score stays at 42 across "+
			"every round, so the signal fires exactly once (default→42 on round 1); receiver must run "+
			"exactly once. If receiver > 1, the diff-gate is emitting attribute/<key>/changed on "+
			"no-change re-settles. If receiver == 0, the walker is failing to fan a signal to a "+
			"foreign-edge subscriber when the sender also has a self-edge on a different signal in "+
			"the same terminal fire. got %d", countByType("receiver"))
}

func testCascadeTerminalSuccessWithTagFilterPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Success(map[string]any{"k": 1}, true, "ok").Tags("ready")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-tag-filter-per-sender", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "sender", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "sender",
					Type:                 "terminal/success",
					When:                 `"ready" in payload.tags`,
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-tag-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	h.WaitForNodeState(sender.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh)
	h.WaitForEventCount(sender.ID, "terminal/success", 1)
}

func testCascadeTerminalSuccessWithTagFilterAbsentPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Success(map[string]any{"k": 1}, true, "ok")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-tag-filter-absent-per-sender", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "sender", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "sender",
					Type:                 "terminal/success",
					When:                 `"ready" in payload.tags`,
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-tag-absent-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	h.WaitForNodeState(sender.ID, cascade.NodeStateFresh)
	h.WaitForEventCount(sender.ID, "terminal/success", 1)

	var receiverRuns int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{receiver.ID}, &receiverRuns)
	require.Zero(t, receiverRuns,
		"receiver's when: filter requires \"ready\" in payload.tags and the sender settled with no "+
			"tags, so the tag-absent leg must suppress the wake; the filter is evaluated in the sender's "+
			"settle transaction and a broken-open filter would create the receiver's run atomically with "+
			"the sender's terminal/success, so any receiver node-run after the sender settles means the "+
			"filter fired on an absent tag; got %d run(s)", receiverRuns)
	require.False(t, h.HasEventKind(receiver.ID, "terminal/success"),
		"receiver must NOT dispatch (no terminal/success audit row) when the sender's settling tags "+
			"do not carry \"ready\"")
}
