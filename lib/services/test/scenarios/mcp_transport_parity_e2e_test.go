// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

type mcpTransportToolCategory struct {
	name string

	readTool     string
	readArgs     map[string]any
	readHTTPVerb string
	readHTTPPath string

	mutationTool     string
	mutationArgs     map[string]any
	mutationHTTPVerb string
	mutationHTTPPath string
}

func TestMcpTransportParity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	adminKey := mintAPIKey(t, ep, "", "mcp-parity-admin", []map[string]any{
		{"action": "*"},
	})
	mcpOnlyKey := mintAPIKey(t, ep, adminKey, "mcp-parity-mcponly", []map[string]any{
		{"action": "mcp:read"},
	})

	templateID := deployScenarioTemplateAuth(t, ep, adminKey, map[string]any{
		"spec": map[string]any{
			"name":    "mcp-transport-parity",
			"version": "1",
			"messages": []map[string]any{
				{
					"type": "parity/probe",
					"body_schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"reason": map[string]any{"type": "string"},
						},
					},
				},
			},
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})

	instanceKey := "mcp-parity-seed"
	instanceID := createInstanceAuth(t, ep, adminKey, templateID, instanceKey, map[string]any{})

	_ = waitForFirstNodeID(t, ep, adminKey, instanceID)

	mcpURL := ep.BaseURL + "/v1/mcp"

	sessionID := mcpInitialize(t, mcpURL, adminKey)
	mcpNotifyInitialized(t, mcpURL, adminKey, sessionID)

	adminTools := mcpToolsList(t, mcpURL, adminKey, sessionID)
	assertToolCatalogCoverage(t, adminTools)

	mcpOnlySessionID := mcpInitialize(t, mcpURL, mcpOnlyKey)
	mcpNotifyInitialized(t, mcpURL, mcpOnlyKey, mcpOnlySessionID)
	mcpOnlyTools := mcpToolsList(t, mcpURL, mcpOnlyKey, mcpOnlySessionID)
	postureExempt := map[string]bool{"health_probe": true, "auth_whoami": true, "service_auth_ca_root": true}
	seen := map[string]bool{}
	for _, tool := range mcpOnlyTools {
		if !postureExempt[tool.Name] {
			t.Fatalf("mcp-only key (grant {mcp:read}) saw the permissioned tool %q; a tool whose action consults a grant must be filtered out\ntools: %v",
				tool.Name, mcpOnlyTools)
		}
		seen[tool.Name] = true
	}
	for name := range postureExempt {
		if !seen[name] {
			t.Fatalf("mcp-only key did not see %q; a tool whose action consults no permission lists for every caller\ntools: %v",
				name, mcpOnlyTools)
		}
	}

	categories := []mcpTransportToolCategory{
		{
			name:         "template",
			readTool:     "template_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/templates",
			mutationTool: "template_register",
			mutationArgs: map[string]any{
				"spec": map[string]any{
					"name":    "mcp-transport-parity-mut",
					"version": "1",
					"nodes": []map[string]any{
						{"type": "worker", "executor": "stub"},
					},
				},
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/templates",
		},
		{
			name:         "tag",
			readTool:     "tag_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/tags",
			mutationTool: "tag_create",
			mutationArgs: map[string]any{
				"tag":      "mcp-parity-tag",
				"template": templateID,
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/tags",
		},
		{
			name:         "instance",
			readTool:     "instance_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances",
			mutationTool: "instance_create",
			mutationArgs: map[string]any{
				"template":     templateID,
				"instance_key": "mcp-parity-mut-inst",
				"params":       map[string]any{},
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/instances",
		},
		{
			name:     "node",
			readTool: "node_list",
			readArgs: map[string]any{
				"idOrKey": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/nodes",
		},
		{
			name:         "instance_lifecycle",
			readTool:     "instance_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances",
			mutationTool: "instance_pause",
			mutationArgs: map[string]any{
				"idOrKey": instanceID,
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/instances/" + instanceID + "/pause",
		},
		{
			name:     "message",
			readTool: "message_list",
			readArgs: map[string]any{
				"idOrKey": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/messages",
			mutationTool: "message_send",
			// @decision: idempotency-key-header-universal
			mutationArgs: map[string]any{
				"idOrKey":         instanceID,
				"type":            "parity/probe",
				"idempotency_key": "mcp-parity-probe-" + uuid.NewString(),
				"payload": map[string]any{
					"reason": "mcp parity probe",
				},
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/instances/" + instanceID + "/messages",
		},
		{
			name:         "event",
			readTool:     "event_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/events",
		},
		{
			name:         "audit",
			readTool:     "audit_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/audit",
		},
		{
			name:     "breakpoint",
			readTool: "breakpoint_list",
			readArgs: map[string]any{
				"idOrKey": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/breakpoints",
			mutationTool: "breakpoint_create",
			mutationArgs: map[string]any{
				"idOrKey":     instanceID,
				"checkpoint":  "after_terminal",
				"signal_type": "terminal/success",
				"mode":        "notify_only",
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/instances/" + instanceID + "/breakpoints",
		},
		{
			name:     "asset",
			readTool: "asset_list",
			readArgs: map[string]any{
				"idOrKey": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/assets",
		},
		{
			name:     "lineage",
			readTool: "lineage_get",
			readArgs: map[string]any{
				"executor_name": "stub",
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/lineage/by-producer/stub",
		},
		{
			name:         "diagnostics",
			readTool:     "parked_node_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/admin/diagnostics/parked-nodes",
		},
		{
			name:         "auth",
			readTool:     "auth_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/auth/keys",
			mutationTool: "auth_create_key",
			mutationArgs: map[string]any{
				"name":        "mcp-parity-mut-key",
				"permissions": []map[string]any{{"action": "instance:read"}},
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/auth/keys",
		},
	}

	for _, cat := range categories {
		cat := cat
		t.Run("auth_parity/"+cat.name, func(t *testing.T) {
			t.Parallel()
			assertAuthParity(t, ep, mcpURL, mcpOnlyKey, mcpOnlySessionID, cat.readTool, cat.readArgs, cat.readHTTPVerb, cat.readHTTPPath)
			if cat.mutationTool != "" {
				assertAuthParity(t, ep, mcpURL, mcpOnlyKey, mcpOnlySessionID, cat.mutationTool, cat.mutationArgs, cat.mutationHTTPVerb, cat.mutationHTTPPath)
			}
		})
	}

	for _, cat := range categories {
		assertObservableStateReadParity(t, ep, mcpURL, adminKey, sessionID, cat)
		if cat.mutationTool != "" {
			assertObservableStateMutationParity(t, ep, mcpURL, adminKey, sessionID, cat)
		}
	}
}

func assertToolCatalogCoverage(t *testing.T, tools []toolEntry) {
	t.Helper()
	have := map[string]bool{}
	for _, tool := range tools {
		have[tool.Name] = true
	}
	must := []string{
		"template_list",
		"tag_list",
		"instance_list",
		"node_list",
		"message_list",
		"event_list",
		"audit_list",
		"breakpoint_list",
		"asset_list",
		"lineage_get",
		"parked_node_list",
		"auth_list",
	}
	for _, name := range must {
		if !have[name] {
			t.Fatalf("admin tools/list missing %q — the category's surface is unreachable via MCP\nhave: %v", name, toolNames(tools))
		}
	}
}

func assertAuthParity(t *testing.T, ep harness.RimskyEndpoint, mcpURL, bearer, sessionID, toolName string, args map[string]any, httpVerb, httpPath string) {
	t.Helper()
	httpStatus := requestStatusAuth(t, ep, httpVerb, httpPath, bearer, nil)
	if httpStatus != http.StatusForbidden {
		t.Fatalf("HTTP %s %s with bearer holding only mcp:read returned %d, want 403 — the HTTP-route gate must deny", httpVerb, httpPath, httpStatus)
	}

	envelope := mcpToolsCall(t, mcpURL, bearer, sessionID, toolName, args)
	status, isErr := unwrapMcpErrorEnvelope(t, envelope, toolName)
	if !isErr {
		t.Fatalf("MCP tools/call %q with bearer holding only mcp:read returned a success envelope — MCP-side gate is weaker than HTTP route's gate\nenvelope: %v", toolName, envelope)
	}
	if status != http.StatusForbidden {
		t.Fatalf("MCP tools/call %q with bearer holding only mcp:read returned status %d, want 403 — MCP must mirror the HTTP-route 403\nenvelope: %v", toolName, status, envelope)
	}
}

func assertObservableStateReadParity(t *testing.T, ep harness.RimskyEndpoint, mcpURL, bearer, sessionID string, cat mcpTransportToolCategory) {
	t.Helper()

	status, raw := getJSONAuth(t, ep, cat.readHTTPPath, bearer)
	if status != http.StatusOK {
		t.Fatalf("%s read parity: HTTP %s returned %d, want 200\nbody: %s", cat.name, cat.readHTTPPath, status, string(raw))
	}
	var httpBody map[string]json.RawMessage
	if err := json.Unmarshal(raw, &httpBody); err != nil {
		t.Fatalf("%s read parity: decode HTTP %s body: %v\nraw: %s", cat.name, cat.readHTTPPath, err, string(raw))
	}

	envelope := mcpToolsCall(t, mcpURL, bearer, sessionID, cat.readTool, cat.readArgs)
	mcpBody := unwrapMcpSuccessEnvelope(t, envelope, cat.readTool)
	var mcpMap map[string]json.RawMessage
	switch v := mcpBody.(type) {
	case map[string]any:
		mcpMap = map[string]json.RawMessage{}
		for k, val := range v {
			b, err := json.Marshal(val)
			if err != nil {
				t.Fatalf("%s read parity: re-marshal MCP body key %q: %v", cat.name, k, err)
			}
			mcpMap[k] = b
		}
	default:
		t.Fatalf("%s read parity: MCP %q response is not a JSON object (canned response shape?); got %T\nenvelope: %v", cat.name, cat.readTool, mcpBody, envelope)
	}

	for k, httpVal := range httpBody {
		mcpVal, ok := mcpMap[k]
		if !ok {
			t.Fatalf("%s read parity: MCP %q response missing top-level key %q present on HTTP %s response — MCP transport returned a canned shape\nhttp keys: %v\nmcp keys: %v",
				cat.name, cat.readTool, k, cat.readHTTPPath, jsonMapKeys(httpBody), jsonMapKeys(mcpMap))
		}
		if !jsonValueIsEmpty(t, httpVal) && jsonValueIsEmpty(t, mcpVal) {
			t.Fatalf("%s read parity: MCP %q top-level key %q is empty/null (%s) while HTTP %s carries real content (%s) — MCP transport returned a canned/empty payload",
				cat.name, cat.readTool, k, string(mcpVal), cat.readHTTPPath, string(httpVal))
		}
	}
}

func jsonValueIsEmpty(t *testing.T, raw json.RawMessage) bool {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode JSON value for emptiness check: %v: %s", err, string(raw))
	}
	switch tv := v.(type) {
	case nil:
		return true
	case string:
		return tv == ""
	case []any:
		return len(tv) == 0
	case map[string]any:
		return len(tv) == 0
	default:
		return false
	}
}

func assertObservableStateMutationParity(t *testing.T, ep harness.RimskyEndpoint, mcpURL, bearer, sessionID string, cat mcpTransportToolCategory) {
	t.Helper()

	envelope := mcpToolsCall(t, mcpURL, bearer, sessionID, cat.mutationTool, cat.mutationArgs)
	if status, isErr := unwrapMcpErrorEnvelopeOptional(envelope); isErr {
		t.Fatalf("%s mutation parity: MCP %q returned an error envelope (status=%d); the mutation never landed so observable-state parity is moot\nenvelope: %v",
			cat.name, cat.mutationTool, status, envelope)
	}
	body := unwrapMcpSuccessEnvelope(t, envelope, cat.mutationTool)

	switch cat.name {
	case "template":
		m, ok := body.(map[string]any)
		if !ok {
			t.Fatalf("template mutation parity: response not a JSON object: %T\nbody: %v", body, body)
		}
		newID, _ := m["template_id"].(string)
		if newID == "" {
			t.Fatalf("template mutation parity: template_register response missing template_id\nbody: %v", m)
		}
		status, raw := getJSONAuth(t, ep, "/v1/templates/"+newID, bearer)
		if status != http.StatusOK {
			t.Fatalf("template mutation parity: GET /v1/templates/%s returned %d, want 200 (the template MCP claimed to create must be readable via HTTP)\nbody: %s", newID, status, string(raw))
		}

	case "tag":
		if !tagListedAuth(t, ep, bearer, "mcp-parity-tag") {
			t.Fatalf("tag mutation parity: tag %q is not on /v1/tags after MCP tag_create — MCP write returned canned success without invoking the handler", "mcp-parity-tag")
		}

	case "instance":
		key := "mcp-parity-mut-inst"
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+key, bearer)
		if status != http.StatusOK {
			t.Fatalf("instance mutation parity: GET /v1/instances/%s returned %d, want 200 (the instance MCP claimed to create must be readable via HTTP)\nbody: %s", key, status, string(raw))
		}

	case "instance_lifecycle":
		instID := cat.mutationArgs["idOrKey"].(string)
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+instID, bearer)
		if status != http.StatusOK {
			t.Fatalf("instance_lifecycle mutation parity: GET /v1/instances/%s returned %d, want 200\nbody: %s", instID, status, string(raw))
		}
		var inst struct {
			Paused bool `json:"paused"`
		}
		if err := json.Unmarshal(raw, &inst); err != nil {
			t.Fatalf("instance_lifecycle mutation parity: decode instance: %v\nbody: %s", err, string(raw))
		}
		if !inst.Paused {
			t.Fatalf("instance_lifecycle mutation parity: GET /v1/instances/%s shows paused=false after MCP instance_pause — MCP write returned canned success without invoking the handler\nbody: %s", instID, string(raw))
		}
		resumeStatus, resumeRaw := ep.PostJSONWithHeaders(t,
			"/v1/instances/"+instID+"/resume",
			map[string]any{},
			map[string]string{
				"Authorization":   "Bearer " + bearer,
				"Idempotency-Key": "mcp-parity-resume-" + uuid.NewString(),
			})
		if resumeStatus != http.StatusOK {
			t.Fatalf("instance_lifecycle mutation parity: HTTP resume after MCP pause returned %d: %s", resumeStatus, string(resumeRaw))
		}

	case "message":
		instID := cat.mutationArgs["idOrKey"].(string)
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+instID+"/messages", bearer)
		if status != http.StatusOK {
			t.Fatalf("message mutation parity: GET /v1/instances/%s/messages returned %d, want 200\nbody: %s", instID, status, string(raw))
		}
		if !strings.Contains(string(raw), `"type":"parity/probe"`) {
			t.Fatalf("message mutation parity: GET /v1/instances/%s/messages does not include a parity/probe message after MCP message_send\nbody: %s", instID, string(raw))
		}

	case "breakpoint":
		idOrKey := cat.mutationArgs["idOrKey"].(string)
		assertBreakpointAppears(t, ep, bearer, idOrKey, "after_terminal")

	case "auth":
		status, raw := getJSONAuth(t, ep, "/v1/auth/keys", bearer)
		if status != http.StatusOK {
			t.Fatalf("auth mutation parity: GET /v1/auth/keys returned %d, want 200\nbody: %s", status, string(raw))
		}
		if !strings.Contains(string(raw), `"mcp-parity-mut-key"`) {
			t.Fatalf("auth mutation parity: GET /v1/auth/keys does not include the key MCP claimed to mint\nbody: %s", string(raw))
		}

	default:
		t.Fatalf("mutation parity case missing for category %q — add it or set mutationTool to empty", cat.name)
	}

	if body == nil {
		t.Fatalf("%s mutation parity: MCP %q returned a nil body (canned blank envelope?)", cat.name, cat.mutationTool)
	}
}

type toolEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func mcpInitialize(t *testing.T, mcpURL, bearer string) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	resp := postMCPJSONRPC(t, mcpURL, bearer, "", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("mcp initialize: got %d, want 200\nbody: %s", resp.StatusCode, string(raw))
	}
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("mcp initialize: missing Mcp-Session-Id header")
	}
	return sid
}

func mcpNotifyInitialized(t *testing.T, mcpURL, bearer, sessionID string) {
	t.Helper()
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	resp := postMCPJSONRPC(t, mcpURL, bearer, sessionID, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mcp notifications/initialized: got %d, want 202\nbody: %s", resp.StatusCode, string(raw))
	}
	if strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("mcp notifications/initialized: must return empty body; got %q", string(raw))
	}
}

func mcpToolsList(t *testing.T, mcpURL, bearer, sessionID string) []toolEntry {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	resp := postMCPJSONRPC(t, mcpURL, bearer, sessionID, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("mcp tools/list: got %d, want 200\nbody: %s", resp.StatusCode, string(raw))
	}
	var env struct {
		Result struct {
			Tools []toolEntry `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("mcp tools/list decode: %v", err)
	}
	if env.Error != nil {
		t.Fatalf("mcp tools/list returned error: %+v", env.Error)
	}
	return env.Result.Tools
}

type mcpToolsCallEnvelope struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func mcpToolsCall(t *testing.T, mcpURL, bearer, sessionID, name string, args map[string]any) mcpToolsCallEnvelope {
	t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	pb, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("mcp tools/call %q: marshal params: %v", name, err)
	}
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":%s}`, string(pb))
	resp := postMCPJSONRPC(t, mcpURL, bearer, sessionID, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("mcp tools/call %q: got %d, want 200\nbody: %s", name, resp.StatusCode, string(raw))
	}
	var env struct {
		Result mcpToolsCallEnvelope `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("mcp tools/call %q: decode: %v", name, err)
	}
	if env.Error != nil {
		t.Fatalf("mcp tools/call %q returned JSON-RPC error: %+v", name, env.Error)
	}
	return env.Result
}

func postMCPJSONRPC(t *testing.T, mcpURL, bearer, sessionID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, mcpURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build MCP POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MCP POST: %v", err)
	}
	return resp
}

func unwrapMcpSuccessEnvelope(t *testing.T, env mcpToolsCallEnvelope, toolName string) any {
	t.Helper()
	if len(env.Content) == 0 {
		t.Fatalf("MCP tools/call %q: envelope has no content entries", toolName)
	}
	c := env.Content[0]
	if c.Type != "text" {
		t.Fatalf("MCP tools/call %q: content[0].type=%q, want text", toolName, c.Type)
	}
	var body any
	if c.Text == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(c.Text), &body); err != nil {
		t.Fatalf("MCP tools/call %q: decode inner body: %v\nraw: %s", toolName, err, c.Text)
	}
	return body
}

func unwrapMcpErrorEnvelope(t *testing.T, env mcpToolsCallEnvelope, toolName string) (int, bool) {
	t.Helper()
	body := unwrapMcpSuccessEnvelope(t, env, toolName)
	m, ok := body.(map[string]any)
	if !ok {
		return 0, false
	}
	isErr, _ := m["isError"].(bool)
	statusF, _ := m["status"].(float64)
	return int(statusF), isErr
}

func unwrapMcpErrorEnvelopeOptional(env mcpToolsCallEnvelope) (int, bool) {
	if len(env.Content) == 0 {
		return 0, false
	}
	c := env.Content[0]
	if c.Type != "text" || c.Text == "" {
		return 0, false
	}
	var body any
	if err := json.Unmarshal([]byte(c.Text), &body); err != nil {
		return 0, false
	}
	m, ok := body.(map[string]any)
	if !ok {
		return 0, false
	}
	isErr, _ := m["isError"].(bool)
	statusF, _ := m["status"].(float64)
	return int(statusF), isErr
}

func requestStatusAuth(t *testing.T, ep harness.RimskyEndpoint, verb, path, bearer string, body any) int {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("requestStatusAuth: marshal body for %s %s: %v", verb, path, err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(verb, ep.BaseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("requestStatusAuth: build %s %s: %v", verb, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if verb == http.MethodPost || verb == http.MethodPut {
		req.Header.Set("Idempotency-Key", "mcp-parity-"+uuid.NewString())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requestStatusAuth: %s %s: %v", verb, path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func getJSONAuth(t *testing.T, ep harness.RimskyEndpoint, path, bearer string) (int, []byte) {
	t.Helper()
	return ep.GetJSON(t, path, bearer)
}

func createInstanceAuth(t *testing.T, ep harness.RimskyEndpoint, bearer, templateID, instanceKey string, params map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSONWithHeaders(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       params,
	}, map[string]string{"Authorization": "Bearer " + bearer})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", status, string(raw))
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
	return resp.InstanceID
}

func waitForFirstNodeID(t *testing.T, ep harness.RimskyEndpoint, bearer, instanceID string) string {
	t.Helper()
	var nodeID string
	awaited.Until(t, fmt.Sprintf("the first node to appear on instance %s", instanceID), func() bool {
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+instanceID+"/nodes", bearer)
		if status != http.StatusOK {
			return false
		}
		var resp struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Nodes) == 0 {
			return false
		}
		nodeID = resp.Nodes[0].ID
		return true
	})
	return nodeID
}

func assertBreakpointAppears(t *testing.T, ep harness.RimskyEndpoint, bearer, idOrKey, checkpoint string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("the %s breakpoint created over MCP to appear in the REST breakpoint list for %s",
		checkpoint, idOrKey), func() bool {
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+idOrKey+"/breakpoints", bearer)
		return status == http.StatusOK && strings.Contains(string(raw), `"`+checkpoint+`"`)
	})
}

func toolNames(tools []toolEntry) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

func jsonMapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
