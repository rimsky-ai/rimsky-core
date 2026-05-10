// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies spec §8 ("Quality-rule failures and frame outcomes") and
// spec §6.2 ("On terminal failure: frame_id is preserved"): a node
// failing during a frame causes the frame to end with state='failed';
// the failed node's rimsky_nodes.frame_id is preserved; subsequent
// queued frames advance.
//
// Mechanism: stub returns Errored("test_failure") which, with no
// matching policy entry, resolves to give_up (default for unknown
// error class per modeling/node/policy.go::Evaluate). The runner takes
// the give-up path; node state ends 'failed'; frame ends 'failed'.
// We then re-script the stub to Complete and fire a second invalidate
// to verify the next frame proceeds.
package frame_resolution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestFailedNodeMarksFrameFailed(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("test_failure", nil)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "failed-node-marks-frame-failed", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-failed-node", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFailed, 15*time.Second),
		"worker did not reach failed on first fire")

	// Wait for the frame engine's tick to record frame-end.
	deadline := time.Now().Add(5 * time.Second)
	var first frameRow
	for time.Now().Before(deadline) {
		frames := listFrames(t, h, iid)
		if len(frames) == 1 && (frames[0].State == "failed" || frames[0].State == "completed") {
			first = frames[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, "failed", first.State,
		"frame should end failed when its expected node ended failed")
	require.NotNil(t, first.EndedAt, "failed frame must have ended_at set")

	// frame_id on the failed node is preserved (per §6.2).
	var nodeFrameID *uuid.UUID
	err := h.Pool.QueryRow(context.Background(),
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&nodeFrameID)
	require.NoError(t, err)
	require.NotNil(t, nodeFrameID,
		"failed node must preserve frame_id (got NULL)")
	require.Equal(t, first.FrameID, *nodeFrameID,
		"failed node frame_id should match the failed frame")

	// Fire a second invalidate after re-scripting the stub to succeed.
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")
	fireInvalidate(t, h, iid, worker.ID)

	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh on second fire")

	// Wait for the second frame to settle.
	require.True(t, waitForFramesByState(t, h, iid, "completed", 1, 5*time.Second),
		"expected one completed frame after second fire")

	frames := listFrames(t, h, iid)
	require.Len(t, frames, 2, "expected two frames total")
	require.Equal(t, "failed", frames[0].State)
	require.Equal(t, "completed", frames[1].State)
}
