// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executable proof for STORY-cascade-signal-blind.
//
// Drives the real assembled stack (control-api + scheduler +
// supervisor + stub-executor + Postgres via testcontainers) through
// every cascade-firing signal type in the post-collapse taxonomy —
// `terminal/success`, `terminal/error/<class>` (give_up and pass
// flavors), `attribute/<key>/changed` — plus the new
// `terminal/success` + `when: "<tag>" in payload.tags` CEL row that
// replaces the retired `event/<name>` signal under
// TD-collapse-named-event-to-tags.
//
// For each row asserts (a) a subscriber dispatches in response to
// the signal, AND (b) the audit row lands in `rimsky_events`. The
// per-sender (`{ node: X, type: ... }`) and cross-cutting (`instance:
// true`) subscription shapes are exercised for the terminal/success
// + terminal/error rows; per-sender alone for attribute/<key>/changed
// and the tag CEL row. Trailing-`*` prefix shapes (`terminal/error/*`)
// are exercised on the error rows. The `transient/retry/<n>/<class>`
// signal is exercised by the retry-after-error scratch round-trip in
// `code:test/scenarios/scratch_round_trip_e2e_test.go::TestScratchRoundTripE2E_RetryAfterError`,
// which drives a real retry chain end to end; we don't duplicate
// here.
//
// Load-bearing property: cascade signal-blindness — drive REAL
// settlements through the runtime; no hand-injected signals. Per
// TD-collapse-named-event-to-tags the `event/<name>` row is dropped
// entirely and replaced by the `terminal/success + tags` CEL row.
//
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
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @deliberate: waitForNodeStateNoEvent polls the node row until the
// state matches, with no `terminal/success` event-row requirement.
// The harness's WaitForNodeState requires a terminal/success row when
// waiting on fresh — fine for the success path, wrong for `pass`-
// settled fresh (where the settling signal is terminal/error/<class>,
// not terminal/success). Used by the pass-flavored terminal/error
// rows.
func waitForNodeStateNoEvent(h *scenario.Harness, nodeID shared.UUID, state cascade.NodeState, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n *persistence.NodeRow
		_ = h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(ctx, nodeID, tx)
			n = r
			return err
		})
		if n != nil && n.State == state {
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

	// @constraint: Post TD-collapse-named-event-to-tags the
	// `event/<name>` taxonomy row retires; subscribers express tag
	// interest via CEL filters over `payload.tags` on the terminal/*
	// signal. This row pins the new shape.
	t.Run("terminal_success_with_tag_filter__per_sender", testCascadeTerminalSuccessWithTagFilterPerSender)
}

// @deliberate: terminal/success per-sender exact

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
					WakeOnChange:         node.BoolPtr(true),
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

// @deliberate: terminal/success cross-cutting exact

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
					WakeOnChange:         node.BoolPtr(true),
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

// @deliberate: terminal/error/<class> give_up flavor

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
					"stub/giveup_class": {Policy: []node.PolicyAction{{Action: "give_up"}}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				// @deliberate: Trailing-* prefix shape — per-sender
				// `terminal/error/*` fires on any terminal/error/<class>
				// signal from the named sender.
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "terminal/error/*",
					WakeOnChange:         node.BoolPtr(true),
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
					"stub/giveup_class_cc": {Policy: []node.PolicyAction{{Action: "give_up"}}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance:             true,
					Type:                 "terminal/error/stub/giveup_class_cc",
					WakeOnChange:         node.BoolPtr(true),
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

// @deliberate: terminal/error/<class> pass flavor

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
					"stub/pass_class": {Policy: []node.PolicyAction{{Action: "pass"}}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "terminal/error/*",
					WakeOnChange:         node.BoolPtr(true),
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

	// @constraint: pass settles the sender fresh — the wire signal is
	// still terminal/error/<class>; the receiver wildcards on it.
	require.True(t, waitForNodeStateNoEvent(h, sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle fresh under pass (the wire signal is still terminal/error/<class>)")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender terminal/error/* subscriber must dispatch on the sender's pass settlement")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/error/stub/pass_class", 10*time.Second),
		"audit row for terminal/error/<class> must land in rimsky_events under pass")
}

// @deliberate: attribute/<key>/changed

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
					WakeOnChange:         node.BoolPtr(true),
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

// @deliberate: terminal/success + CEL `when:` tag filter — the post-
// collapse replacement for the retired `event/<name>` taxonomy row.
// The subscriber's `when: "<tag>" in payload.tags` predicate gates the
// fire on the sender's settling outcome carrying that tag.

func testCascadeTerminalSuccessWithTagFilterPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: Sender's Success carries an explicit `ready` tag on
	// the terminal verdict; the subscriber's CEL `when:` predicate
	// gates on its presence in payload.tags.
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
					WakeOnChange:         node.BoolPtr(true),
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
