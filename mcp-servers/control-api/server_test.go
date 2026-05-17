// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapimcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServerInitializeAndToolsList smoke-tests the JSON-RPC plumbing.
func TestServerInitializeAndToolsList(t *testing.T) {
	t.Parallel()
	srv, err := NewServer(Config{ControlAPIURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()

	// initialize
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	resp, err := http.Post(httpSrv.URL+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST initialize: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["jsonrpc"] != "2.0" {
		t.Fatalf("jsonrpc: %v", got["jsonrpc"])
	}
	result, ok := got["result"].(map[string]any)
	if !ok {
		t.Fatalf("result: %v", got)
	}
	if pv, _ := result["protocolVersion"].(string); pv == "" {
		t.Fatalf("protocolVersion missing")
	}

	// tools/list
	body, _ = json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	resp2, err := http.Post(httpSrv.URL+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST tools/list: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var got2 map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	bs, _ := json.Marshal(got2)
	wants := []string{
		"template_list", "template_register", "instance_create", "instance_terminate",
		"node_invalidate", "held_frames_list", "parked_nodes_list",
	}
	body2 := string(bs)
	for _, name := range wants {
		if !strings.Contains(body2, `"`+name+`"`) {
			t.Errorf("tools/list missing %q", name)
		}
	}
}

// TestServerToolsCall_ForwardsToControlAPI verifies tools/call wraps the
// control-API in HTTP — by pointing ControlAPIURL at a captured stub.
func TestServerToolsCall_ForwardsToControlAPI(t *testing.T) {
	t.Parallel()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the path so the test can verify routing.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path})
	}))
	defer stub.Close()
	srv, err := NewServer(Config{ControlAPIURL: stub.URL})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()

	call := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "template_list",
			"arguments": map[string]any{},
		},
	}
	body, _ := json.Marshal(call)
	resp, err := http.Post(httpSrv.URL+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST tools/call: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["error"] != nil {
		t.Fatalf("rpc error: %v", got["error"])
	}
	bs, _ := json.Marshal(got["result"])
	if !strings.Contains(string(bs), "/templates") {
		t.Fatalf("expected wrapped path /templates: %s", bs)
	}
}
