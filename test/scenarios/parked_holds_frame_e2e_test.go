// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pass-7 acceptance gate for the 2026-06-03 durable-by-default lifecycle
// spec, scenario 3 ("Parked holds the frame"). Proves end-to-end against
// the real runtime (real control-api over HTTP, real supervisor +
// async-callback listener, real scheduler + frame engine, testcontainers
// Postgres) that:
//
//   - A `parked` node_run holds its frame open — the frame stays
//     `running`/held and the held-frames diagnostic reports it (the Pass-1
//     frame-end fix: `ListRunningFramesNoPendingNodes` now treats parked as
//     unresolved).
//   - The instance is NOT terminated while parked, even though it was
//     created with `terminate_after_run = true` (the Pass-3 parked-aware
//     instance-terminal guard).
//   - Only after the parked node is woken (admin invalidate), resumes, and
//     resolves to Success — i.e. the frame genuinely ends — does
//     `terminate_after_run` fire and stamp `terminated_at` (the Pass-3
//     strict "terminate after the next frame ends" semantics).
//
// Wake mechanism (load-bearing): a `parked` node is NOT woken by the
// `/v1/callback` endpoint (that endpoint serves the separate
// AwaitAsyncCallback terminal, which keeps a node `running`; a Park
// terminal registers no async_ack_id and the callback handler rejects a
// parked run). A parked node is woken only by admin/cascade invalidate or
// the snooze sweep. This test uses the true park + admin-invalidate wake
// path, modeled on parked_lifecycle_test.go::
// TestParkedLifecycleResumeOnExternalInvalidate. This grounds the spec's
// scenario-3 wording "awaiting an async callback" to the real parked-node
// wake path; the spec's intent — "a parked node holds its frame open and
// the instance is not terminated until the parked work resolves" — is
// preserved exactly.
package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// instanceProjection mirrors the fields of the control-api's GET
// /instances/{id} JSON body that this test reads. The handler's struct is
// unexported, so the test decodes the wire JSON into this local shape.
// terminated_at is omitempty on the wire (nil pointer ⇒ field absent).
type instanceProjection struct {
	ID                string     `json:"id"`
	TerminateAfterRun bool       `json:"terminate_after_run"`
	TerminatedAt      *time.Time `json:"terminated_at,omitempty"`
}

func TestParkedHoldsFrame_EndToEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// The worker parks indefinitely on first dispatch (no resume_at), so the
	// frame stays held until an external invalidate wakes it. The resolving
	// Success script is registered after the parked-state probes below (the
	// same ordering parked_lifecycle_test.go uses).
	h.Stub.WhenType("worker").
		Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "await_callback", []byte(`{"ticket":"R-7"}`), time.Time{}, "")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-holds-frame", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})

	// Create the instance with terminate_after_run = true via the real HTTP
	// create path (the harness CreateInstance helper does not set the flag).
	iid := createInstanceTerminateAfterRun(t, h, tid)

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// --- Parked: the frame is held, the instance is NOT terminated --------
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateParked, 30*time.Second),
		"worker should reach parked")

	// The instance must NOT be terminated while a node is parked, even with
	// terminate_after_run set (Pass-3 parked-aware instance-terminal guard).
	require.Nil(t, getInstance(t, h, iid).TerminatedAt,
		"instance must NOT be terminated while a node is parked, even with terminate_after_run set")

	// The held-frames diagnostic must report this frame held (running) —
	// the Pass-1 fix keeps the frame open while the node is parked, so the
	// diagnostic (running frame + parked node_run) and the frame-end rule
	// agree.
	require.True(t, waitForHeldFrame(t, h, worker.ID.String(), 10*time.Second),
		"held-frames diagnostic should report the frame held while the node is parked")

	// --- Wake: resume → resolve → frame ends → only THEN terminate -------
	// Re-script the worker so the resume dispatch resolves to Success.
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "after-callback")

	// Wake via admin invalidate (NOT /v1/callback — a parked node is not
	// woken by the callback endpoint).
	resp, err := http.Post(
		h.ControlBase+"/admin/instances/"+worker.InstanceID.String()+"/nodes/"+worker.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)),
	)
	require.NoError(t, err)
	resp.Body.Close()

	require.True(t, h.WaitForEventKind(worker.ID, "parked_resume_started", 10*time.Second),
		"admin invalidate should wake the parked node")
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should resolve to Success after the wake dispatch")

	// Only after the real frame-end does terminate_after_run fire. terminated_at
	// must not have been set while parked (asserted above); it becomes set once
	// the resolved frame ends.
	require.True(t, waitForInstanceTerminated(t, h, iid, 30*time.Second),
		"instance must terminate only after the parked work resolves and the frame ends (terminate_after_run)")
}

// createInstanceTerminateAfterRun POSTs an instance-create with
// terminate_after_run=true through the real HTTP create path, then waits
// for root dispatch (mirroring the harness CreateInstance helper, which
// does not expose the flag).
func createInstanceTerminateAfterRun(t *testing.T, h *scenario.Harness, templateHash string) shared.UUID {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"template":            templateHash,
		"params":              map[string]any{},
		"terminate_after_run": true,
	})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/instances", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("create instance: status %d: %s", resp.StatusCode, string(buf))
	}
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	id, err := uuid.Parse(out.InstanceID)
	require.NoError(t, err)

	// Confirm the flag round-trips on the GET projection (the thread-through
	// is what makes terminate_after_run reach the instance-terminal predicate).
	require.True(t, getInstance(t, h, id).TerminateAfterRun,
		"created instance should report terminate_after_run=true")

	// Wait for the worker's root dispatch to land (a node_run row exists).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs d
		               JOIN rimsky_nodes n ON n.id = d.node_id
		               WHERE n.instance_id = $1`, []any{id}, &count)
		if count > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return id
}

// getInstance fetches GET /instances/{id} and decodes the projection.
func getInstance(t *testing.T, h *scenario.Harness, id shared.UUID) instanceProjection {
	t.Helper()
	resp, err := http.Get(h.ControlBase + "/instances/" + id.String())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET /instances/{id} should return 200")
	var item instanceProjection
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&item))
	return item
}

func waitForInstanceTerminated(t *testing.T, h *scenario.Harness, id shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if getInstance(t, h, id).TerminatedAt != nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// waitForHeldFrame polls GET /admin/diagnostics/held-frames until a held
// frame bucket lists the given node id.
func waitForHeldFrame(t *testing.T, h *scenario.Harness, nodeID string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(h.ControlBase + "/admin/diagnostics/held-frames")
		if err == nil {
			var body controlapi.HeldFramesResponse
			decErr := json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if decErr == nil {
				for _, f := range body.Frames {
					for _, nid := range f.NodeIDs {
						if nid == nodeID {
							return true
						}
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
