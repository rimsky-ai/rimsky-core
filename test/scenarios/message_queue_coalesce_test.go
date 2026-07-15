// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: message-queue-coalesces-pending
// @decision: message-queue-mode-per-instance
func TestMessageQueueCoalesce_DropsPriorPendingOnReceipt(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("root").Success(map[string]any{"ok": true}, true, "root")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "message-queue-coalesce", Version: "1",
		MessageQueueMode: "coalesce",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
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

	for i := 1; i <= 5; i++ {
		h.PostInstanceMessage(iid, "test/wake", nil, fmt.Sprintf("wake-%d", i))
	}

	deadline := time.Now().Add(30 * time.Second)
	var pending int
	var running int
	for time.Now().Before(deadline) {
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE`,
			[]any{iid}, &pending)
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
			[]any{iid}, &running)
		if pending == 0 && running == 0 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.Equal(t, 0, pending, "no pending messages should remain after quiescence")
	require.Equal(t, 0, running, "no running frames should remain after quiescence")

	var cancelledCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND cancelled = TRUE`,
		[]any{iid}, &cancelledCount)
	require.GreaterOrEqual(t, cancelledCount, 4,
		"under message_queue_mode=coalesce, at least 4 of the 5 test/wake messages should be marked cancelled — "+
			"each new insert coalesces prior pending; got %d", cancelledCount)

	var deliveredCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'test/wake' AND delivered_at IS NOT NULL`,
		[]any{iid}, &deliveredCount)
	require.LessOrEqual(t, deliveredCount, 2,
		"under coalesce mode, at most 2 test/wake messages should have been delivered — "+
			"the queue is bounded at ≤ 1 pending at any moment; got %d", deliveredCount)
}

// @story: message-queue-coalesces-pending
// @decision: message-queue-mode-per-instance
func TestMessageQueueCoalesce_BacklogModePreservesEveryMessage(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("root").Success(map[string]any{"ok": true}, true, "root")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name:    "message-queue-backlog",
		Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
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

	for i := 1; i <= 5; i++ {
		h.PostInstanceMessage(iid, "test/wake", nil, fmt.Sprintf("wake-%d", i))
	}

	deadline := time.Now().Add(60 * time.Second)
	var pending int
	var running int
	for time.Now().Before(deadline) {
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE`,
			[]any{iid}, &pending)
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
			[]any{iid}, &running)
		if pending == 0 && running == 0 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.Equal(t, 0, pending, "no pending messages should remain after quiescence")

	var cancelledCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND cancelled = TRUE`,
		[]any{iid}, &cancelledCount)
	require.Equal(t, 0, cancelledCount,
		"under default backlog mode, no messages should be cancelled by the runtime")

	var deliveredCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'test/wake' AND delivered_at IS NOT NULL`,
		[]any{iid}, &deliveredCount)
	require.Equal(t, 5, deliveredCount,
		"under backlog mode, all 5 test/wake messages should have been delivered")
}
