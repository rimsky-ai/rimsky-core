// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: role-template
// @story: grant-scope-enforcement
package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const readOnlyRoleName = "read-only"

const readOnlyKeyName = "dashboard"

const readActionCount = 18

const absentIdentifier = "00000000-0000-0000-0000-0000000000ff"

func TestReadOnlyRoleGrantsEveryReadActionAndNoWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	stack := harness.StartAllInOneZeroConfig(ctx, t, "")
	ep := stack.Endpoint
	adminKey := mintAPIKey(t, ep, "", "read-only-probe-admin", []map[string]any{{"action": "*"}})
	templateID := deployScenarioTemplateAuth(t, ep, adminKey, map[string]any{
		"spec": map[string]any{
			"name":    "read-only-probe",
			"version": "1",
			"nodes":   []map[string]any{{"type": "work", "kind": "attribute_passthrough"}},
		},
	})
	instanceID := createInstanceAs(t, ep, adminKey, templateID, "read-only-probe-1")
	createTagAs(t, ep, adminKey, "read-only-probe:v1", templateID)

	readOnlyKey := mintKeyWithRole(t, ep.BaseURL, adminKey, readOnlyKeyName, readOnlyRoleName)
	requireGrantIsTheSingleReadWildcard(t, ep, adminKey)

	registry := controlapi.BuildV1Registry()
	pathValues := map[string]string{
		"id":              instanceID,
		"idOrKey":         instanceID,
		"run_id":          absentIdentifier,
		"claim_handle_id": absentIdentifier,
		"*":               "system/health",
	}

	readActions := actionsEndingInRead(registry)
	if len(readActions) != readActionCount {
		t.Fatalf("the action registry holds %d actions ending in :read (%v), and this test drives %d. A new "+
			"read action needs a route drive here before the role's coverage claim holds again",
			len(readActions), readActions, readActionCount)
	}
	for _, action := range readActions {
		method, path := firstRouteFor(t, registry, action, pathValues)
		status := driveAs(t, ep, readOnlyKey, action, method, path)
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			t.Errorf("%s: %s %s answered %d for the read-only key. The role's single *:read entry covers "+
				"every action ending in :read", action, method, path, status)
		}
	}

	for _, write := range readOnlyRefusedWrites(templateID, instanceID) {
		status, raw := ep.PostJSONWithHeaders(t, write.path, write.body,
			map[string]string{"Authorization": "Bearer " + readOnlyKey})
		if status != http.StatusForbidden {
			t.Errorf("%s: POST %s answered %d for the read-only key, want 403: %s",
				write.action, write.path, status, string(raw))
		}
	}

	for _, action := range []string{"instance:list-frames", "instance:read-frame"} {
		method, path := firstRouteFor(t, registry, action, map[string]string{
			"id": instanceID, "idOrKey": instanceID, "frame_id": absentIdentifier,
		})
		status := driveAs(t, ep, readOnlyKey, action, method, path)
		if status != http.StatusForbidden {
			t.Errorf("%s: %s %s answered %d for the read-only key, want 403 — the wildcard matches the whole "+
				"verb, and this action's verb is not read", action, method, path, status)
		}
	}
}

type refusedWrite struct {
	action string
	path   string
	body   map[string]any
}

func readOnlyRefusedWrites(templateID, instanceID string) []refusedWrite {
	return []refusedWrite{
		{action: "template:register", path: "/v1/templates", body: map[string]any{
			"spec": map[string]any{"name": "refused", "version": "1",
				"nodes": []map[string]any{{"type": "work", "kind": "attribute_passthrough"}}}}},
		{action: "instance:create", path: "/v1/instances", body: map[string]any{
			"template": templateID, "instance_key": "refused"}},
		{action: "instance:kill", path: "/v1/instances/" + instanceID + "/terminate", body: map[string]any{}},
		{action: "node:reset", path: "/v1/nodes/" + absentIdentifier + "/reset", body: map[string]any{}},
		{action: "auth:create", path: "/v1/auth/keys", body: map[string]any{
			"name": "refused", "permissions": []map[string]any{{"action": "*"}}}},
		{action: "tag:create", path: "/v1/tags", body: map[string]any{
			"tag": "refused:v1", "template": templateID}},
	}
}

func actionsEndingInRead(registry *controlapi.ActionRegistry) []string {
	out := []string{}
	for _, action := range registry.AllActions() {
		if strings.HasSuffix(action, ":read") {
			out = append(out, action)
		}
	}
	return out
}

func firstRouteFor(t *testing.T, registry *controlapi.ActionRegistry, action string,
	pathValues map[string]string) (method, path string) {
	t.Helper()
	entry, ok := registry.Entry(action)
	if !ok {
		t.Fatalf("the action registry holds no entry for %q", action)
	}
	if len(entry.Routes) == 0 {
		t.Fatalf("%s carries no route, so this test cannot drive the gate at it", action)
	}
	route := entry.Routes[0]
	segments := strings.Split(route.Path, "/")
	for i, seg := range segments {
		name := seg
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			name = strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		} else if seg != "*" {
			continue
		}
		value, ok := pathValues[name]
		if !ok {
			t.Fatalf("%s route %s names path parameter %q, which this test has no value for", action, route.Path, name)
		}
		segments[i] = value
	}
	return route.Method, strings.Join(segments, "/")
}

func driveAs(t *testing.T, ep harness.RimskyEndpoint, key, action, method, path string) int {
	t.Helper()
	switch method {
	case http.MethodGet:
		status, _ := ep.GetJSON(t, path, key)
		return status
	case http.MethodPost:
		body, ok := readRouteBodies[action]
		if !ok {
			t.Fatalf("%s is driven at POST %s, which needs a request body this test does not carry", action, path)
		}
		status, _ := ep.PostJSONWithHeaders(t, path, body, map[string]string{
			"Authorization":        "Bearer " + key,
			"Mcp-Protocol-Version": mcpProtocolVersion,
		})
		return status
	}
	t.Fatalf("%s is driven at %s %s, a method this test cannot send", action, method, path)
	return 0
}

const mcpProtocolVersion = "2025-06-18"

var readRouteBodies = map[string]any{
	"mcp:read": map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "read-only-probe", "version": "1"},
		},
	},
}

func mintKeyWithRole(t *testing.T, baseURL, callerKey, name, role string) string {
	t.Helper()
	out, code := captureRun(t, func() int {
		return cli.RunAuth([]string{"create-key", "--endpoint", baseURL, "--key", callerKey,
			"--name", name, "--role", role})
	})
	if code != 0 {
		t.Fatalf("rimsky auth create-key --role %s exited %d:\n%s", role, code, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if _, after, found := strings.Cut(line, `RIMSKY_API_KEY="`); found {
			if plaintext, _, ok := strings.Cut(after, `"`); ok && plaintext != "" {
				return plaintext
			}
		}
	}
	t.Fatalf("rimsky auth create-key --role %s printed no key plaintext:\n%s", role, out)
	return ""
}

func requireGrantIsTheSingleReadWildcard(t *testing.T, ep harness.RimskyEndpoint, adminKey string) {
	t.Helper()
	status, raw := ep.GetJSON(t, "/v1/auth/keys/"+readOnlyKeyName, adminKey)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/auth/keys/%s: %d %s", readOnlyKeyName, status, string(raw))
	}
	var resp struct {
		Permissions auth.Grant `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode the read-only key: %v: %s", err, string(raw))
	}
	want := auth.Grant{{Action: "*:read"}}
	if len(resp.Permissions) != 1 || resp.Permissions[0].Action != want[0].Action ||
		resp.Permissions[0].Mode != "" || len(resp.Permissions[0].Scope) != 0 {
		t.Fatalf("the %s role expanded to %+v, want exactly %+v", readOnlyRoleName, resp.Permissions, want)
	}
}

func createInstanceAs(t *testing.T, ep harness.RimskyEndpoint, key, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSONWithHeaders(t, "/v1/instances", map[string]any{
		"template": templateID, "instance_key": instanceKey,
	}, map[string]string{"Authorization": "Bearer " + key})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /v1/instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance create: %v: %s", err, string(raw))
	}
	return resp.InstanceID
}

func createTagAs(t *testing.T, ep harness.RimskyEndpoint, key, tag, templateID string) {
	t.Helper()
	status, raw := ep.PostJSONWithHeaders(t, "/v1/tags", map[string]any{
		"tag": tag, "template": templateID,
	}, map[string]string{"Authorization": "Bearer " + key})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /v1/tags: %d %s", status, string(raw))
	}
}
