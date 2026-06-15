// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-template-lifecycle end-to-end against the real assembled stack.
//
// Drives the full curation lifecycle for a workflow definition through
// the real control-API (/v1/templates surface) plus the real supervisor
// + scheduler + persistence. The proof exhibits, in order, every clause
// of STORY-template-lifecycle's Acceptance:
//
//  1. POST /v1/templates with a valid spec returns 201 + a content hash;
//     a follow-up GET /v1/templates/{hash} returns the persisted row.
//  2. POST /v1/templates/validate runs the full validation pipeline on a
//     DIFFERENT spec and reports findings without persisting (a follow-up
//     GET /v1/templates shows the same row count as before the validate
//     call — pre-flight validation does NOT persist).
//  3. POST /v1/templates/{hash}/deploy flips deploy state; from that
//     moment, POST /v1/instances against the hash returns 201 and the
//     supervisor begins driving the instance (one stub worker node
//     dispatches and resolves).
//  4. DELETE /v1/templates/{hash} while the template is deployed returns
//     409 (the deployed-state guard refuses to drop a live definition).
//  5. Once the instance reaches terminal AND the template is undeployed
//     (POST /v1/templates/{hash}/undeploy succeeds when active=0), a
//     subsequent POST /v1/instances against the hash returns 4xx (the
//     `state != deployed` guard refuses instance creation against an
//     undeployed template — the falsifier's first clause).
//  6. The template row still exists post-undeploy; DELETE the instance
//     row, then DELETE the template — both succeed (the catalog is
//     retired cleanly only after no instance row references it AND it
//     has been undeployed).
//
// The proof drives the REAL control-API HTTP routes against the REAL
// in-process supervisor/scheduler (no in-process handler calls, no
// stubbed value-delivering component — the stub executor IS the bundled
// scenario stub that runs the real gRPC dispatch path). It is the
// executable proof STORY-template-lifecycle's Proof line names.
//
// @story: template-lifecycle
package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTemplateLifecycle_FullLifecycleEndToEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: Stub script the single worker node so the cascade resolves through
	// the REAL supervisor dispatch path (no canned handler returns); the
	// node's resolved attributes carry the executor's delta on terminal.
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "lifecycle-done")

	// @deliberate: (1) POST /v1/templates with a valid spec — register WITHOUT deploying,
	// so we can independently exhibit the deploy/undeploy state transitions
	// below. The harness's DeployTemplate helper bundles register+deploy;
	// here we hit the raw HTTP routes one at a time. Canonical naming
	// (`project-alpha`) per decision:project-agnostic.
	specBody := buildLifecycleTemplateSpec(t, "project-alpha", "1")
	registerResp := postJSON(t, h.ControlBase+"/v1/templates",
		map[string]any{"spec": specBody})
	require.Equal(t, http.StatusCreated, registerResp.status,
		"POST /v1/templates with a valid spec must return 201 Created: %s", registerResp.bodyStr())
	templateHash := registerResp.stringField("template_id")
	require.True(t, strings.HasPrefix(templateHash, "sha256-"),
		"register response must carry a content hash (sha256-…); got %q", templateHash)

	// @constraint: (1b) GET /v1/templates/{hash} — the persisted row is readable by hash
	// and reports state=registered (the post-register, pre-deploy state).
	getResp := getJSON(t, h.ControlBase+"/v1/templates/"+templateHash)
	require.Equal(t, http.StatusOK, getResp.status,
		"GET /v1/templates/{hash} on a registered template must return 200: %s",
		getResp.bodyStr())
	require.Equal(t, "registered", getResp.stringField("state"),
		"newly-registered template must be in state=registered")

	// @constraint: (2) POST /v1/templates/validate — pre-flight validation against a
	// DIFFERENT spec (so we can prove this run did not persist by counting
	// rows before-and-after the validate call). The validate path always
	// returns HTTP 200 (verdict in body, not status).
	preValidateCount := listTemplateCount(t, h.ControlBase)
	validateSpec := buildLifecycleTemplateSpec(t, "project-validate-only", "9")
	validateResp := postJSON(t, h.ControlBase+"/v1/templates/validate",
		map[string]any{"spec": validateSpec})
	require.Equal(t, http.StatusOK, validateResp.status,
		"POST /v1/templates/validate must return 200 with the verdict in the body: %s",
		validateResp.bodyStr())
	// @constraint: Verdict carries the `ok` boolean and a `validation_errors` array,
	// independent of pass/fail (the contract is "findings without persisting").
	_, okBool := validateResp.body["ok"].(bool)
	require.True(t, okBool, "validate response must carry a boolean `ok` verdict")
	_, errsPresent := validateResp.body["validation_errors"]
	require.True(t, errsPresent, "validate response must carry a validation_errors array")
	postValidateCount := listTemplateCount(t, h.ControlBase)
	require.Equal(t, preValidateCount, postValidateCount,
		"POST /v1/templates/validate must NOT persist; row count unchanged "+
			"(before=%d after=%d) — the spec's `pre-flight validation persists` "+
			"falsifier is what this assertion negates",
		preValidateCount, postValidateCount)

	// @constraint: (3) POST /v1/templates/{hash}/deploy — the registered template flips
	// to state=deployed. Before deploy, instance creation MUST be refused
	// (the `state != deployed` guard); we exhibit the gating clause by
	// attempting an instance create FIRST.
	preDeployInstanceAttempt := postJSON(t, h.ControlBase+"/v1/instances",
		map[string]any{"template": templateHash, "params": map[string]any{}})
	require.GreaterOrEqual(t, preDeployInstanceAttempt.status, 400,
		"POST /v1/instances against a registered (not-yet-deployed) template "+
			"must be refused (4xx) — got %d: %s",
		preDeployInstanceAttempt.status, preDeployInstanceAttempt.bodyStr())
	require.Less(t, preDeployInstanceAttempt.status, 500,
		"refusal must be a 4xx client error, not a 5xx server error")

	deployResp := postJSON(t, h.ControlBase+"/v1/templates/"+templateHash+"/deploy",
		map[string]any{})
	require.Equal(t, http.StatusOK, deployResp.status,
		"POST /v1/templates/{hash}/deploy must return 200: %s",
		deployResp.bodyStr())
	require.Equal(t, "deployed", deployResp.stringField("state"),
		"deploy response must report state=deployed")

	// @deliberate: Now instance creation succeeds — and the supervisor begins driving
	// the instance through the real dispatch path. We use the harness's
	// CreateInstance helper so the root-dispatch wait is bundled.
	iid := h.CreateInstance(templateHash, "ck-lifecycle", map[string]any{})
	workerNode := h.FindNode(iid, "worker")
	require.NotNil(t, workerNode,
		"after deploy, POST /v1/instances must materialize the template's worker node")
	require.True(t, h.WaitForNodeState(workerNode.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker node must reach the terminal `fresh` state via the real supervisor "+
			"dispatch path — the `deployed-vs-undeployed state is recorded but not "+
			"gated on at instance creation` falsifier requires that the supervisor "+
			"actually drives the instance once deployed (not just that the row was "+
			"inserted)")

	// @constraint: (4) DELETE /v1/templates/{hash} while the template is deployed must
	// return 409 (the deployed-state guard refuses to drop a live
	// definition). This is the first protection on the catalog: a deployed
	// template can never be deleted, regardless of whether instances exist.
	deleteWhileDeployedResp := doRequest(t, http.MethodDelete,
		h.ControlBase+"/v1/templates/"+templateHash, nil)
	require.Equal(t, http.StatusConflict, deleteWhileDeployedResp.status,
		"DELETE /v1/templates/{hash} while state=deployed must return 409 "+
			"(the spec's `delete succeeds while live instances reference the "+
			"template` falsifier — a deployed template is by definition still "+
			"reachable for new instance creation): %s",
		deleteWhileDeployedResp.bodyStr())

	// @constraint: (5) Terminate the instance so the undeploy precondition (active=0)
	// holds, then POST /v1/templates/{hash}/undeploy — the row flips to
	// state=undeployed. From that moment, POST /v1/instances MUST be
	// refused (the same `state != deployed` guard now fires for the
	// undeployed state).
	terminateResp := postJSON(t, h.ControlBase+"/v1/instances/"+iid.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, terminateResp.status,
		"POST /v1/instances/{id}/terminate must succeed (we need active=0 to "+
			"undeploy): %s", terminateResp.bodyStr())
	require.True(t, waitForInstanceTerminatedLifecycle(t, h, iid, 30*time.Second),
		"instance must reach terminal (terminated_at set) before undeploy will "+
			"succeed — the undeploy handler refuses when active > 0")

	undeployResp := postJSON(t, h.ControlBase+"/v1/templates/"+templateHash+"/undeploy",
		map[string]any{})
	require.Equal(t, http.StatusOK, undeployResp.status,
		"POST /v1/templates/{hash}/undeploy must return 200 once active=0: %s",
		undeployResp.bodyStr())
	require.Equal(t, "undeployed", undeployResp.stringField("state"),
		"undeploy response must report state=undeployed")

	// @deliberate: Post-undeploy: instance creation against the same hash is refused
	// (4xx) — this is the second half of the falsifier's first clause
	// ("Deployed-vs-undeployed state is recorded but not gated on at
	// instance creation"). Note: rejection is the `state != deployed`
	// guard, the same gate that refused the pre-deploy attempt above —
	// the gate is symmetric, not one-shot.
	postUndeployInstanceAttempt := postJSON(t, h.ControlBase+"/v1/instances",
		map[string]any{"template": templateHash, "params": map[string]any{}})
	require.GreaterOrEqual(t, postUndeployInstanceAttempt.status, 400,
		"POST /v1/instances against an undeployed template must be refused "+
			"(4xx) — got %d: %s",
		postUndeployInstanceAttempt.status, postUndeployInstanceAttempt.bodyStr())
	require.Less(t, postUndeployInstanceAttempt.status, 500,
		"refusal must be a 4xx client error, not a 5xx server error")

	// @deliberate: (6) DELETE /v1/templates/{hash} — once the template is undeployed
	// AND no active instances reference it, the catalog row can be retired.
	// The current implementation returns 200 {"deleted": true} (the plan
	// text says 204, but the handler's actual contract is 200 + body; we
	// match the implementation since the falsifier's negation is about
	// `delete succeeds while live instances reference the template`, NOT
	// about the success status code shape).
	//
	// First retire the (terminated) instance row so the template DELETE is
	// hit on a clean slate per the plan's "after the instance is removed"
	// step. The terminator already closed the run-scope; DELETE on a
	// terminated instance succeeds.
	deleteInstanceResp := doRequest(t, http.MethodDelete,
		h.ControlBase+"/v1/instances/"+iid.String(), nil)
	require.Equal(t, http.StatusOK, deleteInstanceResp.status,
		"DELETE /v1/instances/{id} on a terminated instance must succeed: %s",
		deleteInstanceResp.bodyStr())

	deleteTemplateResp := doRequest(t, http.MethodDelete,
		h.ControlBase+"/v1/templates/"+templateHash, nil)
	require.Equal(t, http.StatusOK, deleteTemplateResp.status,
		"DELETE /v1/templates/{hash} after undeploy + instance removal must "+
			"succeed: %s", deleteTemplateResp.bodyStr())
	require.Contains(t, deleteTemplateResp.bodyStr(), `"deleted":true`,
		"DELETE response must report deleted:true")

	// @constraint: Post-DELETE: a follow-up GET 404s (the row is gone).
	getAfterDelete := getJSON(t, h.ControlBase+"/v1/templates/"+templateHash)
	require.Equal(t, http.StatusNotFound, getAfterDelete.status,
		"GET /v1/templates/{hash} after DELETE must return 404")
}

// buildLifecycleTemplateSpec returns the wrapped JSON `spec:` body for a
// minimal one-node template the lifecycle test registers and exercises.
// Lives in the test file (not the harness) because the harness's
// templateSpecToJSON helper is unexported and this proof needs to issue
// raw register / validate POSTs against the control-API without going
// through the harness's bundled register+deploy helper. Canonical naming
// (`project-alpha` / `project-validate-only`) per decision:project-agnostic.
func buildLifecycleTemplateSpec(t *testing.T, name, version string) map[string]any {
	t.Helper()
	return map[string]any{
		"name":                  name,
		"version":               version,
		"frame_resolution_mode": "serial_queue",
		"nodes": []map[string]any{
			{
				"type":     "worker",
				"executor": "stub",
				"attributes": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"ok": map[string]any{"type": "boolean"},
						},
					},
				},
			},
		},
	}
}

// listTemplateCount reads `GET /v1/templates` and returns the count of
// rows in the listing. Used to prove validate-only does NOT persist
// (the count before-and-after a validate call must be equal).
func listTemplateCount(t *testing.T, base string) int {
	t.Helper()
	resp := getJSON(t, base+"/v1/templates")
	require.Equal(t, http.StatusOK, resp.status,
		"GET /v1/templates must return 200: %s", resp.bodyStr())
	templates, ok := resp.body["templates"].([]any)
	if !ok {
		return 0
	}
	return len(templates)
}

// waitForInstanceTerminatedLifecycle polls until terminated_at is set on
// the instance row. Local helper (sibling tests have their own to avoid
// the cold-read coupling cost of pulling one out to scenario/harness for
// a single-test consumer).
func waitForInstanceTerminatedLifecycle(t *testing.T, h *scenario.Harness, iid shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var terminatedAt *time.Time
		h.QueryRowSQL(
			`SELECT terminated_at FROM rimsky_instances WHERE id = $1`,
			[]any{iid}, &terminatedAt)
		if terminatedAt != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// jsonResp is a thin wrapper around an HTTP response with the body
// already drained + JSON-decoded (when shape is decodable). The fields
// are read by the lifecycle assertions; keeping them on a tiny struct
// makes the per-step intent legible (status / decoded JSON / raw bytes).
type jsonResp struct {
	status int
	body   map[string]any
	raw    []byte
}

func (r jsonResp) stringField(key string) string {
	if r.body == nil {
		return ""
	}
	if v, ok := r.body[key].(string); ok {
		return v
	}
	return ""
}

func (r jsonResp) bodyStr() string {
	return string(r.raw)
}

func postJSON(t *testing.T, url string, body any) jsonResp {
	t.Helper()
	return doRequest(t, http.MethodPost, url, body)
}

func getJSON(t *testing.T, url string) jsonResp {
	t.Helper()
	return doRequest(t, http.MethodGet, url, nil)
}

func doRequest(t *testing.T, method, url string, body any) jsonResp {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(t, err, "marshal request body")
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := jsonResp{status: resp.StatusCode, raw: raw}
	if len(raw) > 0 {
		// @deliberate: Tolerate non-JSON responses (e.g. an empty body or HTML); the
		// status code is the load-bearing field for those.
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}
