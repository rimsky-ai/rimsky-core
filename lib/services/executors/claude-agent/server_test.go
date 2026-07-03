// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type capturedCallback struct {
	url  string
	body map[string]any
}

type callbackRecorder struct {
	mu    sync.Mutex
	calls []capturedCallback
}

func (r *callbackRecorder) post(url string, body map[string]any, _ *slog.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, capturedCallback{url: url, body: body})
}

func (r *callbackRecorder) waitForCall(t *testing.T) capturedCallback {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.calls) > 0 {
			call := r.calls[0]
			r.mu.Unlock()
			return call
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("callback was not posted within 10s")
	return capturedCallback{}
}

func startTestExecutor(t *testing.T) (*ExecutorServer, *ObservabilityServer, *callbackRecorder) {
	t.Helper()
	recorder := &callbackRecorder{}
	obs := NewObservabilityServer("http://bridge.invalid")
	executor := NewExecutorServer(ServerConfig{
		Opts:          Opts{StubMode: true},
		CliRunner:     &fakeRunner{},
		Observability: obs,
		PostCallback:  recorder.post,
	})
	return executor, obs, recorder
}

func dialTestGrpc(t *testing.T, executor *ExecutorServer, obs *ObservabilityServer) (genv1.ExecutorClient, genv1.ExecutorObservabilityClient) {
	t.Helper()
	running, err := StartGrpcServer("127.0.0.1", 0, executor, obs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Shutdown)
	conn, err := grpc.NewClient(running.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return genv1.NewExecutorClient(conn), genv1.NewExecutorObservabilityClient(conn)
}

func TestGrpcExecuteAsyncAckAndCallback(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	executor, obs, recorder := startTestExecutor(t)
	client, _ := dialTestGrpc(t, executor, obs)

	attributes, err := structpb.NewStruct(map[string]any{"user_prompt": "go"})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := client.Execute(context.Background(), &genv1.ExecuteRequest{
		NodeId:      "node-a",
		InstanceId:  "instance-1",
		NodeType:    "claude-agent",
		DispatchId:  "disp-42",
		Attributes:  attributes,
		CallbackUrl: "http://supervisor.invalid/cb/",
		CancelToken: "ct-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitAsync := outcome.GetAwaitAsync()
	if awaitAsync == nil || awaitAsync.GetAsyncAckId() == "" {
		t.Fatalf("expected await_async outcome, got %+v", outcome)
	}

	call := recorder.waitForCall(t)
	wantURL := "http://supervisor.invalid/cb/v1/callback/" + awaitAsync.GetAsyncAckId()
	if call.url != wantURL {
		t.Fatalf("callback url = %q, want %q", call.url, wantURL)
	}
	success, ok := call.body["success"].(map[string]any)
	if !ok {
		t.Fatalf("expected success callback body, got %v", call.body)
	}
	delta, _ := success["attributes_delta"].(map[string]any)
	if delta["stub"] != true || delta["session_token"] != "disp-42" {
		t.Fatalf("delta = %v", delta)
	}
	if _, hasAck := call.body["async_ack_id"]; hasAck {
		t.Fatal("gRPC-path callback body must not carry async_ack_id (it rides in the URL)")
	}
}

func TestGrpcObservabilityCapabilitiesAndTrace(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	t.Setenv("RIMSKY_EXECUTOR_DECLARED_TAGS", "alpha,beta")
	executor, obs, recorder := startTestExecutor(t)
	client, obsClient := dialTestGrpc(t, executor, obs)

	caps, err := obsClient.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !caps.GetSupportsTraceGet() || !caps.GetSupportsTraceStream() {
		t.Fatalf("caps = %+v", caps)
	}
	if string(caps.GetExpectedAttributesSchema()) != string(SchemaBytes()) {
		t.Fatal("capabilities schema does not match SchemaBytes()")
	}
	if len(caps.GetDeclaredErrorClasses()) != 13 {
		t.Fatalf("declared error classes = %d, want 13", len(caps.GetDeclaredErrorClasses()))
	}
	if len(caps.GetDeclaredTags()) != 2 {
		t.Fatalf("declared tags = %v", caps.GetDeclaredTags())
	}
	if caps.GetHttpBridgeUrl() != "http://bridge.invalid" {
		t.Fatalf("bridge url = %q", caps.GetHttpBridgeUrl())
	}

	attributes, _ := structpb.NewStruct(map[string]any{"user_prompt": "go"})
	_, err = client.Execute(context.Background(), &genv1.ExecuteRequest{
		NodeId:      "node-a",
		NodeType:    "claude-agent",
		DispatchId:  "disp-trace",
		Attributes:  attributes,
		CallbackUrl: "http://supervisor.invalid/cb",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.waitForCall(t)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		trace, err := obsClient.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: "disp-trace"})
		if err != nil {
			t.Fatal(err)
		}
		if trace.GetComplete() {
			categories := map[string]bool{}
			for _, ev := range trace.GetEvents() {
				categories[ev.GetCategory()] = true
			}
			if !categories["step_started"] || !categories["step_completed"] {
				t.Fatalf("trace categories = %v", categories)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("trace did not complete")
}

func TestGrpcExecuteParseErrorPostsErrorCallback(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "")
	executor, obs, recorder := startTestExecutor(t)
	client, _ := dialTestGrpc(t, executor, obs)

	attributes, _ := structpb.NewStruct(map[string]any{
		"user_prompt": "go",
		"cli":         map[string]any{"mcp_servers": "not-an-array"},
	})
	_, err := client.Execute(context.Background(), &genv1.ExecuteRequest{
		NodeId:      "node-a",
		NodeType:    "claude-agent",
		DispatchId:  "disp-bad",
		Attributes:  attributes,
		CallbackUrl: "http://supervisor.invalid/cb",
	})
	if err != nil {
		t.Fatal(err)
	}
	call := recorder.waitForCall(t)
	errBody, ok := call.body["error"].(map[string]any)
	if !ok || errBody["error_class"] != "agent/attribute_invalid" {
		t.Fatalf("expected attribute_invalid error callback, got %v", call.body)
	}
}

func TestOutcomeToCallbackBodyShapes(t *testing.T) {
	summary := "did it"
	resumeAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

	complete := OutcomeToCallbackBody(AgentOutcome{
		Kind:            OutcomeComplete,
		AttributesDelta: map[string]any{"a": 1},
		Changed:         true,
		ChangeSummary:   &summary,
	})
	raw, _ := json.Marshal(complete)
	if !strings.Contains(string(raw), `"changed":true`) || !strings.Contains(string(raw), `"change_summary":"did it"`) {
		t.Fatalf("complete body = %s", raw)
	}

	blocked := OutcomeToCallbackBody(AgentOutcome{Kind: OutcomeBlocked, Reason: "waiting"})
	errBody := blocked["error"].(map[string]any)
	if errBody["error_class"] != "agent/blocked" {
		t.Fatalf("blocked body = %v", blocked)
	}

	park := OutcomeToCallbackBody(AgentOutcome{
		Kind:         OutcomeParkRequested,
		Reason:       "snooze",
		ReasonNote:   "later",
		ResumeAt:     &resumeAt,
		SessionToken: "sess-1",
	})
	parkBody := park["park"].(map[string]any)
	if parkBody["reason"] != "snooze" || parkBody["scratch"] != SessionTokenToScratchBase64("sess-1") {
		t.Fatalf("park body = %v", park)
	}
	if parkBody["resume_at"] != "2026-07-02T12:00:00Z" {
		t.Fatalf("resume_at = %v", parkBody["resume_at"])
	}

	errored := OutcomeToCallbackBody(AgentOutcome{
		Kind:       OutcomeErrored,
		ErrorClass: "agent/timeout",
		Payload:    map[string]any{"silence_duration_ms": 500},
	})
	errBody = errored["error"].(map[string]any)
	if errBody["error_class"] != "agent/timeout" {
		t.Fatalf("errored body = %v", errored)
	}
}

func TestSessionTokenScratchRoundTrip(t *testing.T) {
	if SessionTokenFromScratch(nil) != "" {
		t.Fatal("nil scratch must yield empty token")
	}
	if got := SessionTokenFromScratch([]byte("run-99")); got != "run-99" {
		t.Fatalf("got %q", got)
	}
	b64 := SessionTokenToScratchBase64("run-99")
	if got := SessionTokenFromScratchBase64(b64); got != "run-99" {
		t.Fatalf("round trip = %q", got)
	}
	if SessionTokenFromScratchBase64("") != "" {
		t.Fatal("empty scratch string must yield empty token")
	}
}

func TestParseCliConfigNewShape(t *testing.T) {
	raw := `{
		"bare": true,
		"permission_mode": "acceptEdits",
		"allowed_tools": ["Read"],
		"expose_env": ["VALIDATOR_TOKEN"],
		"max_schema_corrections": 5,
		"mcp_servers": [
			{"transport": "http", "name": "search", "url": "https://x/", "headers": {"a": "b"}, "allowed_tools": ["query"]},
			{"transport": "stdio", "name": "local", "command": "/bin/tool", "args": ["--serve"], "env": {"M": "1"}},
			{"transport": "http-loopback", "name": "loop", "module": "witness"}
		],
		"required_signoffs": [{"public_key": "PEM", "path": "endpoints"}],
		"silence_timeout_ms": 250
	}`
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseCliConfig(v)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Bare || cfg.PermissionMode != "acceptEdits" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.ExposeEnv) != 1 || cfg.ExposeEnv[0] != "VALIDATOR_TOKEN" {
		t.Fatalf("expose env = %v", cfg.ExposeEnv)
	}
	if cfg.MaxSchemaCorrections == nil || *cfg.MaxSchemaCorrections != 5 {
		t.Fatalf("max corrections = %v", cfg.MaxSchemaCorrections)
	}
	if len(cfg.McpServers) != 3 {
		t.Fatalf("servers = %+v", cfg.McpServers)
	}
	if cfg.McpServers[0].Transport != "http" || cfg.McpServers[0].Headers["a"] != "b" {
		t.Fatalf("http entry = %+v", cfg.McpServers[0])
	}
	if cfg.McpServers[1].Command != "/bin/tool" || cfg.McpServers[1].Env["M"] != "1" {
		t.Fatalf("stdio entry = %+v", cfg.McpServers[1])
	}
	if cfg.McpServers[2].Module != "witness" {
		t.Fatalf("module entry = %+v", cfg.McpServers[2])
	}
	if len(cfg.RequiredSignoffs) != 1 || cfg.RequiredSignoffs[0].Path != "endpoints" {
		t.Fatalf("signoffs = %+v", cfg.RequiredSignoffs)
	}
	if cfg.SilenceTimeoutMs == nil || *cfg.SilenceTimeoutMs != 250 {
		t.Fatalf("silence = %v", cfg.SilenceTimeoutMs)
	}
}

func TestParseCliConfigRejectsRetiredRefShape(t *testing.T) {
	var v map[string]any
	_ = json.Unmarshal([]byte(`{"mcp_servers": [{"ref": "catalog-entry"}]}`), &v)
	_, err := ParseCliConfig(v)
	if err == nil || !strings.Contains(err.Error(), "retired {ref} shape") {
		t.Fatalf("expected retired-ref rejection, got %v", err)
	}
}

func TestParseCliConfigRejectsNegativeTimeout(t *testing.T) {
	var v map[string]any
	_ = json.Unmarshal([]byte(`{"silence_timeout_ms": -1}`), &v)
	if _, err := ParseCliConfig(v); err == nil {
		t.Fatal("expected rejection of negative timeout")
	}
}
