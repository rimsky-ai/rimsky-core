// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executable proof for STORY-read-without-waking
// (spec 2026-06-14-explicit-substitution-cascade-behavior).
//
// As a template author, I can read an upstream's attribute via
// substitution while declaring on the covering subscription that the
// read does not fire my receiver on the sender's change. Two scenarios:
//
//  1. The receiver A has subscription (wake_on_change: false) covering
//     context-sender X's attribute/<key>/changed. Frame where X is
//     invalidated alone — A's gated subscription on gate-sender G does
//     not match because G is not in the frame, so A does NOT dispatch
//     despite X's signal landing on the wake_on_change:false edge.
//
//  2. A trigger node fires in a single frame. Both gate-sender G and
//     context-sender X subscribe to trigger (wake_on_change:true), so
//     both re-run in the same frame. G's emission has
//     `value: "needs_work"` which satisfies A's gated CEL predicate
//     (so A wakes via the gate edge). X's emission lands a wait-set
//     row on A via the wake_on_change:false edge. A dispatches once
//     and its substitution context carries BOTH senders' values
//     (gate-sender's value is read via the wake-edge wait-set drain;
//     context-sender's via the wake_on_change:false-edge wait-set
//     drain — proving the wait-set insert is NOT gated by
//     wake_on_change).
//
// Load-bearing property — the wake_on_change:false edge inserts the
// wait-set row but does NOT stale-mark the receiver. Scenario 1 is the
// gate falsifier — if the receiver dispatches, the cascade walker's
// wake gate from Pass 1 Task 7 is broken or absent. Scenario 2 is the
// context-drain falsifier — if the receiver dispatches without X's
// value in its substitution context, the wait-set insert was
// incorrectly gated by wake_on_change.
//
// Real-executor discipline — the stub IS a real gRPC executor from the
// supervisor's perspective; it receives the substituted attributes on
// the wire via req.GetAttributes(). The test reads back the receiver's
// dispatch-time attribute bag from h.Stub.Observed() so the assertion
// pins the resolved cascade-driven substitution context.
//
// Single-frame discipline — scenario 2 uses a cascade-driven multi-
// sender frame rather than two back-to-back admin invalidates. Two
// admin invalidates queue independent frames in serial_queue mode and
// race the conductor in coalesce mode; a cascade-driven multi-sender
// frame is deterministic.
//
//	@story: explicit-attribute-context-read

package scenarios

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestStoryReadWithoutWaking exhibits STORY-read-without-waking via the
// real assembled testcontainers stack. Template shape mirrors the GH
// issue #18 author's scenario: a receiver with one gated subscription
// to a gate-sender plus a context-gathering read of a different
// context-sender on a wake_on_change:false edge.
//
//	@story: explicit-attribute-context-read
func TestStoryReadWithoutWaking(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: gate-sender's boot payload is "idle" so the initial
	// frame does NOT trip the receiver's gated subscription; re-primed
	// to "needs_work" before scenario 2.
	h.Stub.WhenType("trigger").Success(
		map[string]any{"tick": "boot"}, true, "boot")
	h.Stub.WhenType("gate-sender").Success(
		map[string]any{"status": "idle"}, true, "boot")
	h.Stub.WhenType("context-sender").Success(
		map[string]any{"data": "ctx-boot"}, true, "boot")
	h.Stub.WhenType("receiver").Success(
		map[string]any{"summary": "boot"}, true, "boot")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name:    "explicit-attribute-context-read",
		Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/trigger"},
			{Type: "test/wake/context-sender"},
		},
		Nodes: []node.TemplateNodeDef{
			// @deliberate: trigger is a root; when invalidated its
			// terminal/success fan-outs both senders into ONE frame.
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "trigger", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/trigger", Type: "terminal/success",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tick": map[string]any{"type": "string"},
					},
					"required": []any{"tick"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "gate-sender", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "trigger", Type: "terminal/success",
						WakeOnChange:         node.BoolPtr(true),
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status": map[string]any{"type": "string"},
					},
					"required": []any{"status"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "context-sender", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "trigger", Type: "terminal/success",
						WakeOnChange:         node.BoolPtr(true),
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "test/wake/context-sender", Type: "terminal/success",
						WakeOnChange:         node.BoolPtr(true),
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"data": map[string]any{"type": "string"},
					},
					"required": []any{"data"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver", Executor: "stub"},
				scenario.WithSubscribes(
					// @deliberate: gated wake edge — receiver dispatches
					// only when gate-sender's status emission has
					// `value == "needs_work"`; wake_on_change: true so a
					// matching emission both inserts the wait-set row AND
					// stale-marks the receiver.
					node.SubscriptionEntry{
						Node:                 "gate-sender",
						Type:                 "attribute/status/changed",
						When:                 "payload.value == 'needs_work'",
						WakeOnChange:         node.BoolPtr(true),
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					// @deliberate: context-gathering read on
					// wake_on_change: false — context-sender's emit
					// inserts the receiver's wait-set row (so the
					// substitution context picks up the value at
					// dispatch via the gate edge) but does NOT
					// stale-mark the receiver.
					node.SubscriptionEntry{
						Node:                 "context-sender",
						Type:                 "attribute/data/changed",
						WakeOnChange:         node.BoolPtr(false),
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"gate_status": map[string]any{
							"type":   "string",
							"source": "{{nodes.gate-sender.attribute.status}}",
						},
						"ctx_data": map[string]any{
							"type":   "string",
							"source": "{{nodes.context-sender.attribute.data}}",
						},
					},
					"required": []any{"gate_status", "ctx_data"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-attr-ctx-read", map[string]any{})

	trigN := h.FindNode(iid, "trigger")
	gateN := h.FindNode(iid, "gate-sender")
	ctxN := h.FindNode(iid, "context-sender")
	rcvN := h.FindNode(iid, "receiver")
	require.NotNil(t, trigN)
	require.NotNil(t, gateN)
	require.NotNil(t, ctxN)
	require.NotNil(t, rcvN)
	// @constraint: trigger was previously a structural root; the
	// subscribes: entry added for the typed-message wake demoted it
	// from root, so the harness's empty-wake doesn't fire it. Emit
	// the typed message here to drive the boot cascade the test
	// assertions expect.
	h.PostInstanceMessage(iid, "test/wake/trigger", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	// @deliberate: boot-frame baseline must be zero receiver dispatches
	// — gate-sender's "idle" payload fails the receiver's CEL `when:`,
	// and context-sender's edge is wake_on_change: false. The receiver
	// stays fresh-but-never-dispatched after boot.
	require.True(t, h.WaitForNodeState(trigN.ID, cascade.NodeStateFresh, 30*time.Second),
		"trigger should settle fresh in the boot frame")
	require.True(t, h.WaitForNodeState(gateN.ID, cascade.NodeStateFresh, 30*time.Second),
		"gate-sender should settle fresh in the boot frame")
	require.True(t, h.WaitForNodeState(ctxN.ID, cascade.NodeStateFresh, 30*time.Second),
		"context-sender should settle fresh in the boot frame")
	// @deliberate: falsifier window — give the cascade ample time to
	// (incorrectly) wake the receiver if the wake-gate is broken.
	time.Sleep(2 * time.Second)
	bootReceiverRuns := countObservedReceiverRuns(h)
	require.Equal(t, 0, bootReceiverRuns,
		"STORY falsifier (boot edge): receiver dispatched during boot despite "+
			"gate-sender's payload not matching `when:` and context-sender's edge "+
			"being wake_on_change:false — the wake gate is broken")

	// @deliberate: scenario 1 — invalidate context-sender alone. Its
	// emit lands a wait-set row on the receiver via the
	// wake_on_change:false edge, but the receiver is NOT stale-marked
	// and the gate edge does not fire (gate-sender is not in the
	// frame). The receiver MUST NOT dispatch in this frame.
	h.Stub.WhenType("context-sender").Success(
		map[string]any{"data": "ctx-s1"}, true, "scenario-1")
	h.Stub.WhenType("receiver").Success(
		map[string]any{"summary": "scenario-1"}, true, "scenario-1")

	postAdminInvalidate(t, h, iid, "test/wake/context-sender", "1")

	require.True(t, h.WaitForEventKind(ctxN.ID, "attribute/data/changed", 30*time.Second),
		"context-sender's attribute/data/changed audit row must land in scenario 1")
	require.True(t, h.WaitForEventKind(ctxN.ID, "terminal/success", 10*time.Second),
		"context-sender's terminal/success audit row must land in scenario 1")

	// @deliberate: falsifier window — if the wake_on_change: false
	// gate is missing the receiver dispatches within this window.
	time.Sleep(2 * time.Second)

	scenario1ReceiverRuns := countObservedReceiverRuns(h)
	require.Equal(t, bootReceiverRuns, scenario1ReceiverRuns,
		"STORY falsifier (scenario 1): receiver dispatched on context-sender's "+
			"emit despite wake_on_change:false on the covering subscription (the "+
			"cascade walker's wake gate is broken or absent)")

	// @deliberate: scenario 2 — invalidate trigger; its terminal/success
	// cascades both senders into the SAME frame. gate-sender's
	// "needs_work" satisfies the receiver's CEL `when:` (stale-mark +
	// wait-set row via the wake edge); context-sender's emit adds a
	// wait-set row on the wake_on_change:false edge that carries the
	// value into the receiver's substitution context without an extra
	// stale-mark. Receiver dispatches once with BOTH senders' values.
	h.Stub.WhenType("trigger").Success(
		map[string]any{"tick": "scenario-2"}, true, "scenario-2")
	h.Stub.WhenType("gate-sender").Success(
		map[string]any{"status": "needs_work"}, true, "scenario-2")
	// @constraint: a wake_on_change: false wait-set row only lands when
	// the receiver already has an in-flight row in the sender's
	// RunScope at sender-settle time. Gate-sender's settlement
	// stale-marks the receiver; delay context-sender so gate-sender
	// wins the ordering race deterministically.
	// @decision: substitution-grammar-fallback-routing — authors who
	// need order-independent carry-through use
	// force_upstream_refresh: true instead.
	h.Stub.WhenType("context-sender").Delay(500*time.Millisecond).Success(
		map[string]any{"data": "ctx-s2"}, true, "scenario-2")
	h.Stub.WhenType("receiver").Success(
		map[string]any{"summary": "scenario-2"}, true, "scenario-2")

	postAdminInvalidate(t, h, iid, "test/wake/trigger", "2")

	require.Eventually(t, func() bool {
		return countObservedReceiverRuns(h) > scenario1ReceiverRuns
	}, 30*time.Second, 50*time.Millisecond,
		"receiver must dispatch in scenario 2 (trigger's terminal/success "+
			"cascades both senders into one frame; gate-sender's "+
			"`value: \"needs_work\"` satisfies the receiver's gated subscription)")

	// @deliberate: scan the LAST receiver dispatch's attributes — the
	// stub records every ExecuteRequest off the wire — and assert
	// both senders' scenario-2 values landed in the substitution
	// context.
	var lastReceiverAttrs map[string]any
	for _, obs := range h.Stub.Observed() {
		if obs.NodeType == "receiver" {
			lastReceiverAttrs = obs.Attributes
		}
	}
	require.NotNil(t, lastReceiverAttrs,
		"stub should have observed the receiver's scenario-2 dispatch")
	require.Equal(t, "needs_work", lastReceiverAttrs["gate_status"],
		"receiver's substitution context must carry gate-sender's scenario-2 value")
	require.Equal(t, "ctx-s2", lastReceiverAttrs["ctx_data"],
		"STORY falsifier (scenario 2): receiver's substitution context must "+
			"carry context-sender's scenario-2 value via the wake_on_change:false "+
			"edge's wait-set drain — a missing value would mean Pass 1 Task 7 "+
			"incorrectly gated the wait-set insert on wake_on_change")

	// @deliberate: audit-log corroboration for scenario 2.
	require.True(t, h.WaitForEventKind(rcvN.ID, "work_started", 10*time.Second),
		"receiver's scenario-2 work_started audit row must land")
	require.True(t, h.WaitForEventKind(rcvN.ID, "terminal/success", 10*time.Second),
		"receiver's scenario-2 terminal/success audit row must land")
}

// countObservedReceiverRuns returns how many times the stub has
// observed an Execute call for node_type == "receiver" since harness
// start. Wraps the Stub.Observed() walk so the test's gate-falsifier
// assertion reads as a single comparison.
func countObservedReceiverRuns(h *scenario.Harness) int {
	n := 0
	for _, obs := range h.Stub.Observed() {
		if obs.NodeType == "receiver" {
			n++
		}
	}
	return n
}

// postAdminInvalidate emits a per-target typed-message wake to invoke
// invalidation of a specific node in the instance. Each call site uses a
// distinct `wakeType` (declared in the template's `messages:` block and
// covered by a `subscribes:` entry on the target node) and a unique
// `callSiteID` so a re-run of the test produces a fresh frame, not a
// 200-OK idempotency replay.
//
// @story: upstream-pull-on-invalidate
func postAdminInvalidate(t *testing.T, h *scenario.Harness, instanceID shared.UUID, wakeType, callSiteID string) {
	t.Helper()
	h.PostInstanceMessage(instanceID, wakeType, nil,
		fmt.Sprintf("test-wake-%s-%s", t.Name(), callSiteID))
}
