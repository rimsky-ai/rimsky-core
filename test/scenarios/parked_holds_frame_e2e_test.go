// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pass-7 acceptance gate for the 2026-06-03 durable-by-default lifecycle
// spec, scenario 3 ("Outstanding work holds the frame"), expressed via
// the parked-state flavor. Proves end-to-end against the real runtime
// (real control-api over HTTP, real supervisor, real scheduler + frame
// engine, testcontainers Postgres) that:
//
//   - A parked node_run holds its frame open — the frame stays `running`
//     and the held-frames diagnostic (`/v1/admin/diagnostics/held-frames`)
//     reports it. This complements the async-callback flavor in
//     `async_callback_holds_frame_e2e_test.go`; the held-frames
//     diagnostic is specifically scoped to parked nodes
//     (`phase='parked'`), so the parked-state property is the one that
//     exercises the diagnostic surface.
//   - The instance is NOT terminated while a parked node holds the
//     frame, even though it was created with `terminate_after_run =
//     true` (the Pass-3 instance-terminal guard treats parked as
//     unresolved, matching the Pass-1
//     `ListRunningFramesNoPendingNodes` semantics).
//   - Only after the parked node resolves via a typed-message wake —
//     i.e. the frame genuinely ends — does `terminate_after_run` fire
//     and stamp `terminated_at` (the Pass-3 strict "terminate after the
//     next frame ends" semantics).
//
// Template shape (load-bearing): two nodes — `root` (a structural-root
// node that just succeeds) and `parker` (a downstream of `root` that
// also subscribes to the typed wake message `test/wake/parker`). The
// post-spec uniform wake pattern: the harness-emitted empty-message
// wake fires `root` via the structural-root injection edge; `root`'s
// success cascades to `parker` via author-declared subscription; the
// typed-message wake re-fires `parker` post-park via cascade-walk on
// its `test/wake/parker` subscription.
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

// heldFramesResponse mirrors the wire JSON of GET
// /v1/admin/diagnostics/held-frames so the test can decode the
// diagnostic surface without importing the unexported control-api
// types. Only the fields the assertion reads are typed.
type heldFramesResponse struct {
	Frames []struct {
		FrameID    string   `json:"frame_id"`
		InstanceID string   `json:"instance_id"`
		NodeIDs    []string `json:"node_ids"`
	} `json:"frames"`
}

// TestParkedHoldsFrame_EndToEnd exhibits the parked-state flavor of the
// "outstanding work holds the frame" property. A parked node-run keeps
// its frame open; the held-frames diagnostic surfaces it; the instance
// stays non-terminal under `terminate_after_run = true` until the
// parked work resolves through the typed-message wake path.
func TestParkedHoldsFrame_EndToEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: `root` succeeds immediately so the empty-message
	// harness wake settles it, cascading to `parker`. `parker`'s first
	// dispatch parks indefinitely (no resume_at, no max_park_duration
	// → SweepParkedNodes does not wake it); the typed-message wake is
	// the only path out.
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
			// @deliberate: `root` is the structural root — no `subscribes:`
			// block, so the runtime-injected structural-root edge under
			// sender="" wakes it on the empty-message harness wake.
			scenario.MakeNode(node.TemplateNodeDef{Type: "root", Executor: "stub"}),
			// @deliberate: `parker` cascades from `root` on terminal/success
			// (first dispatch → parks) AND subscribes to the typed wake
			// envelope `test/wake/parker` (post-park resumption path).
			// Both subscriptions carry wake_on_change: true; the typed
			// wake's force_upstream_refresh is false because there is no
			// upstream to refresh — the wake is the trigger.
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

	// @deliberate: Create the instance with terminate_after_run = true via the real
	// HTTP create path (the harness CreateInstance helper does not set
	// the flag).
	iid := createInstanceTerminateAfterRun(t, h, tid)

	root := h.FindNode(iid, "root")
	parker := h.FindNode(iid, "parker")
	require.NotNil(t, root)
	require.NotNil(t, parker)

	// @constraint: `parker` must reach the parked state via the
	// cascade triggered by `root`'s terminal/success.
	require.True(t, h.WaitForNodeState(parker.ID, cascade.NodeStateParked, 30*time.Second),
		"parker should reach parked after root settles")

	// @constraint: The instance must NOT be terminated while a node
	// holds its frame open via the parked phase, even with
	// terminate_after_run set (instance-terminal guard).
	require.Nil(t, getInstance(t, h, iid).TerminatedAt,
		"instance must NOT be terminated while a node is parked, even with terminate_after_run set")

	// @constraint: The held-frames diagnostic surfaces the parker's
	// frame. The endpoint groups parked rows by frame_id; the only
	// parked row at this point is `parker`, so its frame appears in
	// the response.
	require.True(t, waitForHeldFrame(t, h, iid, parker.ID, 10*time.Second),
		"held-frames diagnostic should surface the parked node's frame")

	// @deliberate: Re-script parker to Success so the wake dispatch
	// resolves cleanly. WhenType replaces the entire per-type script
	// in the stub.
	h.Stub.WhenType("parker").Success(map[string]any{"p": 1}, true, "resumed")

	// @deliberate: Wake the parked node via the typed-message path —
	// the only legitimate post-spec wake mechanism for a parked node
	// outside the deadline-elapsed / max-park-overrun paths.
	// @decision: test-harness-invalidate-node-retired
	h.PostInstanceMessage(iid, "test/wake/parker", nil,
		fmt.Sprintf("test-wake-%s-parker", t.Name()))

	require.True(t, h.WaitForNodeState(parker.ID, cascade.NodeStateFresh, 30*time.Second),
		"parker should reach fresh after the typed-message wake")

	// @constraint: Only after the parked work resolves and the frame
	// ends does terminate_after_run fire. terminated_at must not have
	// been set while parked (asserted above); it becomes set once the
	// resolved frame ends.
	require.True(t, waitForInstanceTerminated(t, h, iid, 30*time.Second),
		"instance must terminate only after the parked work resolves and the frame ends (terminate_after_run)")
}

// waitForHeldFrame polls GET /v1/admin/diagnostics/held-frames until a
// frame for `instanceID` containing `nodeID` appears, or the deadline
// elapses. Returns true on the first sighting.
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
