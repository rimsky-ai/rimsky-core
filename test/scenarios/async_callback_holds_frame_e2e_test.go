// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pass-7 acceptance gate for the 2026-06-03 durable-by-default lifecycle
// spec, scenario 3 ("Outstanding work holds the frame"), expressed via
// the async-callback flavor. Proves end-to-end against the real runtime
// (real control-api over HTTP, real supervisor + async-callback listener,
// real scheduler + frame engine, testcontainers Postgres) that:
//
//   - A node_run with outstanding async work holds its frame open — the
//     frame stays `running` until the callback resolves.
//   - The instance is NOT terminated while async work is outstanding, even
//     though it was created with `terminate_after_run = true` (the Pass-3
//     instance-terminal guard treats async-callback-pending as unresolved
//     identically to parked).
//   - Only after the outstanding work resolves via the supervisor's
//     callback endpoint — i.e. the frame genuinely ends — does
//     `terminate_after_run` fire and stamp `terminated_at` (the Pass-3
//     strict "terminate after the next frame ends" semantics).
//
// Wake mechanism (load-bearing): the executor stub uses
// `AwaitAsyncCallback` to register an ack-id on the supervisor's pending-
// callback registry. The frame stays held while the ack is outstanding.
// Posting a real `success` body to `POST /v1/callback/{ack}` resolves the
// node through the same path a real out-of-process executor would use.
// This grounds the scenario test in the legitimate production wake path;
// no synthetic-envelope or test-only state-injection is used.
//
// The parked-state flavor of the same hold-the-frame property is covered
// by `parked_holds_frame_e2e_test.go::TestParkedHoldsFrame_EndToEnd`;
// that test exercises the held-frames diagnostic, which is scoped to
// parked nodes specifically.
package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
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

func TestAsyncCallbackHoldsFrame_EndToEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: The worker dispatch awaits an async callback. The node
	// stays running with a registered ack-id; the frame stays held until
	// the test posts a real success callback.
	h.Stub.WhenType("worker").AwaitAsyncCallback("ack-async-holds", 60_000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "async-callback-holds-frame", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})

	// @deliberate: Create the instance with terminate_after_run = true via the real HTTP
	// create path (the harness CreateInstance helper does not set the flag).
	iid := createInstanceTerminateAfterRun(t, h, tid)

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// @constraint: Running with outstanding async ack: the frame is
	// held, the instance is NOT terminated.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateRunning, 30*time.Second),
		"worker should reach running with the async ack outstanding")

	// @constraint: The instance must NOT be terminated while a node has
	// outstanding async work, even with terminate_after_run set
	// (instance-terminal guard).
	require.Nil(t, getInstance(t, h, iid).TerminatedAt,
		"instance must NOT be terminated while async work is outstanding, even with terminate_after_run set")

	// @constraint: While the worker has an outstanding async ack the
	// instance must not be terminated and the frame stays open — the
	// terminated_at assertion above proves the property. The
	// held-frames diagnostic is specifically scoped to parked nodes
	// (phase='parked'), so it does not apply under the
	// running-with-async-ack flavor; the structural property (frame
	// held while outstanding work exists, instance not terminated)
	// holds independent of the diagnostic. The parked-state variant
	// of the same hold-the-frame property is covered by
	// TestParkedHoldsFrame_EndToEnd, which exercises the diagnostic.

	// @deliberate: Resolve via the real supervisor callback endpoint with
	// a Success AsyncCallbackBody — the same path an out-of-process
	// executor would use to deliver its terminal asynchronously.
	cbURL := "http://" + h.Supervisor.CallbackAddr() + "/v1/callback/ack-async-holds"
	cbBody, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{},
			"changed":          true,
			"change_summary":   "after-callback",
		},
	})
	cbDeadline := time.Now().Add(10 * time.Second)
	var cbStatus int
	for time.Now().Before(cbDeadline) {
		resp, err := http.Post(cbURL, "application/json", bytes.NewReader(cbBody))
		require.NoError(t, err)
		cbStatus = resp.StatusCode
		_ = resp.Body.Close()
		if cbStatus == http.StatusOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, http.StatusOK, cbStatus, "supervisor callback did not become available")

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should resolve to Success after the async callback")

	// @constraint: Only after the real frame-end does terminate_after_run fire. terminated_at
	// must not have been set while outstanding work was pending (asserted above);
	// it becomes set once the resolved frame ends.
	require.True(t, waitForInstanceTerminated(t, h, iid, 30*time.Second),
		"instance must terminate only after the async work resolves and the frame ends (terminate_after_run)")
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
	resp, err := http.Post(h.ControlBase+"/v1/instances", "application/json", bytes.NewReader(body))
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

	// @deliberate: Confirm the flag round-trips on the GET projection (the thread-through
	// is what makes terminate_after_run reach the instance-terminal predicate).
	require.True(t, getInstance(t, h, id).TerminateAfterRun,
		"created instance should report terminate_after_run=true")

	// @constraint: post-spec instance creation is idle; the test
	// helper emits an empty-message wake so the structural-root worker
	// dispatches.
	// @decision: empty-message-as-root-trigger
	h.PostInstanceMessage(id, "", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

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
	resp, err := http.Get(h.ControlBase + "/v1/instances/" + id.String())
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
