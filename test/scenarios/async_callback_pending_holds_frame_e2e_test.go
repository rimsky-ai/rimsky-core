// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: frame
func TestAsyncCallbackPendingNodeHoldsFrameOpen(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("agent").AwaitAsyncCallback("ack-held-frame", 5000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "async-frame-hold", Version: "1",
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
	iid := h.CreateInstance(tid, "ck-async-frame-hold", map[string]any{})

	n := h.FindNode(iid, "agent")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateRunning)

	frameID := h.GetRunningFrameID(iid)
	require.NotEqual(t, shared.UUID{}, frameID)

	require.NoError(t, frame.RunTick(h.Ctx, h.Persist, h.Queue, shared.SilentLogger{}),
		"forcing a frame-settlement sweep while the node is async-callback-pending must not error")

	pending := getFrameDetail(t, h, iid, frameID)
	require.Equal(t, "running", pending.State,
		"the frame containing an async-callback-pending node must stay open (state=running) — "+
			"the node has not reached a terminal state yet")
	require.Nil(t, pending.EndedAt,
		"the frame must not be marked ended while its node is still awaiting an async callback")

	cbURL := "http://" + h.Supervisor.CallbackAddr() + "/v1/callback/ack-held-frame"
	body, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"done": true},
			"changed":          true,
			"change_summary":   "async-ok",
		},
	})
	postCallbackBody(t, cbURL, body)

	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	waitForFrameEnded(t, h, iid, frameID)
}

type frameDetail struct {
	FrameID string     `json:"frame_id"`
	State   string     `json:"state"`
	EndedAt *time.Time `json:"ended_at,omitempty"`
}

func getFrameDetail(t *testing.T, h *scenario.Harness, instanceID, frameID shared.UUID) frameDetail {
	t.Helper()
	url := h.ControlBase + "/v1/instances/" + instanceID.String() + "/frames/" + frameID.String()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "frame detail endpoint must return 200")
	var out frameDetail
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func waitForFrameEnded(t *testing.T, h *scenario.Harness, instanceID, frameID shared.UUID) {
	t.Helper()
	for {
		if getFrameDetail(t, h, instanceID, frameID).EndedAt != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
