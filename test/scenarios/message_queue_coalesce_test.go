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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: message-queue-coalesces-pending
// @decision: message-queue-mode-per-instance
func TestMessageQueueCoalesce_DropsPriorPendingOnReceipt(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	openerHold := make(chan struct{})
	h.Stub.WhenType("opener").Success(map[string]any{}, true, "opener").HoldUntil(openerHold)
	h.Stub.WhenType("root").Success(map[string]any{"ok": true}, true, "root")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "message-queue-coalesce", Version: "1",
		MessageQueueMode: "coalesce",
		Messages: []spec.MessageSchema{
			{Type: "test/prime"},
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			{
				Type:     "opener",
				Executor: "stub",
				Subscribes: []node.SubscriptionEntry{
					{
						Node: "test/prime", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				},
			},
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "root", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"ok": map[string]any{"type": "boolean", "readOnly": true}},
					"required":   []any{"ok"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-coalesce", map[string]any{})
	opener := h.FindNode(iid, "opener")
	root := h.FindNode(iid, "root")
	require.NotNil(t, opener)
	require.NotNil(t, root)

	h.PostInstanceMessage(iid, "test/prime", nil, "prime-1")
	waitForNodeRunning(h, opener.ID)

	for i := 1; i <= 5; i++ {
		h.PostInstanceMessage(iid, "test/wake", nil, fmt.Sprintf("wake-%d", i))
	}

	var pendingBeforeRelease int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'test/wake' AND delivered_at IS NULL AND cancelled = FALSE`,
		[]any{iid}, &pendingBeforeRelease)
	require.Equal(t, 1, pendingBeforeRelease,
		"the queue must hold exactly one pending test/wake message while the first frame is "+
			"still running — each new insert coalesces (cancels) every prior pending message "+
			"in the same transaction, so five rapid inserts never let the pending count exceed 1")

	close(openerHold)
	h.WaitForNodeState(opener.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(root.ID, cascade.NodeStateFresh)
	waitForMessageQueueQuiescent(h, iid)

	var cancelledCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'test/wake' AND cancelled = TRUE`,
		[]any{iid}, &cancelledCount)
	require.Equal(t, 4, cancelledCount,
		"under message_queue_mode=coalesce, exactly 4 of the 5 test/wake messages (1-4) must "+
			"be cancelled by the later inserts that coalesced over them; got %d", cancelledCount)

	var frameCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1`, []any{iid}, &frameCount)
	require.Equal(t, 2, frameCount,
		"coalesce mode must run exactly two frames: the one opened by test/prime (during "+
			"which the five test/wake messages coalesce) and one more opened by whichever "+
			"test/wake message survives; got %d", frameCount)

	survivorMessageID := requireSingleMessageID(t, h, iid, "wake-5")
	var cancelledMessageIDs []string
	for i := 1; i <= 4; i++ {
		cancelledMessageIDs = append(cancelledMessageIDs, requireSingleMessageID(t, h, iid, fmt.Sprintf("wake-%d", i)))
	}
	for i, id := range cancelledMessageIDs {
		var cancelled bool
		h.QueryRowSQL(`SELECT cancelled FROM rimsky_messages WHERE id = $1`, []any{id}, &cancelled)
		require.True(t, cancelled, "wake-%d must be cancelled", i+1)
	}
	var survivorCancelled, survivorDelivered bool
	h.QueryRowSQL(`SELECT cancelled, delivered_at IS NOT NULL FROM rimsky_messages WHERE id = $1`,
		[]any{survivorMessageID}, &survivorCancelled, &survivorDelivered)
	require.False(t, survivorCancelled, "wake-5 must survive uncancelled")
	require.True(t, survivorDelivered, "wake-5 must be the message that gets delivered")

	var secondFrameTrigger string
	h.QueryRowSQL(`SELECT triggering_message_id::text FROM rimsky_frames WHERE instance_id = $1 ORDER BY started_at ASC OFFSET 1 LIMIT 1`,
		[]any{iid}, &secondFrameTrigger)
	require.Equal(t, survivorMessageID, secondFrameTrigger,
		"the second frame's trigger must be wake-5 specifically, by inspecting the frame's "+
			"triggering_message_id — a coalesce-to-oldest (or coalesce-to-any-other-message) "+
			"bug would still pass a bag count assertion but fail this identity check")

	var rootRunCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1`, []any{root.ID}, &rootRunCount)
	require.Equal(t, 1, rootRunCount,
		"root must dispatch exactly once, driven solely by wake-5's frame")
}

// @story: message-queue-coalesces-pending
// @decision: message-queue-mode-per-instance
func TestMessageQueueCoalesce_BacklogModePreservesEveryMessage(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	openerHold := make(chan struct{})
	h.Stub.WhenType("opener").Success(map[string]any{}, true, "opener").HoldUntil(openerHold)
	h.Stub.WhenType("root").Success(map[string]any{"ok": true}, true, "root")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name:    "message-queue-backlog",
		Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/prime"},
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			{
				Type:     "opener",
				Executor: "stub",
				Subscribes: []node.SubscriptionEntry{
					{
						Node: "test/prime", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				},
			},
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "root", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"ok": map[string]any{"type": "boolean", "readOnly": true}},
					"required":   []any{"ok"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-backlog", map[string]any{})
	opener := h.FindNode(iid, "opener")
	root := h.FindNode(iid, "root")
	require.NotNil(t, opener)
	require.NotNil(t, root)

	h.PostInstanceMessage(iid, "test/prime", nil, "prime-1")
	waitForNodeRunning(h, opener.ID)

	for i := 1; i <= 5; i++ {
		h.PostInstanceMessage(iid, "test/wake", nil, fmt.Sprintf("wake-%d", i))
	}

	var pendingBeforeRelease int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'test/wake' AND delivered_at IS NULL AND cancelled = FALSE`,
		[]any{iid}, &pendingBeforeRelease)
	require.Equal(t, 5, pendingBeforeRelease,
		"under default backlog mode, all 5 test/wake messages must remain pending while the "+
			"first frame is still running — no insert cancels a predecessor")

	close(openerHold)
	h.WaitForNodeState(opener.ID, cascade.NodeStateFresh)
	waitForMessageQueueQuiescent(h, iid)

	var cancelledCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND cancelled = TRUE`,
		[]any{iid}, &cancelledCount)
	require.Equal(t, 0, cancelledCount,
		"under default backlog mode, no messages should be cancelled by the runtime")

	var frameCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1`, []any{iid}, &frameCount)
	require.Equal(t, 6, frameCount,
		"backlog mode must run exactly six frames: the one opened by test/prime plus one "+
			"more for each of the five queued test/wake messages; got %d", frameCount)

	var deliveredCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'test/wake' AND delivered_at IS NOT NULL`,
		[]any{iid}, &deliveredCount)
	require.Equal(t, 5, deliveredCount,
		"under backlog mode, all 5 test/wake messages should have been delivered")

	var triggerIDs []string
	h.QuerySQL(`SELECT triggering_message_id::text FROM rimsky_frames WHERE instance_id = $1 ORDER BY started_at ASC`,
		[]any{iid}, func(scan func(...any) error) error {
			var id string
			if err := scan(&id); err != nil {
				return err
			}
			triggerIDs = append(triggerIDs, id)
			return nil
		})
	require.Len(t, triggerIDs, 6)
	for i := 1; i <= 5; i++ {
		wantID := requireSingleMessageID(t, h, iid, fmt.Sprintf("wake-%d", i))
		require.Equal(t, wantID, triggerIDs[i],
			"frame %d (in receipt order) must be triggered by wake-%d specifically, not a "+
				"reordered or coalesced substitute", i+1, i)
	}
}

func waitForNodeRunning(h *scenario.Harness, nodeID shared.UUID) {
	for {
		var count int
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1 AND state = 'running'`,
			[]any{nodeID}, &count)
		if count > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForMessageQueueQuiescent(h *scenario.Harness, instanceID shared.UUID) {
	for {
		var pending, running int
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE`,
			[]any{instanceID}, &pending)
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
			[]any{instanceID}, &running)
		if pending == 0 && running == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireSingleMessageID(t *testing.T, h *scenario.Harness, instanceID shared.UUID, idempotencyKey string) string {
	t.Helper()
	var id string
	h.QueryRowSQL(`SELECT message_id::text FROM rimsky_message_idempotencies
		 WHERE instance_id = $1 AND idempotency_key = $2`,
		[]any{instanceID, idempotencyKey}, &id)
	require.NotEmpty(t, id, "no message row found for idempotency key %q", idempotencyKey)
	return id
}
