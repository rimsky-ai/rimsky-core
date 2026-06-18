// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

// @story: terminate-after-run
func TestTerminateAfterRun_EndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", execEP),
	)

	templateID := deployTerminateAfterRunTemplate(t, ep)
	instanceID := createTerminateAfterRunInstance(t, ep, templateID, "ck-terminate-after-run")

	waitForDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)

	requireInstanceTerminated(t, ep, instanceID, 60*time.Second)

	requireMessageRejectedTerminated(t, ep, instanceID, "worker")
}

func deployTerminateAfterRunTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	return deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":             "terminate-after-run",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"messages": []map[string]any{
				{
					"type": "term/probe",
					"body_schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"target": map[string]any{"type": "string"},
						},
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
				},
			},
		},
	})
}

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
	// @decision: test-harness-create-instance-wakes-roots-after-create
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "terminate-after-run", instanceKey)
	return resp.InstanceID
}

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

func requireMessageRejectedTerminated(t *testing.T, ep harness.RimskyEndpoint, instanceID, target string) {
	t.Helper()
	path := "/v1/instances/" + instanceID + "/messages"
	body := map[string]any{
		"type": "term/probe",
		"payload": map[string]any{
			"target": target,
		},
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
