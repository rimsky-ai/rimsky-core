// Verifies spec §3.2: under coalesce mode, no matter how many
// invalidates fire while a frame is running, at most one queued
// coalesce row exists at any time. Enforced structurally by
// uq_rimsky_frames_coalesce_queued.
package frame_resolution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
)

func TestFrameInFlightPendingCoalesce(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok").Delay(3 * time.Second)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pending-coalesce", Version: "1",
		FrameResolution: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-pending-coalesce", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, waitForFramesByState(t, h.Pool, iid, "running", 1, 5*time.Second),
		"first frame did not enter running")

	// Fire many invalidates rapidly. The partial unique index prevents
	// more than one queued coalesce row from existing simultaneously.
	for i := 0; i < 25; i++ {
		fireInvalidate(t, h, iid, worker.ID)
	}

	// Sample over a window: at every read, queued coalesce rows ≤ 1.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n := countFramesByState(t, h.Pool, iid, "queued")
		require.LessOrEqual(t, n, 1,
			"observed %d queued coalesce rows; uq_rimsky_frames_coalesce_queued violated", n)
		time.Sleep(20 * time.Millisecond)
	}

	// Eventually drain.
	require.Eventually(t, func() bool {
		return countFramesByState(t, h.Pool, iid, "completed") == 2
	}, 30*time.Second, 100*time.Millisecond,
		"expected exactly 2 completed frames")
}
