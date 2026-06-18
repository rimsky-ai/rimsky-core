// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

// @story: explicit-attribute-context-read
func TestStoryReadWithoutWaking(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

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
					node.SubscriptionEntry{
						Node:                 "gate-sender",
						Type:                 "attribute/status/changed",
						When:                 "payload.value == 'needs_work'",
						WakeOnChange:         node.BoolPtr(true),
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
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
	h.PostInstanceMessage(iid, "test/wake/trigger", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	require.True(t, h.WaitForNodeState(trigN.ID, cascade.NodeStateFresh, 30*time.Second),
		"trigger should settle fresh in the boot frame")
	require.True(t, h.WaitForNodeState(gateN.ID, cascade.NodeStateFresh, 30*time.Second),
		"gate-sender should settle fresh in the boot frame")
	require.True(t, h.WaitForNodeState(ctxN.ID, cascade.NodeStateFresh, 30*time.Second),
		"context-sender should settle fresh in the boot frame")
	time.Sleep(2 * time.Second)
	bootReceiverRuns := countObservedReceiverRuns(h)
	require.Equal(t, 0, bootReceiverRuns,
		"STORY falsifier (boot edge): receiver dispatched during boot despite "+
			"gate-sender's payload not matching `when:` and context-sender's edge "+
			"being wake_on_change:false — the wake gate is broken")

	h.Stub.WhenType("context-sender").Success(
		map[string]any{"data": "ctx-s1"}, true, "scenario-1")
	h.Stub.WhenType("receiver").Success(
		map[string]any{"summary": "scenario-1"}, true, "scenario-1")

	postAdminInvalidate(t, h, iid, "test/wake/context-sender", "1")

	require.True(t, h.WaitForEventKind(ctxN.ID, "attribute/data/changed", 30*time.Second),
		"context-sender's attribute/data/changed audit row must land in scenario 1")
	require.True(t, h.WaitForEventKind(ctxN.ID, "terminal/success", 10*time.Second),
		"context-sender's terminal/success audit row must land in scenario 1")

	time.Sleep(2 * time.Second)

	scenario1ReceiverRuns := countObservedReceiverRuns(h)
	require.Equal(t, bootReceiverRuns, scenario1ReceiverRuns,
		"STORY falsifier (scenario 1): receiver dispatched on context-sender's "+
			"emit despite wake_on_change:false on the covering subscription (the "+
			"cascade walker's wake gate is broken or absent)")

	h.Stub.WhenType("trigger").Success(
		map[string]any{"tick": "scenario-2"}, true, "scenario-2")
	h.Stub.WhenType("gate-sender").Success(
		map[string]any{"status": "needs_work"}, true, "scenario-2")
	// @decision: substitution-grammar-fallback-routing — authors who
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

	require.True(t, h.WaitForEventKind(rcvN.ID, "work_started", 10*time.Second),
		"receiver's scenario-2 work_started audit row must land")
	require.True(t, h.WaitForEventKind(rcvN.ID, "terminal/success", 10*time.Second),
		"receiver's scenario-2 terminal/success audit row must land")
}

func countObservedReceiverRuns(h *scenario.Harness) int {
	n := 0
	for _, obs := range h.Stub.Observed() {
		if obs.NodeType == "receiver" {
			n++
		}
	}
	return n
}

// @story: upstream-pull-on-invalidate
func postAdminInvalidate(t *testing.T, h *scenario.Harness, instanceID shared.UUID, wakeType, callSiteID string) {
	t.Helper()
	h.PostInstanceMessage(instanceID, wakeType, nil,
		fmt.Sprintf("test-wake-%s-%s", t.Name(), callSiteID))
}
