// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

type heldFramesResponse struct {
	Frames []struct {
		FrameID    string   `json:"frame_id"`
		InstanceID string   `json:"instance_id"`
		NodeIDs    []string `json:"node_ids"`
	} `json:"frames"`
}

// @concept: parked-state
func TestParkedHoldsFrame_TypedMessageDoesNotWake(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("root").Success(map[string]any{"r": 1}, true, "root")
	h.Stub.WhenType("parker").Park(
		genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK,
		"waiting-for-wake", time.Time{},
	)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-holds-frame", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/parker"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "root", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "parker", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "root", Type: "terminal/success",
						WakeOnChange:         node.BoolPtr(true),
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "test/wake/parker", Type: "terminal/success",
						WakeOnChange:         node.BoolPtr(true),
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
			),
		},
	})

	iid := createInstanceTerminateAfterRun(t, h, tid)

	root := h.FindNode(iid, "root")
	parker := h.FindNode(iid, "parker")
	require.NotNil(t, root)
	require.NotNil(t, parker)

	require.True(t, h.WaitForNodeState(parker.ID, cascade.NodeStateParked, 30*time.Second),
		"parker should reach parked after root settles")

	require.Nil(t, getInstance(t, h, iid).TerminatedAt,
		"instance must NOT be terminated while a node is parked, even with terminate_after_run set")

	require.True(t, waitForHeldFrame(t, h, iid, parker.ID, 10*time.Second),
		"held-frames diagnostic should surface the parked node's frame")

	h.PostInstanceMessage(iid, "test/wake/parker", nil,
		fmt.Sprintf("test-wake-%s-parker", t.Name()))

	require.False(t, h.WaitForNodeState(parker.ID, cascade.NodeStateFresh, 5*time.Second),
		"parker MUST remain parked after a typed-message cascade arrives — `concept:parked-state` invariant: cascade-driven re-invocation does NOT mutate the parked row")

	require.Nil(t, getInstance(t, h, iid).TerminatedAt,
		"instance must NOT terminate while the parker stays parked, even after the typed-message cascade")
}

func waitForHeldFrame(t *testing.T, h *scenario.Harness, instanceID, nodeID shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	url := h.ControlBase + "/v1/admin/diagnostics/held-frames"
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			var body heldFramesResponse
			derr := json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			if derr == nil {
				for _, f := range body.Frames {
					if f.InstanceID != instanceID.String() {
						continue
					}
					for _, nid := range f.NodeIDs {
						if nid == nodeID.String() {
							return true
						}
					}
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
