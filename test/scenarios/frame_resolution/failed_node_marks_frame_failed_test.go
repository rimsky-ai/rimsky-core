// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_resolution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFailedNodeMarksFrameFailed(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("test_failure", nil)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "failed-node-marks-frame-failed", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-failed-node", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 15*time.Second),
		"worker did not reach failed on first fire")

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

	var nodeFrameID *uuid.UUID
	err := h.Pool.QueryRow(context.Background(),
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&nodeFrameID)
	require.NoError(t, err)
	require.NotNil(t, nodeFrameID,
		"failed node must preserve frame_id (got NULL)")
	require.Equal(t, first.FrameID, *nodeFrameID,
		"failed node frame_id should match the failed frame")

	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")
	postInvalidateMessage(t, h, iid)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh on second fire")

	require.True(t, waitForFramesByState(t, h, iid, "completed", 1, 5*time.Second),
		"expected one completed frame after second fire")

	frames := listFrames(t, h, iid)
	require.Len(t, frames, 2, "expected two frames total")
	require.Equal(t, "failed", frames[0].State)
	require.Equal(t, "completed", frames[1].State)
}
