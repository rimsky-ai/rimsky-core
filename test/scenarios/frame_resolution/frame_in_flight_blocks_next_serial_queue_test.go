// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies spec §3.1: under serial_queue, while frame N is running,
// frame N+1 stays queued. After frame N completes, the engine
// advances frame N+1 to running on a subsequent scheduler tick.
package frame_resolution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
)

func TestFrameInFlightBlocksNextSerialQueue(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Slow first frame; fast second frame.
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok").Delay(3 * time.Second)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "blocks-next", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-blocks-next", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// First frame is running due to the delay.
	require.True(t, waitForFramesByState(t, h, iid, "running", 1, 5*time.Second),
		"first frame did not enter running")

	// Fire second invalidate; it should queue, not run.
	fireInvalidate(t, h, iid, worker.ID)

	// While first is running, second must stay queued.
	require.True(t, waitForFramesByState(t, h, iid, "queued", 1, 2*time.Second),
		"second frame did not appear in queued state")
	require.Equal(t, 1, countFramesByState(t, h, iid, "running"),
		"only one frame may run at a time per instance")

	// Wait for the cascade to fully drain.
	require.Eventually(t, func() bool {
		return countFramesByState(t, h, iid, "completed") == 2
	}, 30*time.Second, 100*time.Millisecond,
		"expected both frames completed")
}
