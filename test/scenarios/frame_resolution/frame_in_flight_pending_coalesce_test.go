// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies spec §3.2: under coalesce mode, no matter how many
// invalidates fire while a frame is running, at most one queued
// coalesce row exists at any time. Enforced structurally by
// uq_rimsky_frames_coalesce_queued.
package frame_resolution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
)

func TestFrameInFlightPendingCoalesce(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok").Delay(3 * time.Second)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pending-coalesce", Version: "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-pending-coalesce", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, waitForFramesByState(t, h, iid, "running", 1, 5*time.Second),
		"first frame did not enter running")

	// Fire many invalidates rapidly. The partial unique index prevents
	// more than one queued coalesce row from existing simultaneously.
	for i := 0; i < 25; i++ {
		fireInvalidate(t, h, iid, worker.ID)
	}

	// Sample over a window: at every read, queued coalesce rows ≤ 1.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n := countFramesByState(t, h, iid, "queued")
		require.LessOrEqual(t, n, 1,
			"observed %d queued coalesce rows; uq_rimsky_frames_coalesce_queued violated", n)
		time.Sleep(20 * time.Millisecond)
	}

	// Eventually drain.
	require.Eventually(t, func() bool {
		return countFramesByState(t, h, iid, "completed") == 2
	}, 30*time.Second, 100*time.Millisecond,
		"expected exactly 2 completed frames")
}
