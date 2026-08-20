// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package frame_resolution

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
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

	h.WaitForNodeState(worker.ID, cascade.NodeStateFailed)

	var first frameRow
	awaited.Until(t, "the instance's single frame to end", func() bool {
		frames := listFrames(t, h, iid)
		if len(frames) != 1 || (frames[0].State != "failed" && frames[0].State != "completed") {
			return false
		}
		first = frames[0]
		return true
	})
	require.Equal(t, "failed", first.State,
		"frame should end failed when its expected node ended failed")
	require.NotNil(t, first.EndedAt, "failed frame must have ended_at set")

	var runFrameID uuid.UUID
	err := h.Pool.QueryRow(context.Background(),
		`SELECT frame_id FROM rimsky_node_runs
		  WHERE node_id = $1 AND state = 'failed'
		  ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC LIMIT 1`,
		uuid.UUID(worker.ID)).Scan(&runFrameID)
	require.NoError(t, err)
	require.Equal(t, first.FrameID, runFrameID,
		"failed run frame_id should match the failed frame")

	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")
	postInvalidateMessage(t, h, iid)

	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	waitForFramesByState(t, h, iid, "completed", 1)

	frames := listFrames(t, h, iid)
	require.Len(t, frames, 2, "expected two frames total")
	require.Equal(t, "failed", frames[0].State)
	require.Equal(t, "completed", frames[1].State)
}
