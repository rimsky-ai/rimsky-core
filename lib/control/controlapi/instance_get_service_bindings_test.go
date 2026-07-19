// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestInstanceGet_ExposesServiceBindings(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	key, _ := h.mintActiveAPIKey(t, "creator", []map[string]any{{"action": "*"}})

	tplBody := map[string]any{
		"spec": map[string]any{
			"name":    "get-service-bindings-" + uuid.NewString(),
			"version": "v1",
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
			},
		},
	}
	status, body := h.httpPostAs(t, "/v1/templates", tplBody, key, "")
	if status != http.StatusCreated {
		t.Fatalf("register template: status=%d body=%v", status, body)
	}
	tplID, _ := body["template_id"].(string)
	status, body = h.httpPostAs(t, "/v1/templates/"+tplID+"/deploy", map[string]any{}, key, "")
	if status != http.StatusOK {
		t.Fatalf("deploy template: status=%d body=%v", status, body)
	}

	status, body = h.httpPostAs(t, "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
		"service_bindings": map[string]any{
			"codegen": map[string]any{"path": "/usr/local/bin/codegen-service"},
		},
	}, key, "")
	if status != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%v", status, body)
	}
	instID, _ := body["instance_id"].(string)

	getStatus, getBody := getFromSenderSubjectHarness(t, h, "/v1/instances/"+instID, key)
	if getStatus != http.StatusOK {
		t.Fatalf("get instance: status=%d body=%v", getStatus, getBody)
	}

	bindings, ok := getBody["service_bindings"].(map[string]any)
	if !ok {
		t.Fatalf("GET /v1/instances/{id} response missing service_bindings (or wrong shape); got %v (full body %v)",
			getBody["service_bindings"], getBody)
	}
	codegen, ok := bindings["codegen"].(map[string]any)
	if !ok {
		t.Fatalf("service_bindings.codegen missing or wrong shape; got %v", bindings)
	}
	if got := codegen["path"]; got != "/usr/local/bin/codegen-service" {
		t.Fatalf("service_bindings.codegen.path: got %v, want %q "+
			"(the cache-miss GET /instances/{id} fallback the host-agent-proxy dials on a cold cache "+
			"depends on this field round-tripping exactly)", got, "/usr/local/bin/codegen-service")
	}
}
