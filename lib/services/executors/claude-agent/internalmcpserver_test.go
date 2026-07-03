// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type mcpTestClient struct {
	t          *testing.T
	url        string
	sessionID  string
	nextID     int
	serverName string
}

var mcpTestHTTPClient = &http.Client{
	Transport: &http.Transport{DisableKeepAlives: true},
}

func startTestServer(t *testing.T) (*CallbackServerHandle, *mcpTestClient) {
	t.Helper()
	handle, err := StartInternalMcpServer(InternalMcpOpts{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := &mcpTestClient{t: t, url: handle.URL}
	client.initialize()
	if client.serverName != CallbackMCPServerName {
		t.Fatalf("serverInfo.name = %q", client.serverName)
	}
	return handle, client
}

func (c *mcpTestClient) post(body string, sessionID string) (*http.Response, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader([]byte(body)))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := mcpTestHTTPClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp, raw
}

func (c *mcpTestClient) initialize() {
	c.t.Helper()
	c.nextID++
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`, c.nextID)
	resp, raw := c.post(body, "")
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("initialize status = %d body %s", resp.StatusCode, raw)
	}
	c.sessionID = resp.Header.Get("Mcp-Session-Id")
	if c.sessionID == "" {
		c.t.Fatal("initialize did not assign a session id")
	}
	var parsed struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		c.t.Fatal(err)
	}
	c.serverName = parsed.Result.ServerInfo.Name
}

type toolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

func (c *mcpTestClient) callTool(name string, argsJSON string) (toolCallResult, *jsonRPCError) {
	c.t.Helper()
	c.nextID++
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, c.nextID, name, argsJSON)
	resp, raw := c.post(body, c.sessionID)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("tools/call status = %d body %s", resp.StatusCode, raw)
	}
	var parsed struct {
		Result toolCallResult `json:"result"`
		Error  *jsonRPCError  `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		c.t.Fatal(err)
	}
	return parsed.Result, parsed.Error
}

func (c *mcpTestClient) firstText(result toolCallResult) string {
	c.t.Helper()
	if len(result.Content) == 0 {
		c.t.Fatalf("no content blocks in %+v", result)
	}
	return result.Content[0].Text
}

func TestMcpServerListsTheSixCallbackTools(t *testing.T) {
	_, client := startTestServer(t)
	client.nextID++
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, client.nextID)
	_, raw := client.post(body, client.sessionID)
	var parsed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range parsed.Result.Tools {
		names = append(names, tool.Name)
	}
	want := []string{"report_complete", "report_blocked", "report_error", "report_park", "dispatch_context_read", "attributes_read"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestMcpServerDispatchesReportCompleteWithDelta(t *testing.T) {
	handle, client := startTestServer(t)
	var gotDelta map[string]any
	var gotChanged bool
	var gotSummary *string
	var gotSignoffs []string
	entry := makeEntry("run-1")
	entry.OnComplete = func(delta map[string]any, changed bool, summary *string, signoffs []string, td ScheduleTeardown) (CompleteResult, error) {
		gotDelta, gotChanged, gotSummary, gotSignoffs = delta, changed, summary, signoffs
		return CompleteResult{Accepted: true}, nil
	}
	handle.Registry.Register("tok-1", entry)

	result, rpcErr := client.callTool("report_complete", `{"token":"tok-1","attributes_delta":{"answer":42},"changed":true,"change_summary":"did it","signoffs":["c2ln"]}`)
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if client.firstText(result) != `{"status":"accepted"}` {
		t.Fatalf("outcome = %q", client.firstText(result))
	}
	if !gotChanged || gotSummary == nil || *gotSummary != "did it" {
		t.Fatalf("changed/summary wrong: %v %v", gotChanged, gotSummary)
	}
	if gotDelta["answer"] != float64(42) {
		t.Fatalf("delta = %v", gotDelta)
	}
	if len(gotSignoffs) != 1 || gotSignoffs[0] != "c2ln" {
		t.Fatalf("signoffs = %v", gotSignoffs)
	}
}

func TestMcpServerReportCompleteWithoutDeltaAndRejectedOutcome(t *testing.T) {
	handle, client := startTestServer(t)
	entry := makeEntry("run-1")
	entry.OnComplete = func(delta map[string]any, changed bool, summary *string, signoffs []string, td ScheduleTeardown) (CompleteResult, error) {
		if delta != nil {
			t.Fatalf("expected nil delta, got %v", delta)
		}
		return CompleteResult{Accepted: false, Errors: map[string][]string{"attributes": {"invalid"}}}, nil
	}
	handle.Registry.Register("tok-1", entry)

	result, rpcErr := client.callTool("report_complete", `{"token":"tok-1","changed":false}`)
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	want := `{"errors":{"attributes":["invalid"]},"status":"rejected"}`
	if client.firstText(result) != want {
		t.Fatalf("outcome = %q, want %q", client.firstText(result), want)
	}
}

func TestMcpServerAttributesReadReturnsSnapshot(t *testing.T) {
	handle, client := startTestServer(t)
	entry := makeEntry("run-1")
	entry.AttributesAtSpawn = map[string]any{"category": "items", "count": float64(3)}
	handle.Registry.Register("tok-1", entry)

	result, rpcErr := client.callTool("attributes_read", `{"token":"tok-1"}`)
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(client.firstText(result)), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["category"] != "items" || snapshot["count"] != float64(3) {
		t.Fatalf("snapshot = %v", snapshot)
	}
}

func TestMcpServerDispatchContextReadMapsWireEnums(t *testing.T) {
	cases := map[string]string{
		"PRIOR_RETRY_AFTER_ERROR": "retry_after_error",
		"PRIOR_STALE_RECOVERY":    "stale_recovery",
		"PRIOR_RECALCULATE":       "recalculate",
	}
	for wire, want := range cases {
		handle, client := startTestServer(t)
		entry := makeEntry("run-1")
		entry.DispatchContext = NewDispatchContextSnapshot("d-9", "rs-9", "prior-1", wire, nil)
		handle.Registry.Register("tok-1", entry)

		result, rpcErr := client.callTool("dispatch_context_read", `{"token":"tok-1"}`)
		if rpcErr != nil {
			t.Fatalf("rpc error: %+v", rpcErr)
		}
		var ctx map[string]any
		if err := json.Unmarshal([]byte(client.firstText(result)), &ctx); err != nil {
			t.Fatal(err)
		}
		if ctx["dispatch_id"] != "d-9" || ctx["run_scope_id"] != "rs-9" {
			t.Fatalf("identity wrong: %v", ctx)
		}
		if ctx["prior_dispatch_id"] != "prior-1" || ctx["prior_dispatch_disposition"] != want {
			t.Fatalf("wire %s: context = %v", wire, ctx)
		}
	}
}

func TestMcpServerDispatchContextReadFreshDispatch(t *testing.T) {
	handle, client := startTestServer(t)
	entry := makeEntry("run-1")
	handle.Registry.Register("tok-1", entry)

	result, _ := client.callTool("dispatch_context_read", `{"token":"tok-1"}`)
	var ctx map[string]any
	if err := json.Unmarshal([]byte(client.firstText(result)), &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx["prior_dispatch_id"] != nil || ctx["prior_dispatch_disposition"] != nil {
		t.Fatalf("expected null prior fields: %v", ctx)
	}
}

func TestMcpServerUnknownTokenIsError(t *testing.T) {
	_, client := startTestServer(t)
	result, rpcErr := client.callTool("attributes_read", `{"token":"nope"}`)
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if !result.IsError || client.firstText(result) != "unknown_token" {
		t.Fatalf("result = %+v", result)
	}
}

func TestMcpServerReportParkTypedReason(t *testing.T) {
	handle, client := startTestServer(t)
	entry := makeEntry("run-1")
	var gotReason string
	var gotNote *string
	entry.OnPark = func(reason string, note *string, resumeAt *string, td ScheduleTeardown) error {
		gotReason, gotNote = reason, note
		return nil
	}
	handle.Registry.Register("tok-1", entry)

	result, rpcErr := client.callTool("report_park", `{"token":"tok-1","reason":"snooze","reason_note":"resting"}`)
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if client.firstText(result) != `{"status":"accepted"}` {
		t.Fatalf("ack = %q", client.firstText(result))
	}
	if gotReason != "snooze" || gotNote == nil || *gotNote != "resting" {
		t.Fatalf("park args wrong: %q %v", gotReason, gotNote)
	}
}

func TestMcpServerReportParkRejectsUnknownReason(t *testing.T) {
	handle, client := startTestServer(t)
	handle.Registry.Register("tok-1", makeEntry("run-1"))
	for _, reason := range []string{"unspecified", "coffee-break", ""} {
		_, rpcErr := client.callTool("report_park", fmt.Sprintf(`{"token":"tok-1","reason":%q}`, reason))
		if rpcErr == nil || rpcErr.Code != -32602 {
			t.Fatalf("reason %q: expected -32602, got %+v", reason, rpcErr)
		}
	}
}

func TestMcpServerReportParkWithoutOnParkHandler(t *testing.T) {
	handle, client := startTestServer(t)
	handle.Registry.Register("tok-1", makeEntry("run-1"))
	result, rpcErr := client.callTool("report_park", `{"token":"tok-1","reason":"await_callback"}`)
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if !result.IsError || client.firstText(result) != "park_not_supported" {
		t.Fatalf("result = %+v", result)
	}
}

func TestMcpServerConcurrentSessions(t *testing.T) {
	handle, clientA := startTestServer(t)
	clientB := &mcpTestClient{t: t, url: handle.URL}
	clientB.initialize()
	if clientA.sessionID == clientB.sessionID {
		t.Fatal("expected distinct session ids")
	}
	handle.Registry.Register("tok-1", makeEntry("run-1"))
	for _, client := range []*mcpTestClient{clientA, clientB} {
		result, rpcErr := client.callTool("attributes_read", `{"token":"tok-1"}`)
		if rpcErr != nil || len(result.Content) == 0 {
			t.Fatalf("session call failed: %+v %+v", result, rpcErr)
		}
	}
}

func TestMcpServerFreshSessionAfterDelete(t *testing.T) {
	handle, client := startTestServer(t)
	req, err := http.NewRequest(http.MethodDelete, handle.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Mcp-Session-Id", client.sessionID)
	resp, err := mcpTestHTTPClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	_, raw := client.post(`{"jsonrpc":"2.0","id":99,"method":"tools/list"}`, client.sessionID)
	if !strings.Contains(string(raw), "Session not found") {
		t.Fatalf("expected session-not-found after delete, got %s", raw)
	}

	fresh := &mcpTestClient{t: t, url: handle.URL}
	fresh.initialize()
	if fresh.sessionID == "" {
		t.Fatal("expected fresh session to initialize")
	}
}

func TestMcpServerRejectsNonInitializeWithoutSession(t *testing.T) {
	_, client := startTestServer(t)
	resp, raw := client.post(`{"jsonrpc":"2.0","id":5,"method":"tools/list"}`, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d body %s", resp.StatusCode, raw)
	}
}

func TestMcpServerAcceptsNotifications(t *testing.T) {
	_, client := startTestServer(t)
	resp, _ := client.post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, client.sessionID)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification status = %d", resp.StatusCode)
	}
}
