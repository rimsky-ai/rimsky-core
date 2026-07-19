// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: template-lifecycle
package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTemplateLifecycle_FullLifecycleEndToEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "lifecycle-done")

	specBody := buildLifecycleTemplateSpec(t, "project-alpha", "1")
	registerResp := postJSON(t, h.ControlBase+"/v1/templates",
		map[string]any{"spec": specBody})
	require.Equal(t, http.StatusCreated, registerResp.status,
		"POST /v1/templates with a valid spec must return 201 Created: %s", registerResp.bodyStr())
	templateHash := registerResp.stringField("template_id")
	require.True(t, strings.HasPrefix(templateHash, "sha256-"),
		"register response must carry a content hash (sha256-…); got %q", templateHash)

	getResp := getJSON(t, h.ControlBase+"/v1/templates/"+templateHash)
	require.Equal(t, http.StatusOK, getResp.status,
		"GET /v1/templates/{hash} on a registered template must return 200: %s",
		getResp.bodyStr())
	require.Equal(t, "registered", getResp.stringField("state"),
		"newly-registered template must be in state=registered")

	preValidateCount := listTemplateCount(t, h.ControlBase)
	validateSpec := buildLifecycleTemplateSpec(t, "project-validate-only", "9")
	validateResp := postJSON(t, h.ControlBase+"/v1/templates/validate",
		map[string]any{"spec": validateSpec})
	require.Equal(t, http.StatusOK, validateResp.status,
		"POST /v1/templates/validate must return 200 with the verdict in the body: %s",
		validateResp.bodyStr())
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

	iid := h.CreateInstance(templateHash, "ck-lifecycle", map[string]any{})
	workerNode := h.FindNode(iid, "worker")
	require.NotNil(t, workerNode,
		"after deploy, POST /v1/instances must materialize the template's worker node")
	h.WaitForNodeState(workerNode.ID, cascade.NodeStateFresh)

	deleteWhileDeployedResp := doRequest(t, http.MethodDelete,
		h.ControlBase+"/v1/templates/"+templateHash, nil)
	require.Equal(t, http.StatusConflict, deleteWhileDeployedResp.status,
		"DELETE /v1/templates/{hash} while state=deployed must return 409 "+
			"(the spec's `delete succeeds while live instances reference the "+
			"template` falsifier — a deployed template is by definition still "+
			"reachable for new instance creation): %s",
		deleteWhileDeployedResp.bodyStr())

	terminateResp := postJSON(t, h.ControlBase+"/v1/instances/"+iid.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, terminateResp.status,
		"POST /v1/instances/{id}/terminate must succeed (we need active=0 to "+
			"undeploy): %s", terminateResp.bodyStr())
	waitForInstanceTerminated(t, h, iid)

	undeployResp := postJSON(t, h.ControlBase+"/v1/templates/"+templateHash+"/undeploy",
		map[string]any{})
	require.Equal(t, http.StatusOK, undeployResp.status,
		"POST /v1/templates/{hash}/undeploy must return 200 once active=0: %s",
		undeployResp.bodyStr())
	require.Equal(t, "undeployed", undeployResp.stringField("state"),
		"undeploy response must report state=undeployed")

	postUndeployInstanceAttempt := postJSON(t, h.ControlBase+"/v1/instances",
		map[string]any{"template": templateHash, "params": map[string]any{}})
	require.GreaterOrEqual(t, postUndeployInstanceAttempt.status, 400,
		"POST /v1/instances against an undeployed template must be refused "+
			"(4xx) — got %d: %s",
		postUndeployInstanceAttempt.status, postUndeployInstanceAttempt.bodyStr())
	require.Less(t, postUndeployInstanceAttempt.status, 500,
		"refusal must be a 4xx client error, not a 5xx server error")

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

	getAfterDelete := getJSON(t, h.ControlBase+"/v1/templates/"+templateHash)
	require.Equal(t, http.StatusNotFound, getAfterDelete.status,
		"GET /v1/templates/{hash} after DELETE must return 404")
}

func buildLifecycleTemplateSpec(t *testing.T, name, version string) map[string]any {
	t.Helper()
	return map[string]any{
		"name":    name,
		"version": version,
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
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}
