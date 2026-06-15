// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Acceptance scenario 2 — opt-in terminate_after_run against the real
// assembled product (the rimsky-all-in-one image via the services
// testcontainers harness). An instance created with
// `terminate_after_run: true` runs the graph exactly once (its initial
// frame drives the bundled Success-stub executor to a real terminal
// Success over the wire), then self-terminates at that frame-end. A
// subsequent message POST is rejected because the instance is terminal.
//
// The behavior under test is rimsky's OWN lifecycle (terminate-after-one-
// run, strict). The Success stub is the real per-node trigger — it emits a
// genuine terminal Success against a real node-run — it is not the
// component the feature exists to exercise; rimsky's terminal predicate is.
//
// Contrast with TestSensorHTTP_DurableAcrossFires (no flag, stays alive
// across N fires): the only difference between the two instances is the
// `terminate_after_run` flag, and the two acceptance gates pin the two
// opposite lifecycles end to end.
package scenarios

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestTerminateAfterRun_EndToEnd proves the opt-in single-run lifecycle.
//
// @story: terminate-after-run
func TestTerminateAfterRun_EndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: the stub executor must be reachable on the shared network
	// before rimsky starts (the control-api fires a Capabilities handshake
	// against declared executors at startup). Network → executor peer → rimsky.
	netName := harness.NewNetwork(ctx, t)
	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", execEP),
	)

	// @constraint: a single executor node. Creating the instance enqueues an
	// initial frame for the root node (`code:lib/control/controlapi/instances.go`
	// Phase 2), which dispatches the stub executor to a real terminal Success —
	// that one frame is the single run terminate_after_run permits.
	templateID := deployTerminateAfterRunTemplate(t, ep)
	instanceID := createTerminateAfterRunInstance(t, ep, templateID, "ck-terminate-after-run")

	// @deliberate: the one run lands the worker in `fresh` with a real
	// work_started dispatch — proving the executor actually ran, not just a
	// default state.
	waitForDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)

	// @constraint: at that frame-end the strict terminal predicate fires
	// (terminate_after_run gated): `route:GET /instances/{id}` shows
	// terminated_at set. Poll because the frame-end transition lands shortly
	// after the node settles to fresh.
	requireInstanceTerminated(t, ep, instanceID, 60*time.Second)

	// @constraint: a follow-up message to the now-terminated instance is
	// rejected with 409 Conflict (`code:lib/control/controlapi/messages.go::errInstanceTerminated`).
	// Carry the universal Idempotency-Key header so the request is otherwise
	// well-formed and the rejection is unambiguously on the terminal state.
	requireMessageRejectedTerminated(t, ep, instanceID, "worker")
}

// deployTerminateAfterRunTemplate POSTs a single-node executor template and
// deploys it. Returns the template id.
func deployTerminateAfterRunTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	return deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "terminate-after-run",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
				},
			},
		},
	})
}

// createTerminateAfterRunInstance POSTs a new instance WITH
// terminate_after_run: true against the real POST /instances path and
// returns its instance_id. The flag rides createInstanceRequest →
// provisionArgs → InstanceCreateInput → the persisted column, then governs
// the terminal predicate at frame-end.
func createTerminateAfterRunInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":            templateID,
		"instance_key":        instanceKey,
		"params":              map[string]any{},
		"terminate_after_run": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	// @deliberate: confirm the flag round-tripped onto the GET projection —
	// the wire thread-through is itself part of the feature under test.
	gstatus, graw := ep.GetJSON(t, "/v1/instances/"+resp.InstanceID, "")
	if gstatus != http.StatusOK {
		t.Fatalf("GET /instances/%s: %d %s", resp.InstanceID, gstatus, string(graw))
	}
	var gresp struct {
		TerminateAfterRun bool `json:"terminate_after_run"`
	}
	if err := json.Unmarshal(graw, &gresp); err != nil {
		t.Fatalf("decode instance %s: %v: %s", resp.InstanceID, err, string(graw))
	}
	if !gresp.TerminateAfterRun {
		t.Fatalf("instance %s created with terminate_after_run:true but GET shows false — "+
			"the flag did not thread through create → persist → projection: %s",
			resp.InstanceID, string(graw))
	}
	return resp.InstanceID
}

// requireInstanceTerminated polls GET /instances/{id} until terminated_at
// is set (the strict terminate-after-one-run fired at frame-end) or the
// deadline elapses. Fails hard on timeout.
func requireInstanceTerminated(t *testing.T, ep harness.RimskyEndpoint, instanceID string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastBody string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/instances/"+instanceID, "")
		if status == http.StatusOK {
			lastBody = string(raw)
			var resp struct {
				TerminatedAt *string `json:"terminated_at"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				if resp.TerminatedAt != nil && *resp.TerminatedAt != "" {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("terminate_after_run instance %s never reached terminal (terminated_at unset) within %v — "+
		"the strict terminal predicate did not fire at the single frame's end; last GET=%s",
		instanceID, deadline, lastBody)
}

// requireMessageRejectedTerminated asserts a follow-up invalidate message
// to the terminated instance is rejected with 409 Conflict — the
// terminated-instance rejection path. A 2xx here would mean a terminated
// instance still accepts work.
func requireMessageRejectedTerminated(t *testing.T, ep harness.RimskyEndpoint, instanceID, target string) {
	t.Helper()
	path := "/v1/instances/" + instanceID + "/messages"
	body := map[string]any{
		"kind":   "invalidate",
		"target": target,
	}
	status, raw := postJSONWithIdempotencyKey(t, ep, path, body, "terminate-after-run-followup-"+uuid.NewString())
	if status != http.StatusConflict {
		t.Fatalf("POST %s to a terminated instance returned %d (want 409 Conflict) — "+
			"a terminated instance must reject further messages: %s", path, status, string(raw))
	}
	if !strings.Contains(string(raw), "terminated") {
		t.Fatalf("POST %s rejected with 409 but the error body does not mention termination: %s",
			path, string(raw))
	}
}

// postJSONWithIdempotencyKey POSTs body to ep.BaseURL+path carrying the
// universal Idempotency-Key header that every publisher/operator message
// emit must supply. Mirrors RimskyEndpoint.PostJSON, which does not set
// arbitrary headers.
func postJSONWithIdempotencyKey(t *testing.T, ep harness.RimskyEndpoint, path string, body any, idemKey string) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal POST %s: %v", path, err)
	}
	req, err := http.NewRequest(http.MethodPost, ep.BaseURL+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idemKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}
