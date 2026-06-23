// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func waitForNodeStateNoEvent(h *scenario.Harness, nodeID shared.UUID, state cascade.NodeState, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var latest *persistence.NodeRunLatest
		_ = h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(ctx, tx, nodeID)
			latest = r
			return err
		})
		if latest != nil && latest.State == state {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestCascadeSignalBlind_E2E(t *testing.T) {
	t.Parallel()

	t.Run("terminal_success__per_sender", testCascadeTerminalSuccessPerSender)
	t.Run("terminal_success__cross_cutting", testCascadeTerminalSuccessCrossCutting)

	t.Run("terminal_error_giveup__per_sender_prefix", testCascadeTerminalErrorGiveUpPerSender)
	t.Run("terminal_error_giveup__cross_cutting_exact", testCascadeTerminalErrorGiveUpCrossCutting)

	t.Run("terminal_error_pass__per_sender_prefix", testCascadeTerminalErrorPassPerSender)

	t.Run("attribute_changed__per_sender", testCascadeAttributeChangedPerSender)
	t.Run("attribute_changed__diff_gate", testCascadeAttributeChangedDiffGate)

	t.Run("terminal_success_with_tag_filter__per_sender", testCascadeTerminalSuccessWithTagFilterPerSender)
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

	require.True(t, h.WaitForNodeState(sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle terminal/success")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender terminal/success subscriber must dispatch")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/success", 10*time.Second),
		"audit row for terminal/success must land in rimsky_events")
}

func testCascadeTerminalSuccessCrossCutting(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Success(map[string]any{"k": 1}, true, "ok")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-success-cross-cutting", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "sender", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance: true, Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-success-cc", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	require.True(t, h.WaitForNodeState(sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle terminal/success")
	require.True(t, h.WaitForEventKind(receiver.ID, "terminal/success", 30*time.Second),
		"cross-cutting (instance:true) terminal/success subscriber must dispatch and emit terminal/success")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/success", 10*time.Second),
		"audit row for terminal/success must land in rimsky_events")
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

	require.True(t, h.WaitForNodeState(sender.ID, cascade.NodeStateFailed, 30*time.Second),
		"sender should settle failed under give_up")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender terminal/error/* subscriber must dispatch on the sender's give_up settlement")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/error/stub/giveup_class", 10*time.Second),
		"audit row for terminal/error/<class> must land in rimsky_events")
}

func testCascadeTerminalErrorGiveUpCrossCutting(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Error("giveup_class_cc", map[string]any{"hint": "fail"})
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-error-giveup-cross-cutting", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "sender",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/giveup_class_cc": {Action: "give_up"},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance:             true,
					Type:                 "terminal/error/stub/giveup_class_cc",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-err-giveup-cc", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	require.True(t, h.WaitForNodeState(sender.ID, cascade.NodeStateFailed, 30*time.Second),
		"sender should settle failed under give_up")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"cross-cutting terminal/error/<class> subscriber must dispatch")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/error/stub/giveup_class_cc", 10*time.Second),
		"audit row for terminal/error/<class> must land in rimsky_events")
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

	require.True(t, waitForNodeStateNoEvent(h, sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle fresh under pass (the wire signal is still terminal/error/<class>)")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender terminal/error/* subscriber must dispatch on the sender's pass settlement")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/error/stub/pass_class", 10*time.Second),
		"audit row for terminal/error/<class> must land in rimsky_events under pass")
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

	require.True(t, h.WaitForNodeState(sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle terminal/success")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender attribute/<key>/changed subscriber must dispatch when sender's terminal carries that key in attributes_delta")
	require.True(t, h.WaitForEventKind(sender.ID, "attribute/score/changed", 10*time.Second),
		"audit row for attribute/<key>/changed must land in rimsky_events")
}

func testCascadeAttributeChangedDiffGate(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Success(map[string]any{"score": 42}, true, "scored")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-attr-diff-gate", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "sender", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
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

	h.PostInstanceMessage(iid, "test/wake", nil, "diff-gate-r1")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if countByType("sender") >= 1 && countByType("receiver") >= 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.Equal(t, 1, countByType("sender"), "sender ran once after first wake")
	require.Equal(t, 1, countByType("receiver"),
		"receiver woken exactly once on the first attribute/score/changed (no prior value to diff)")

	h.PostInstanceMessage(iid, "test/wake", nil, "diff-gate-r2")
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if countByType("sender") >= 2 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.Equal(t, 2, countByType("sender"), "sender ran twice across two wakes")

	time.Sleep(3 * time.Second)
	require.Equal(t, 1, countByType("receiver"),
		"diff-gate: receiver must not wake when the upstream re-settles with the same attribute value")
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

	require.True(t, h.WaitForNodeState(sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle terminal/success with the `ready` tag")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender terminal/success+tag-filter subscriber must dispatch when sender's verdict carries `ready` in payload.tags")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/success", 10*time.Second),
		"audit row for terminal/success must land in rimsky_events")
}
