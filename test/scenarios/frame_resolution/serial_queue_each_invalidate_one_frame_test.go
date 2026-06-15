// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies spec §3.1: under serial_queue, each invalidate produces a
// distinct frame; multiple rapid invalidates queue separately, all
// render serially, all produce terminal commits. The smoke fixture's
// guiding case in concentrated form.
package frame_resolution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSerialQueueEachInvalidateOneFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "serial-queue-each-invalidate", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-serial-each", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// @deliberate: CreateInstance enqueued the first frame for the root. Fire 9 more,
	// for 10 total invalidates → 10 frames.
	const totalFrames = 10
	for i := 0; i < totalFrames-1; i++ {
		fireInvalidate(t, h, iid, worker.ID)
	}

	require.Eventually(t, func() bool {
		return countFramesByState(t, h, iid, "completed") == totalFrames
	}, 60*time.Second, 100*time.Millisecond,
		"expected %d completed frames; got: queued=%d running=%d completed=%d failed=%d",
		totalFrames,
		countFramesByState(t, h, iid, "queued"),
		countFramesByState(t, h, iid, "running"),
		countFramesByState(t, h, iid, "completed"),
		countFramesByState(t, h, iid, "failed"),
	)

	frames := listFrames(t, h, iid)
	require.Len(t, frames, totalFrames, "expected exactly %d frames", totalFrames)
	seenTriggers := make(map[string]struct{}, totalFrames)
	for i, f := range frames {
		require.Equal(t, "completed", f.State, "frame %d not completed", i)
		require.NotEqual(t, (frameRow{}).TriggeringMessageID, f.TriggeringMessageID,
			"frame %d missing triggering_message_id", i)
		// Each frame carries a distinct triggering message: each invalidate
		// produces its own envelope (one-message-per-frame).
		seenTriggers[f.TriggeringMessageID.String()] = struct{}{}
	}
	require.Equal(t, totalFrames, len(seenTriggers),
		"expected %d distinct triggering_message_id values; got %d", totalFrames, len(seenTriggers))
}
