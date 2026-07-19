// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestInstanceCreate_CreatedByAPIKeyIDMatchesCreatingIdentity_NotTemplateDeployer(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	deployerKey, deployerID := h.mintActiveAPIKey(t, "deployer", []map[string]any{{"action": "*"}})
	creatorKey, creatorID := h.mintActiveAPIKey(t, "creator", []map[string]any{{"action": "*"}})
	if deployerID == creatorID {
		t.Fatalf("test setup: deployer and creator keys must differ")
	}

	tplBody := map[string]any{
		"spec": map[string]any{
			"name":    "created-by-distinct-" + uuid.NewString(),
			"version": "v1",
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
			},
		},
	}
	status, body := h.httpPostAs(t, "/v1/templates", tplBody, deployerKey, "")
	if status != http.StatusCreated {
		t.Fatalf("deploy template body: status=%d body=%v", status, body)
	}
	tplID, _ := body["template_id"].(string)
	status, body = h.httpPostAs(t, "/v1/templates/"+tplID+"/deploy", map[string]any{}, deployerKey, "")
	if status != http.StatusOK {
		t.Fatalf("deploy template: status=%d body=%v", status, body)
	}

	status, body = h.httpPostAs(t, "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	}, creatorKey, "")
	if status != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%v", status, body)
	}
	instID, _ := body["instance_id"].(string)

	getStatus, getBody := getFromSenderSubjectHarness(t, h, "/v1/instances/"+instID, creatorKey)
	if getStatus != http.StatusOK {
		t.Fatalf("get instance: status=%d body=%v", getStatus, getBody)
	}
	got, _ := getBody["created_by_api_key_id"].(string)
	if got != creatorID.String() {
		t.Fatalf("created_by_api_key_id: got %q want the creating identity's key id %q "+
			"(the template-deploying key %q must not leak through)", got, creatorID.String(), deployerID.String())
	}
}

func TestInstanceCreate_AnonymousModeYieldsNilCreatedByAPIKeyID(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	tplBody := map[string]any{
		"spec": map[string]any{
			"name":    "created-by-anon-" + uuid.NewString(),
			"version": "v1",
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
			},
		},
	}
	status, body := h.httpPostAs(t, "/v1/templates", tplBody, "", "")
	if status != http.StatusCreated {
		t.Fatalf("deploy template body (anonymous): status=%d body=%v", status, body)
	}
	tplID, _ := body["template_id"].(string)
	status, body = h.httpPostAs(t, "/v1/templates/"+tplID+"/deploy", map[string]any{}, "", "")
	if status != http.StatusOK {
		t.Fatalf("deploy template (anonymous): status=%d body=%v", status, body)
	}

	status, body = h.httpPostAs(t, "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	}, "", "")
	if status != http.StatusCreated {
		t.Fatalf("create instance (anonymous): status=%d body=%v", status, body)
	}
	instID, _ := body["instance_id"].(string)

	getStatus, getBody := getFromSenderSubjectHarness(t, h, "/v1/instances/"+instID, "")
	if getStatus != http.StatusOK {
		t.Fatalf("get instance: status=%d body=%v", getStatus, getBody)
	}
	if got, present := getBody["created_by_api_key_id"]; present {
		t.Fatalf("created_by_api_key_id: got %v want absent/NULL for an anonymous-created instance "+
			"(ident.KeyID must be nil, not a bogus non-nil fallback)", got)
	}
}

func getFromSenderSubjectHarness(t *testing.T, h *senderSubjectHarness, path, bearer string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return resp.StatusCode, out
}
