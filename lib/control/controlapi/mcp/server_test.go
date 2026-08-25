// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi/mcp"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

type fakeCatalog struct {
	tools     []mcp.Tool
	calls     map[string]any
	called    string
	isErrorBy map[string]bool
}

func (f *fakeCatalog) Filtered(r *http.Request) []mcp.Tool { return f.tools }

func (f *fakeCatalog) Invoke(r *http.Request, name string, args json.RawMessage) (any, bool, *mcp.Error) {
	if f.calls == nil {
		f.calls = map[string]any{}
	}
	f.calls[name] = args
	f.called = name
	return map[string]any{"name": name}, f.isErrorBy[name], nil
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
	if m["protocolVersion"] != "2025-06-18" {
		t.Fatalf("expected default protocolVersion when the client omits one; got %+v", m["protocolVersion"])
	}
}

func TestMCPInitialize_EchoesSupportedRequestedVersion(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	if m["protocolVersion"] != "2025-06-18" {
		t.Fatalf("server must echo back a protocolVersion it supports; got %+v", m["protocolVersion"])
	}
}

func TestMCPInitialize_FallsBackForUnsupportedRequestedVersion(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	if m["protocolVersion"] != "2025-06-18" {
		t.Fatalf("server must fall back to a version it supports for an unsupported request; got %+v", m["protocolVersion"])
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

func TestMCPToolsCall_SetsTopLevelIsErrorOnFailure(t *testing.T) {
	catalog := &fakeCatalog{isErrorBy: map[string]bool{"x": true}}
	server := &mcp.Server{Tools: catalog}
	resp := serveRPC(t, server,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"x","arguments":{"k":1}}}`)
	if resp.Error != nil {
		t.Fatalf("a tool execution failure must still be a JSON-RPC success envelope carrying isError, not a protocol error: %v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result; got %T", resp.Result)
	}
	if m["isError"] != true {
		t.Fatalf("CallToolResult.isError must be true when the underlying tool call failed; got %+v", m)
	}
}

func TestMCPToolsCall_OmitsIsErrorOnSuccess(t *testing.T) {
	catalog := &fakeCatalog{}
	server := &mcp.Server{Tools: catalog}
	resp := serveRPC(t, server,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"x","arguments":{"k":1}}}`)
	if resp.Error != nil {
		t.Fatalf("tools/call: %v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result; got %T", resp.Result)
	}
	if m["isError"] != false {
		t.Fatalf("CallToolResult.isError must be false on a successful tool call; got %+v", m)
	}
}

func TestMCPSession_MissingHeaderOnNonInitializeIs400(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400 for a non-initialize request without a session; got %d", w.Code)
	}
	var resp mcp.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != mcp.CodeSessionRequired {
		t.Fatalf("expected CodeSessionRequired; got %+v", resp.Error)
	}
}

func TestMCPSession_UnknownSessionIs404(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	req.Header.Set("Mcp-Session-Id", "not-a-real-session")
	server.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected HTTP 404 for an unknown session; got %d", w.Code)
	}
	var resp mcp.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != mcp.CodeSessionNotFound {
		t.Fatalf("expected CodeSessionNotFound; got %+v", resp.Error)
	}
}

func TestMCPSession_DeleteTerminatesSession(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	sid := mustInitSession(t, server)

	delReq := httptest.NewRequest("DELETE", "/mcp", nil)
	delReq.Header.Set("Mcp-Session-Id", sid)
	delW := httptest.NewRecorder()
	server.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusOK {
		t.Fatalf("expected 200 from session termination; got %d", delW.Code)
	}

	postReq := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	postReq.Header.Set("Mcp-Session-Id", sid)
	postW := httptest.NewRecorder()
	server.ServeHTTP(postW, postReq)
	if postW.Code != http.StatusNotFound {
		t.Fatalf("expected the terminated session to be rejected with 404; got %d", postW.Code)
	}
}

func TestMCPStreamableHTTPHandshake(t *testing.T) {
	catalog := &fakeCatalog{tools: []mcp.Tool{
		{Name: "x", Description: "x desc", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	server := &mcp.Server{Tools: catalog}

	hs := httptest.NewServer(http.HandlerFunc(server.ServeHTTP))
	defer hs.Close()

	status, hdr, _ := postRPC(t, hs.URL, "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusOK {
		t.Fatalf("initialize status: got %d want 200", status)
	}
	sessionID := hdr.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatalf("initialize did not issue an Mcp-Session-Id header; headers=%v", hdr)
	}

	nStatus, _, nBody := postRPC(t, hs.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if nStatus != http.StatusAccepted {
		t.Fatalf("notifications/initialized status: got %d want 202; body=%q", nStatus, nBody)
	}
	if strings.TrimSpace(nBody) != "" {
		t.Fatalf("notifications/initialized must return an empty body; got %q", nBody)
	}
	if strings.Contains(nBody, "method not found") || strings.Contains(nBody, `"error"`) {
		t.Fatalf("notifications/initialized returned a JSON-RPC error reply (violation): %q", nBody)
	}

	lStatus, _, lBody := postRPC(t, hs.URL, sessionID,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if lStatus != http.StatusOK {
		t.Fatalf("tools/list status: got %d want 200; body=%q", lStatus, lBody)
	}
	var lResp mcp.Response
	if err := json.Unmarshal([]byte(lBody), &lResp); err != nil {
		t.Fatalf("tools/list decode: %v\n%s", err, lBody)
	}
	if lResp.Error != nil {
		t.Fatalf("tools/list error: %v", lResp.Error)
	}

	cStatus, _, cBody := postRPC(t, hs.URL, sessionID,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"x","arguments":{"k":1}}}`)
	if cStatus != http.StatusOK {
		t.Fatalf("tools/call status: got %d want 200; body=%q", cStatus, cBody)
	}
	var cResp mcp.Response
	if err := json.Unmarshal([]byte(cBody), &cResp); err != nil {
		t.Fatalf("tools/call decode: %v\n%s", err, cBody)
	}
	if cResp.Error != nil {
		t.Fatalf("tools/call error: %v", cResp.Error)
	}
	if catalog.called != "x" {
		t.Fatalf("tools/call did not invoke tool x; got %q", catalog.called)
	}

	getMCPStream(t, hs.URL, sessionID)
}

func postRPC(t *testing.T, baseURL, sessionID, body string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest("POST", baseURL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", body, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST body: %v", err)
	}
	return resp.StatusCode, resp.Header, string(raw)
}

func getMCPStream(t *testing.T, baseURL, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/mcp", nil)
	if err != nil {
		t.Fatalf("new GET request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	type result struct {
		status int
		ctype  string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		done <- result{status: resp.StatusCode, ctype: resp.Header.Get("Content-Type")}
		cancel()
	}()
	r := <-done
	if r.err != nil {
		t.Fatalf("GET /mcp: %v", r.err)
	}
	if r.status == http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp returned 405 — no streamable GET handler registered")
	}
	if r.status != http.StatusOK {
		t.Fatalf("GET /mcp status: got %d want 200", r.status)
	}
	if !strings.HasPrefix(r.ctype, "text/event-stream") {
		t.Fatalf("GET /mcp Content-Type: got %q want text/event-stream", r.ctype)
	}
}

func TestMCPMethodOutsideTheProtocolAnswersMethodNotFound(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":4,"method":"totally/unknown"}`)
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

func mustInitSession(t *testing.T, s *mcp.Server) string {
	t.Helper()
	resp, hdr := serveRPCWithHeaders(t, s, "", `{"jsonrpc":"2.0","id":0,"method":"initialize"}`)
	if resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	sid := hdr.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("initialize did not issue an Mcp-Session-Id header")
	}
	return sid
}

func serveRPC(t *testing.T, s *mcp.Server, body string) mcp.Response {
	t.Helper()
	sid := ""
	var probe struct {
		Method string `json:"method"`
	}
	if json.Unmarshal([]byte(body), &probe) == nil && probe.Method != "initialize" {
		sid = mustInitSession(t, s)
	}
	resp, _ := serveRPCWithHeaders(t, s, sid, body)
	return resp
}

func serveRPCWithHeaders(t *testing.T, s *mcp.Server, sessionID, body string) (mcp.Response, http.Header) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req = req.WithContext(context.Background())
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	s.ServeHTTP(w, req)
	var resp mcp.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	return resp, w.Header()
}

func TestCatalogListsEveryToolTheGrantReachesPlusTheToolsThatConsultNoGrant(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{
		tools: []string{"a_read", "a_write", "b_read", "c_probe"},
		entries: map[string]mcp.RegistryEntry{
			"a_read":  {Action: "a:read", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/a"}}},
			"a_write": {Action: "a:write", Routes: []mcp.RegistryRoute{{Method: "POST", Path: "/a"}}},
			"b_read":  {Action: "b:read", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/b"}}},
			"c_probe": {Action: "c:probe", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/c"}}, ExemptFromPermission: true},
		},
	}
	cat := &mcp.Catalog{
		Registry: reg,
		ResolveIdentity: func(ctx context.Context) (auth.Identity, bool) {
			return auth.Identity{Permissions: auth.Grant{{Action: "*:read"}}}, true
		},
	}
	got := cat.Filtered(httptest.NewRequest("GET", "/", nil))
	names := []string{}
	for _, tool := range got {
		names = append(names, tool.Name)
	}
	wantSet := map[string]bool{"a_read": true, "b_read": true, "c_probe": true}
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

// @decision: mcp-base-methods-scope
func TestMCPPingAnswersAnEmptyResultWithoutASession(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	resp, _ := serveRPCWithHeaders(t, server, "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if resp.Error != nil {
		t.Fatalf("ping is a liveness check every client sends; it must not error: %+v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("ping must answer an object result; got %T", resp.Result)
	}
	if len(m) != 0 {
		t.Fatalf("ping's result carries nothing; got %+v", m)
	}
}

// @decision: mcp-base-methods-scope
func TestMCPInitializeDeclaresOnlyTheCapabilitiesItServes(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	caps := resp.Result.(map[string]any)["capabilities"].(map[string]any)
	for _, served := range []string{"tools", "resources"} {
		if _, ok := caps[served]; !ok {
			t.Errorf("capabilities must name %q, which the server serves: %+v", served, caps)
		}
	}
	for _, unserved := range []string{"prompts", "logging", "completions", "sampling", "roots"} {
		if _, ok := caps[unserved]; ok {
			t.Errorf("capabilities must not name %q, which the server does not serve: %+v", unserved, caps)
		}
	}
	resources := caps["resources"].(map[string]any)
	if resources["subscribe"] != false {
		t.Errorf("resources/subscribe is unimplemented, so the capability must say subscribe: false; got %+v", resources)
	}
}

// @decision: mcp-base-methods-scope
func TestMCPUnimplementedBaseMethodsAnswerMethodNotFound(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	for _, method := range []string{
		"prompts/list", "prompts/get",
		"resources/subscribe", "resources/unsubscribe", "resources/templates/list",
		"roots/list", "sampling/createMessage", "logging/setLevel",
		"completion/complete",
	} {
		resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"`+method+`"}`)
		if resp.Error == nil {
			t.Errorf("the server must answer method-not-found for %s, which it does not serve; got result %+v", method, resp.Result)
			continue
		}
		if resp.Error.Code != mcp.CodeMethodNotFound {
			t.Errorf("%s answered code %d, want method-not-found (%d)", method, resp.Error.Code, mcp.CodeMethodNotFound)
		}
	}
}

// @decision: mcp-base-methods-scope
func TestMCPServesEveryMethodTheDecisionNames(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}, Resources: &fakeResources{}}
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"a_read","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"rimsky://instances/probe"}}`,
	} {
		resp := serveRPC(t, server, body)
		if resp.Error != nil && resp.Error.Code == mcp.CodeMethodNotFound {
			t.Errorf("the server serves %s and must not answer method-not-found: %+v", body, resp.Error)
		}
	}
}
