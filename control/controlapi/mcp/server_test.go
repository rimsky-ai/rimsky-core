// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fallguy/rimsky/control/controlapi/mcp"
	"github.com/fallguy/rimsky/foundation/auth"
)

// fakeCatalog implements mcp.ToolCatalog for the smoke test.
type fakeCatalog struct {
	tools  []mcp.Tool
	calls  map[string]any
	called string
}

func (f *fakeCatalog) Filtered(r *http.Request) []mcp.Tool { return f.tools }

func (f *fakeCatalog) Invoke(r *http.Request, name string, args json.RawMessage) (any, *mcp.Error) {
	if f.calls == nil {
		f.calls = map[string]any{}
	}
	f.calls[name] = args
	f.called = name
	if v, ok := f.calls[name+"_result"]; ok {
		return v, nil
	}
	return map[string]any{"name": name}, nil
}

func TestMCPInitialize(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result; got %T", resp.Result)
	}
	caps, ok := m["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("missing capabilities: %+v", m)
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("missing tools capability: %+v", caps)
	}
}

func TestMCPToolsList(t *testing.T) {
	catalog := &fakeCatalog{tools: []mcp.Tool{
		{Name: "x", Description: "x desc", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	server := &mcp.Server{Tools: catalog}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if resp.Error != nil {
		t.Fatalf("tools/list: %v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	tools := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool; got %+v", tools)
	}
}

func TestMCPToolsCall(t *testing.T) {
	catalog := &fakeCatalog{}
	server := &mcp.Server{Tools: catalog}
	resp := serveRPC(t, server,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"x","arguments":{"k":1}}}`)
	if resp.Error != nil {
		t.Fatalf("tools/call: %v", resp.Error)
	}
	if catalog.called != "x" {
		t.Fatalf("invoke not called; got %q", catalog.called)
	}
}

func TestMCPUnsupportedMethod(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	// `prompts/list` is not implemented in v1; both tools/* and
	// resources/* are.
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":4,"method":"prompts/list"}`)
	if resp.Error == nil || resp.Error.Code != mcp.CodeMethodNotFound {
		t.Fatalf("expected method-not-found; got %+v", resp.Error)
	}
}

func TestMCPInvalidJSON(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader("not-json"))
	server.ServeHTTP(w, req)
	var resp mcp.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != mcp.CodeParseError {
		t.Fatalf("expected parse error; got %+v", resp.Error)
	}
}

func serveRPC(t *testing.T, s *mcp.Server, body string) mcp.Response {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req = req.WithContext(context.Background())
	s.ServeHTTP(w, req)
	var resp mcp.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	return resp
}

// TestCatalogFiltered exercises catalog.Filtered against a fake
// registry. Verifies the wildcard-based filter.
//
// Uses SetIdentityHook with a t.Cleanup-scoped restore so the global
// identity hook can't bleed into sibling tests (matters under -race
// when tests within the same package run concurrently via t.Parallel
// or via -count=N).
func TestCatalogFiltered(t *testing.T) {
	reg := &fakeRegistry{
		tools: []string{"a_read", "a_write", "b_read"},
		entries: map[string]mcp.RegistryEntry{
			"a_read":  {Action: "a:read", IsWrite: false, Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/a"}}},
			"a_write": {Action: "a:write", IsWrite: true, Routes: []mcp.RegistryRoute{{Method: "POST", Path: "/a"}}},
			"b_read":  {Action: "b:read", IsWrite: false, Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/b"}}},
		},
	}
	restore := mcp.SetIdentityHook(func(ctx context.Context) (auth.Identity, bool) {
		return auth.Identity{Permissions: auth.Grant{{Action: "*:read"}}}, true
	})
	t.Cleanup(restore)
	cat := &mcp.Catalog{Registry: reg}
	got := cat.Filtered(httptest.NewRequest("GET", "/", nil))
	names := []string{}
	for _, t := range got {
		names = append(names, t.Name)
	}
	wantSet := map[string]bool{"a_read": true, "b_read": true}
	if len(names) != len(wantSet) {
		t.Fatalf("filtered tools: got %v want %v", names, wantSet)
	}
	for _, n := range names {
		if !wantSet[n] {
			t.Errorf("unexpected tool %q", n)
		}
	}
}

type fakeRegistry struct {
	tools   []string
	entries map[string]mcp.RegistryEntry
}

func (f *fakeRegistry) AllTools() []string { return f.tools }
func (f *fakeRegistry) EntryForTool(name string) (mcp.RegistryEntry, bool) {
	e, ok := f.entries[name]
	return e, ok
}
