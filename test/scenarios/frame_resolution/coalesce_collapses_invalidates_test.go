// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies spec §3.2: under coalesce mode, mid-render invalidates
// collapse into a single trailing frame. Many invalidates → at most
// 2 distinct frames produced (the initial running frame + one
// trailing pending-coalesce frame whose source set is the union).
package frame_resolution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
)

func TestCoalesceCollapsesInvalidates(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Slow stub so the first frame is genuinely in flight while we fire follow-ups.
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok").Delay(2 * time.Second)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "coalesce-collapses", Version: "1",
		FrameResolution: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-coalesce", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait until the first frame is running.
	require.True(t, waitForFramesByState(t, h, iid, "running", 1, 5*time.Second),
		"first frame did not enter running")

	// Fire 9 additional invalidates while the first is running. They
	// should all collapse into a single queued coalesce row.
	for i := 0; i < 9; i++ {
		fireInvalidate(t, h, iid, worker.ID)
	}

	// uq_rimsky_frames_coalesce_queued enforces at most one queued coalesce row.
	require.LessOrEqual(t, countFramesByState(t, h, iid, "queued"), 1,
		"more than one queued coalesce row")

	// Wait for both frames to terminate.
	require.Eventually(t, func() bool {
		return countFramesByState(t, h, iid, "completed") == 2
	}, 30*time.Second, 100*time.Millisecond,
		"expected exactly 2 completed frames under coalesce")

	frames := listFrames(t, h, iid)
	require.Len(t, frames, 2,
		"coalesce: should produce exactly 2 frames total (initial running + trailing coalesce); got %d", len(frames))

	for _, f := range frames {
		require.Equal(t, "coalesce", f.Mode)
		require.Equal(t, "completed", f.State)
	}
	// The trailing coalesce frame's source set is the union of the 9
	// follow-ups. Since they all targeted worker.ID, the dedupe leaves
	// a single source. (Spec §3.2: "append sourceNodeID to source_node_ids
	// (deduped)".)
	require.Len(t, frames[1].SourceNodeIDs, 1,
		"trailing coalesce frame should have deduped source set; got %v", frames[1].SourceNodeIDs)
}
