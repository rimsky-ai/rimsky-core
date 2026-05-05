// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies that POST /nodes/{id}/reset on a failed node drives through
// the frame engine (frame.EnqueueOrCoalesce) rather than calling
// UpdateState(failed → stale) directly. Direct UpdateState would strand
// the node with no frame_id (blessed-invariant 19), and sweepReady /
// RecalculateNode skip nil-frame_id nodes — the node would never run.
//
// This test catches review Issues 2 and 16: handleResetNode must clear
// the prior frame_id (defensive) and enqueue a frame so the next
// scheduler tick advances the queued frame and writes the new frame_id
// onto the source node before any dispatch.
package frame_resolution

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestResetFailedNodeDrivesThroughFrameEngine(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Stub fails the worker on first run.
	h.Stub.WhenType("worker").Error("test_failure", nil)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "reset-via-frame-engine", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-reset-frame", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFailed, 15*time.Second),
		"worker did not reach failed on first fire")

	// Wait for the first frame to settle to failed.
	require.True(t, waitForFramesByState(t, h, iid, "failed", 1, 5*time.Second),
		"first frame should end failed")

	// Capture the prior frame_id; reset should not leave it pointing here.
	var priorFrameID *uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&priorFrameID))
	require.NotNil(t, priorFrameID,
		"failed node should preserve frame_id from the failed frame")

	// Re-script the stub to succeed before resetting.
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")

	// POST /nodes/{id}/reset.
	resp, err := http.Post(
		h.ControlBase+"/nodes/"+worker.ID.String()+"/reset",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The node should reach fresh under the new frame.
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 20*time.Second),
		"worker did not reach fresh after reset; if reset bypassed the frame engine the node would be stuck stale with nil/old frame_id")

	// Verify a second frame was created and ended completed.
	require.True(t, waitForFramesByState(t, h, iid, "completed", 1, 5*time.Second),
		"second frame should end completed")

	frames := listFrames(t, h, iid)
	require.Len(t, frames, 2, "expected one failed frame plus one completed frame after reset")

	// Final frame_id on the now-fresh node should be cleared (per the
	// enforceAndUpdate fresh-state guard; spec §4.4).
	var finalFrameID *uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&finalFrameID))
	require.Nil(t, finalFrameID,
		"fresh node must carry no frame_id after work_completed")
}
