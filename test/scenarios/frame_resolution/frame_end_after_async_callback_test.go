// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies spec §9: an async-handoff dispatch keeps the frame in flight
// until the callback resolves. While the executor returns
// AwaitAsyncCallback, the source node stays in 'running' state, so
// frame-end's predicate (no nodes in stale/running for this instance)
// cannot fire. After the callback POST, the cascade resumes and the
// frame ends.
package frame_resolution

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFrameEndAfterAsyncCallback(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("agent").AwaitAsyncCallback("ack-frame-async", 10000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "async-frame", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "agent", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"done": map[string]any{"type": "boolean"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-async-frame", map[string]any{})
	n := h.FindNode(iid, "agent")
	require.NotNil(t, n)

	// Wait for the agent to reach running (async handoff in progress).
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateRunning, 15*time.Second),
		"agent did not reach running")

	// Snapshot the frame_id from the live dispatch row (while it still
	// exists — supervisor may clean up dispatches at terminal commit).
	var dispatchFrameID uuid.UUID
	err := h.Pool.QueryRow(context.Background(),
		`SELECT frame_id FROM rimsky_node_runs WHERE node_id = $1 LIMIT 1`,
		uuid.UUID(n.ID)).Scan(&dispatchFrameID)
	require.NoError(t, err, "expected live dispatch row while node is in async-handoff")

	// While agent is running, the frame must remain running too — frame-end
	// cannot fire when a node is still in_motion.
	require.Equal(t, 1, countFramesByState(t, h, iid, "running"),
		"frame should be running while async-handoff dispatch is open")
	require.Equal(t, 0, countFramesByState(t, h, iid, "completed"),
		"frame must not complete before callback resolves")

	// Hold the running invariant for a beat to give frame-end a chance to
	// (incorrectly) fire.
	for i := 0; i < 5; i++ {
		time.Sleep(200 * time.Millisecond)
		require.Equal(t, 0, countFramesByState(t, h, iid, "completed"),
			"frame completed prematurely while async dispatch was open")
	}

	// Resolve the async callback. The callback body uses the
	// AsyncCallbackBody outcome-oneof shape (success / error / park)
	// rather than the legacy {type: ...} discriminator.
	cbURL := "http://" + h.Supervisor.CallbackAddr() + "/v1/callback/ack-frame-async"
	body, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"done": true},
			"changed":          true,
			"change_summary":   "async-ok",
		},
	})
	deadline := time.Now().Add(10 * time.Second)
	var status int
	for time.Now().Before(deadline) {
		resp, err := http.Post(cbURL, "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		status = resp.StatusCode
		_ = resp.Body.Close()
		if status == http.StatusOK {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.Equal(t, http.StatusOK, status, "callback did not become available")

	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"agent did not reach fresh after callback")

	// Frame should now end.
	require.True(t, waitForFramesByState(t, h, iid, "completed", 1, 10*time.Second),
		"frame did not complete after callback resolved")

	// Frame_id correlates: the snapshotted dispatch frame_id matches the frame row's frame_id.
	frames := listFrames(t, h, iid)
	require.Len(t, frames, 1)
	require.Equal(t, frames[0].FrameID, dispatchFrameID,
		"dispatch frame_id must match the running frame")
}
