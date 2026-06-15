// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executable proof for STORY-cascade-signal-blind.
//
// Drives the real assembled stack (control-api + scheduler + supervisor +
// stub-executor + Postgres via testcontainers) through every cascade-firing
// signal type in the canonical taxonomy — `terminal/success`,
// `terminal/error/<class>` (give_up flavor and pass flavor),
// `transient/retry/<n>/<class>`, `attribute/<key>/changed`, `event/<name>` —
// and asserts that (a) a subscriber dispatches for each, AND (b) the audit
// row lands in `rimsky_events`.
//
// Per-sender (`{ node: X, type: ... }`) and cross-cutting (`instance: true`)
// subscription shapes are exercised for the terminal/success +
// terminal/error/<class> rows. Per-sender alone for transient/retry,
// attribute/<key>/changed, and event/<name>, because those are emitted
// inside the runtime against a specific sender-run and a per-sender shape
// is sufficient to prove the signal-blind code-path. Both `give_up` and
// `pass` variants of `terminal/error/<class>` are exercised — the per-sender
// `terminal/error/*` row is the regression close for GH issue #15 (a
// per-sender error-subscription silently skipped on v0.6.0).
//
// Load-bearing property: cascade signal-blindness — drive REAL settlements
// through the runtime, do not hand-inject signals.
//
//	@story: cascade-signal-blind

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

// waitForNodeStateNoEvent polls the node row until the state matches, with
// no `terminal/success` event-row requirement. The harness's
// WaitForNodeState requires a terminal/success row when waiting on fresh —
// fine for the success path, wrong for `pass`-settled fresh (where the
// settling signal is terminal/error/<class>, not terminal/success). Used by
// the pass-flavored terminal/error rows.
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

// TestCascadeSignalBlind_E2E drives the cascade signal-blindness story
// via real settlements through the runtime. Each subtest exercises one
// (signal-type, subscription-shape) row by:
//
//  1. Deploying a fresh template with a sender configured to produce the
//     signal and a receiver subscribed to a matching type-path.
//  2. Creating an instance and waiting for the receiver to reach `fresh`
//     (the cascade-fire observable: the receiver dispatched in response
//     to the signal).
//  3. Asserting the audit row for the signal lands in `rimsky_events`
//     keyed on the sender node id.
//
// Drive-the-real-runtime: every row uses h.Stub.WhenType + the harness's
// real control-api / scheduler / supervisor chain; no hand-injected
// signals.
func TestCascadeSignalBlind_E2E(t *testing.T) {
	t.Parallel()

	t.Run("terminal_success__per_sender", testCascadeTerminalSuccessPerSender)
	t.Run("terminal_success__cross_cutting", testCascadeTerminalSuccessCrossCutting)

	// @constraint: The GH issue #15 regression close: a per-sender `terminal/error/*`
	// subscription must dispatch when the sender settles
	// terminal/error/<class>.
	t.Run("terminal_error_giveup__per_sender", testCascadeTerminalErrorGiveUpPerSender)
	t.Run("terminal_error_giveup__cross_cutting", testCascadeTerminalErrorGiveUpCrossCutting)

	t.Run("terminal_error_pass__per_sender", testCascadeTerminalErrorPassPerSender)
	t.Run("terminal_error_pass__cross_cutting", testCascadeTerminalErrorPassCrossCutting)

	t.Run("transient_retry__per_sender", testCascadeTransientRetryPerSender)

	t.Run("attribute_changed__per_sender", testCascadeAttributeChangedPerSender)

	t.Run("event_named__per_sender", testCascadeEventNamedPerSender)
}

// @deliberate: terminal/success

func testCascadeTerminalSuccessPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Success(map[string]any{"k": 1}, true, "ok")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-success-per-sender", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "sender", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "terminal/success",
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
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "sender", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance: true, Type: "terminal/success", Frame: "next",
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
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"cross-cutting (instance:true) terminal/success subscriber must dispatch")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/success", 10*time.Second),
		"audit row for terminal/success must land in rimsky_events")
}

// @deliberate: terminal/error/<class> give_up flavor

// testCascadeTerminalErrorGiveUpPerSender — REGRESSION CLOSE for GH issue
// #15. A per-sender subscription on `terminal/error/*` MUST dispatch when
// the sender settles `terminal/error/<class>` via `error_types: give_up`.
//
// Per the plan's necessity rule, if this fails on the current code despite
// the post-v0.6.0 signal-emit refactor at commit 6088bb0, the implementer
// fixes the underlying code in the same pass.
func testCascadeTerminalErrorGiveUpPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: Stub auto-prefixes single-segment classes with `stub/` at emit time,
	// so the wire error_class is `stub/giveup_class`.
	h.Stub.WhenType("sender").Error("giveup_class", map[string]any{"hint": "fail"})
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-error-giveup-per-sender", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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
				// @deliberate: Trailing-* prefix shape per the plan's regression-close
				// requirement. Per-sender (Node: "sender") fires only on
				// signals emitted by the named sender.
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "terminal/error/*",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-err-giveup-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	// @deliberate: give_up settles the sender failed; the receiver still dispatches
	// because the subscription wildcard-matches terminal/error/*.
	require.True(t, h.WaitForNodeState(sender.ID, cascade.NodeStateFailed, 30*time.Second),
		"sender should settle failed under give_up")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender terminal/error/* subscriber must dispatch on the sender's give_up settlement (GH #15 regression close)")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/error/stub/giveup_class", 10*time.Second),
		"audit row for terminal/error/<class> must land in rimsky_events")
}

func testCascadeTerminalErrorGiveUpCrossCutting(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Error("giveup_class_cc", map[string]any{"hint": "fail"})
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-error-giveup-cross-cutting", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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
				// @deliberate: Per the plan: `instance: true` + exact
				// `terminal/error/<class>` for the cross-cutting variant.
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance: true,
					Type:     "terminal/error/stub/giveup_class_cc",
					Frame:    "next",
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
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-err-pass-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	// @constraint: pass settles the sender fresh — the wire signal is still
	// terminal/error/<class>; the receiver wildcards on it. Use the
	// no-event waiter for the sender because pass-settled fresh carries a
	// terminal/error/<class> signal rather than terminal/success (the
	// harness's WaitForNodeState requires the terminal/success row when
	// waiting on fresh).
	require.True(t, waitForNodeStateNoEvent(h, sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle fresh under pass (the wire signal is still terminal/error/<class>)")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender terminal/error/* subscriber must dispatch on the sender's pass settlement")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/error/stub/pass_class", 10*time.Second),
		"audit row for terminal/error/<class> must land in rimsky_events under pass")
}

func testCascadeTerminalErrorPassCrossCutting(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("sender").Error("pass_class_cc", map[string]any{"hint": "absolve"})
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-error-pass-cross-cutting", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "sender",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/pass_class_cc": {Policy: []node.PolicyAction{{Action: "pass"}}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance: true,
					Type:     "terminal/error/stub/pass_class_cc",
					Frame:    "next",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-err-pass-cc", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	require.True(t, waitForNodeStateNoEvent(h, sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle fresh under pass")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"cross-cutting terminal/error/<class> subscriber must dispatch under pass")
	require.True(t, h.WaitForEventKind(sender.ID, "terminal/error/stub/pass_class_cc", 10*time.Second),
		"audit row for terminal/error/<class> must land in rimsky_events under pass")
}

// @deliberate: transient/retry/<n>/<class>

func testCascadeTransientRetryPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: Sender errors persistently; the retry policy fires
	// transient/retry/<n>/stub/flaky for each retry, then falls through to
	// give_up so the test ends deterministically.
	h.Stub.WhenType("sender").Error("flaky", map[string]any{"hint": "transient"})
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-retry-per-sender", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "sender",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/flaky": {Policy: []node.PolicyAction{
						{Action: "retry", Count: 2, BaseDelayMs: 50},
						{Action: "give_up"},
					}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				// @deliberate: Receiver subscribes on transient/retry/* with frame:next
				// so each retry emit opens a fresh frame for the receiver
				// and it dispatches in that frame. (frame:in would gate the
				// receiver on the same frame as the still-retrying sender;
				// the wait-set drain only fires on terminal settlement, not
				// on transient/retry, so the receiver would stay gated
				// throughout the retry window.)
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "transient/retry/*", Frame: "next",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-retry-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	// @constraint: Each retry emits a transient/retry/<n>/<class> signal that fires
	// the receiver. The receiver reaches fresh on its first dispatch from
	// any retry emit.
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender transient/retry/* subscriber must dispatch on the sender's retry emit")

	// @constraint: Audit row for at least one transient/retry/<n>/<class> emit must
	// land. Use the cousin pattern: poll rimsky_events directly because
	// the kind carries an attempt counter we don't pin numerically.
	require.Eventually(t, func() bool {
		var count int
		h.QueryRowSQL(
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'transient/retry/%'`,
			[]any{sender.ID},
			&count,
		)
		return count > 0
	}, 10*time.Second, 50*time.Millisecond,
		"audit row for transient/retry/<n>/<class> must land in rimsky_events")
}

// @deliberate: attribute/<key>/changed

func testCascadeAttributeChangedPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: Sender's Success emits an attributes_delta with key `score`. The
	// runtime's applyTerminalComplete walks t.AttributesDel and emits a
	// per-key attribute/<key>/changed signal — both cascade (in-tx) and
	// audit (post-commit).
	h.Stub.WhenType("sender").Success(map[string]any{"score": 42}, true, "scored")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-attribute-changed-per-sender", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "sender", Executor: "stub"},
				// @deliberate: Declare the attribute key on the sender so the cross-
				// check accepts a downstream subscription / substitution
				// referencing it.
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
		"sender should settle fresh and emit attribute/score/changed")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender attribute/<key>/changed subscriber must dispatch")
	require.True(t, h.WaitForEventKind(sender.ID, "attribute/score/changed", 10*time.Second),
		"audit row for attribute/score/changed must land in rimsky_events")
}

// @deliberate: event/<name>

func testCascadeEventNamedPerSender(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: `ready` is declared in the stub's DeclaredEvents so the validator
	// accepts a downstream `event/ready` subscription against the stub
	// executor.
	h.Stub.WhenType("sender").
		EmitNamedEvent("ready", []byte(`{"go":true}`)).
		Success(map[string]any{"k": 1}, true, "done")
	h.Stub.WhenType("receiver").Success(map[string]any{"r": 1}, true, "rcv")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-signal-blind-event-named-per-sender", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "sender", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sender", Type: "event/ready",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-csb-event-ps", map[string]any{})

	sender := h.FindNode(iid, "sender")
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, sender)
	require.NotNil(t, receiver)

	require.True(t, h.WaitForNodeState(sender.ID, cascade.NodeStateFresh, 30*time.Second),
		"sender should settle and emit event/ready")
	require.True(t, h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 30*time.Second),
		"per-sender event/<name> subscriber must dispatch")
	require.True(t, h.WaitForEventKind(sender.ID, "event/ready", 10*time.Second),
		"audit row for event/ready must land in rimsky_events")
}
