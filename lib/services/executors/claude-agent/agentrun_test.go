// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHandle struct {
	mu            sync.Mutex
	stdoutCbs     []func(string)
	stderrCbs     []func(string)
	exitCbs       []func(ExitResult)
	exited        bool
	result        ExitResult
	done          chan struct{}
	exitOnSigterm bool
	sigterms      int
	sigkills      int
}

func newFakeHandle(exitOnSigterm bool) *fakeHandle {
	return &fakeHandle{done: make(chan struct{}), exitOnSigterm: exitOnSigterm}
}

func (h *fakeHandle) Pid() int { return 4242 }

func (h *fakeHandle) OnStdout(cb func(string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stdoutCbs = append(h.stdoutCbs, cb)
}

func (h *fakeHandle) OnStderr(cb func(string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stderrCbs = append(h.stderrCbs, cb)
}

func (h *fakeHandle) OnExit(cb func(ExitResult)) {
	h.mu.Lock()
	if h.exited {
		result := h.result
		h.mu.Unlock()
		cb(result)
		return
	}
	h.exitCbs = append(h.exitCbs, cb)
	h.mu.Unlock()
}

func (h *fakeHandle) emitStderr(chunk string) {
	h.mu.Lock()
	cbs := append([]func(string){}, h.stderrCbs...)
	h.mu.Unlock()
	for _, cb := range cbs {
		cb(chunk)
	}
}

func (h *fakeHandle) emitStdout(chunk string) {
	h.mu.Lock()
	cbs := append([]func(string){}, h.stdoutCbs...)
	h.mu.Unlock()
	for _, cb := range cbs {
		cb(chunk)
	}
}

func (h *fakeHandle) waitStdoutRegistered() {
	for {
		h.mu.Lock()
		n := len(h.stdoutCbs)
		h.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *fakeHandle) waitStderrRegistered() {
	for {
		h.mu.Lock()
		n := len(h.stderrCbs)
		h.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *fakeHandle) exit(result ExitResult) {
	h.mu.Lock()
	if h.exited {
		h.mu.Unlock()
		return
	}
	h.exited = true
	h.result = result
	cbs := append([]func(ExitResult){}, h.exitCbs...)
	h.mu.Unlock()
	close(h.done)
	for _, cb := range cbs {
		cb(result)
	}
}

func (h *fakeHandle) SendSigterm() {
	h.mu.Lock()
	h.sigterms++
	exitNow := h.exitOnSigterm && !h.exited
	h.mu.Unlock()
	if exitNow {
		h.exit(ExitResult{Signal: "terminated"})
	}
}

func (h *fakeHandle) SendSigkill() {
	h.mu.Lock()
	h.sigkills++
	exited := h.exited
	h.mu.Unlock()
	if !exited {
		h.exit(ExitResult{Signal: "killed"})
	}
}

func (h *fakeHandle) WaitExit() ExitResult {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.result
}

type fakeRunner struct {
	mu            sync.Mutex
	spawns        []CliSpawnRequest
	resumes       []CliResumeRequest
	spawnHandles  []*fakeHandle
	resumeHandles []*fakeHandle
	spawnErr      error
	resumeErr     error
}

func (r *fakeRunner) Spawn(req CliSpawnRequest) (CliHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spawns = append(r.spawns, req)
	if r.spawnErr != nil {
		return nil, r.spawnErr
	}
	if len(r.spawnHandles) == 0 {
		return nil, fmt.Errorf("fakeRunner: no spawn handle scripted")
	}
	h := r.spawnHandles[0]
	r.spawnHandles = r.spawnHandles[1:]
	return h, nil
}

func (r *fakeRunner) Resume(req CliResumeRequest) (CliHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumes = append(r.resumes, req)
	if r.resumeErr != nil {
		return nil, r.resumeErr
	}
	if len(r.resumeHandles) == 0 {
		return nil, fmt.Errorf("fakeRunner: no resume handle scripted")
	}
	h := r.resumeHandles[0]
	r.resumeHandles = r.resumeHandles[1:]
	return h, nil
}

func (r *fakeRunner) waitForSpawn(t *testing.T) CliSpawnRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.spawns) > 0 {
			req := r.spawns[0]
			r.mu.Unlock()
			return req
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("spawn was not called within 5s")
	return CliSpawnRequest{}
}

func baseRunOpts(runner CliRunner) AgentRunOptions {
	return AgentRunOptions{
		SessionID:    "run-1",
		NodeID:       "node-a",
		NodeType:     "claude-agent",
		InstanceID:   "instance-1",
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "S",
		UserPrompt:   "U",
		Attributes:   map[string]any{},
		NodeRunID:    "disp-1",
		RunScopeID:   "rs-1",
		CallbackURL:  "http://supervisor.invalid/cb",
		CancelToken:  "ct-1",
		CliRunner:    runner,
	}
}

func mcpClientFor(t *testing.T, req CliSpawnRequest) (*mcpTestClient, string) {
	t.Helper()
	url := req.Env["RIMSKY_CALLBACK_URL"]
	token := req.Env["RIMSKY_CALLBACK_TOKEN"]
	if url == "" || token == "" {
		t.Fatalf("spawn env missing callback plumbing: %v", req.Env)
	}
	client := &mcpTestClient{t: t, url: url}
	client.initialize()
	return client, token
}

func TestRunAgentStubModeCompletes(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	outcome := RunAgent(baseRunOpts(&fakeRunner{}))
	if outcome.Kind != OutcomeComplete || outcome.AttributesDelta["stub"] != true {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.AttributesDelta["session_token"] != "run-1" {
		t.Fatalf("expected session token, got %v", outcome.AttributesDelta)
	}
}

func TestRunAgentStubProbePark(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	opts := baseRunOpts(&fakeRunner{})
	opts.Attributes = map[string]any{"probe_park": true}
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeParkRequested || outcome.ResumeAt == nil {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentStubProbeAsyncRoutesToStubNotRealCLI(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	opts := baseRunOpts(&fakeRunner{})
	opts.Attributes = map[string]any{"probe_async": true}
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeComplete || outcome.AttributesDelta["stub"] != true {
		t.Fatalf("probe_async alone must route to the stub path (fast, deterministic), not attempt a real CLI spawn: outcome = %+v", outcome)
	}
}

func TestRunAgentMalformedAttributes(t *testing.T) {
	opts := baseRunOpts(&fakeRunner{})
	opts.Attributes = map[string]any{"_invalid": "yes"}
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/attribute_invalid" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentCompleteViaMcpCallback(t *testing.T) {
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()

	req := runner.waitForSpawn(t)
	client, token := mcpClientFor(t, req)
	result, rpcErr := client.callTool("report_complete",
		fmt.Sprintf(`{"token":%q,"attributes_delta":{"answer":42},"changed":true,"change_summary":"done"}`, token))
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if client.firstText(result) != `{"status":"accepted"}` {
		t.Fatalf("ack = %q", client.firstText(result))
	}

	outcome := <-done
	if outcome.Kind != OutcomeComplete || !outcome.Changed {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.AttributesDelta["answer"] != float64(42) || outcome.AttributesDelta["session_token"] != "run-1" {
		t.Fatalf("delta = %v", outcome.AttributesDelta)
	}
	if req.SessionID != "run-1" {
		t.Fatalf("spawn session id = %q", req.SessionID)
	}
	if !strings.Contains(req.UserPrompt, "callback_token: "+token) ||
		!strings.Contains(req.UserPrompt, "binding_id: disp-1") {
		t.Fatalf("user prompt missing callback plumbing: %q", req.UserPrompt)
	}
}

func TestRunAgentSchemaCorrectionThenExhaustion(t *testing.T) {
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	max := 1
	opts.CliConfig = &CliConfig{MaxSchemaCorrections: &max}
	opts.AttributesSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
	}
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()

	req := runner.waitForSpawn(t)
	client, token := mcpClientFor(t, req)

	badCall := fmt.Sprintf(`{"token":%q,"attributes_delta":{"count":"not-an-int"},"changed":true}`, token)
	result, _ := client.callTool("report_complete", badCall)
	if client.firstText(result) == `{"status":"accepted"}` {
		t.Fatal("first violation should be rejected for correction")
	}
	if !strings.Contains(client.firstText(result), "correction 1/1") {
		t.Fatalf("expected correction counter, got %q", client.firstText(result))
	}

	result, _ = client.callTool("report_complete", badCall)
	if client.firstText(result) != `{"status":"accepted"}` {
		t.Fatalf("exhausted violation should be accepted-with-teardown, got %q", client.firstText(result))
	}

	outcome := <-done
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/schema_violation" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentSignoffGateRejectsThenAccepts(t *testing.T) {
	signer := makeTestSigner(t)
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{
		RequiredSignoffs: []RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "endpoints"}},
	}
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()

	req := runner.waitForSpawn(t)
	client, token := mcpClientFor(t, req)

	result, _ := client.callTool("report_complete",
		fmt.Sprintf(`{"token":%q,"attributes_delta":{"endpoints":[{"url":"x"}]},"changed":true}`, token))
	if !strings.Contains(client.firstText(result), "unmet sign-offs") {
		t.Fatalf("expected sign-off rejection, got %q", client.firstText(result))
	}

	sig := signer.sign(t, "disp-1", []any{map[string]any{"url": "x"}})
	result, _ = client.callTool("report_complete",
		fmt.Sprintf(`{"token":%q,"attributes_delta":{"endpoints":[{"url":"x"}]},"changed":true,"signoffs":[%q]}`, token, sig))
	if client.firstText(result) != `{"status":"accepted"}` {
		t.Fatalf("expected acceptance with valid signature, got %q", client.firstText(result))
	}

	outcome := <-done
	if outcome.Kind != OutcomeComplete {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentSignoffGateMisconfiguredKeyFailsImmediatelyAsAttributeInvalid(t *testing.T) {
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{
		RequiredSignoffs: []RequiredSignoff{{PublicKey: "not a real PEM at all", Path: "endpoints"}},
	}
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()

	req := runner.waitForSpawn(t)
	client, token := mcpClientFor(t, req)

	result, _ := client.callTool("report_complete",
		fmt.Sprintf(`{"token":%q,"attributes_delta":{"endpoints":[{"url":"x"}]},"changed":true,"signoffs":["AA=="]}`, token))
	if client.firstText(result) != `{"status":"accepted"}` {
		t.Fatalf("a misconfigured signoff key can never be satisfied by any signature; "+
			"the FIRST report_complete must be accepted as terminal (no retry prompt), got %q", client.firstText(result))
	}

	outcome := <-done
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/attribute_invalid" {
		t.Fatalf("outcome = %+v, want OutcomeErrored/agent/attribute_invalid "+
			"(a node-config error, not agent/signoff_unobtained which implies the agent could have fixed it by re-signing)", outcome)
	}
}

func TestRunAgentReportErrorRejectsUndeclaredErrorClass(t *testing.T) {
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	req := runner.waitForSpawn(t)
	client, token := mcpClientFor(t, req)

	_, rpcErr := client.callTool("report_error",
		fmt.Sprintf(`{"token":%q,"error_class":"not_a_declared_class"}`, token))
	if rpcErr == nil {
		t.Fatal("expected a JSON-RPC error rejecting the undeclared error_class, got nil")
	}
	if !strings.Contains(rpcErr.Message, "not_a_declared_class") {
		t.Fatalf("expected rejection message to name the undeclared class, got %q", rpcErr.Message)
	}

	if _, rpcErr := client.callTool("report_error",
		fmt.Sprintf(`{"token":%q,"error_class":"agent/subprocess_exit/before_complete"}`, token)); rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	outcome := <-done
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/subprocess_exit/before_complete" {
		t.Fatalf("outcome = %+v, want a declared wildcard-matched error_class to be accepted", outcome)
	}
}

func TestRunAgentBlockedAndParkViaMcp(t *testing.T) {
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true), newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	req := runner.waitForSpawn(t)
	client, token := mcpClientFor(t, req)
	if _, rpcErr := client.callTool("report_blocked", fmt.Sprintf(`{"token":%q,"reason":"waiting on signal"}`, token)); rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	outcome := <-done
	if outcome.Kind != OutcomeBlocked || outcome.Reason != "waiting on signal" {
		t.Fatalf("outcome = %+v", outcome)
	}

	opts2 := baseRunOpts(runner)
	done2 := make(chan AgentOutcome, 1)
	go func() { done2 <- RunAgent(opts2) }()
	deadline := time.Now().Add(5 * time.Second)
	var req2 CliSpawnRequest
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		if len(runner.spawns) > 1 {
			req2 = runner.spawns[1]
			runner.mu.Unlock()
			break
		}
		runner.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	client2, token2 := mcpClientFor(t, req2)
	resumeAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, rpcErr := client2.callTool("report_park",
		fmt.Sprintf(`{"token":%q,"resume_at":%q}`, token2, resumeAt)); rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	outcome2 := <-done2
	if outcome2.Kind != OutcomeParkRequested || outcome2.ResumeAt == nil || outcome2.SessionToken != "run-1" {
		t.Fatalf("outcome = %+v", outcome2)
	}
}

func TestRunAgentExposeEnvAllowlistViolation(t *testing.T) {
	runner := &fakeRunner{}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{ExposeEnv: []string{"ALLOWED_VAR", "FORBIDDEN_VAR"}}
	opts.ExposeEnvAllowlist = NewAllowlist([]string{"ALLOWED_VAR"})
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/attribute_invalid" {
		t.Fatalf("outcome = %+v", outcome)
	}
	payload := outcome.Payload.(map[string]any)
	reason := payload["reason"].(string)
	for _, needle := range []string{"FORBIDDEN_VAR", "instance-1", "node-a", "RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST"} {
		if !strings.Contains(reason, needle) {
			t.Fatalf("rejection must name %q, got %q", needle, reason)
		}
	}
	if payload["disallowed_env_var"] != "FORBIDDEN_VAR" {
		t.Fatalf("payload = %v", payload)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.spawns) != 0 {
		t.Fatal("dispatch must not spawn on allowlist violation")
	}
}

func TestRunAgentExposeEnvPassedToSpawn(t *testing.T) {
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{ExposeEnv: []string{"VALIDATOR_TOKEN"}}
	opts.ExposeEnvAllowlist = NewAllowlist([]string{"VALIDATOR_TOKEN"})
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	req := runner.waitForSpawn(t)
	if len(req.ExposeEnvNames) != 1 || req.ExposeEnvNames[0] != "VALIDATOR_TOKEN" {
		t.Fatalf("expose env names = %v", req.ExposeEnvNames)
	}
	client, token := mcpClientFor(t, req)
	_, _ = client.callTool("report_complete", fmt.Sprintf(`{"token":%q,"changed":false}`, token))
	<-done
}

func TestRunAgentMcpAllowlistViolation(t *testing.T) {
	runner := &fakeRunner{}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{McpServers: []McpServerInput{
		{Transport: "http", Name: "forbidden-server", URL: "https://mcp.example.invalid/"},
	}}
	opts.McpAllowlist = NewAllowlist([]string{"some-other-server"})
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/attribute_invalid" {
		t.Fatalf("outcome = %+v", outcome)
	}
	payload := outcome.Payload.(map[string]any)
	reason := payload["reason"].(string)
	for _, needle := range []string{"forbidden-server", "instance-1", "node-a", "RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST"} {
		if !strings.Contains(reason, needle) {
			t.Fatalf("rejection must name %q, got %q", needle, reason)
		}
	}
	if payload["disallowed_mcp_server"] != "forbidden-server" {
		t.Fatalf("payload = %v", payload)
	}
}

func TestRunAgentStdioBlockedWhenAllowlistClosed(t *testing.T) {
	runner := &fakeRunner{}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{McpServers: []McpServerInput{
		{Transport: "stdio", Name: "search", Command: "/bin/sh", Args: []string{"-c", "id"}},
	}}
	opts.McpAllowlist = NewAllowlist([]string{"search"})
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/attribute_invalid" {
		t.Fatalf("an allowlisted name with a node-spawned stdio command must be refused when the allowlist is set: %+v", outcome)
	}
	payload := outcome.Payload.(map[string]any)
	if payload["disallowed_mcp_server"] != "search" {
		t.Fatalf("payload = %v", payload)
	}
	if reason, _ := payload["reason"].(string); !strings.Contains(reason, "stdio") {
		t.Fatalf("rejection must explain the stdio boundary, got %q", reason)
	}
}

func TestRunAgentStdioAllowedWhenAllowlistOpen(t *testing.T) {
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{McpServers: []McpServerInput{
		{Transport: "stdio", Name: "local-tool", Command: "/bin/tool", Args: []string{"--serve"}},
	}}
	opts.McpAllowlist = OpenAllowlist()
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	req := runner.waitForSpawn(t)
	found := false
	for _, tool := range req.Tools {
		if tool.Kind == CliToolKindMcpStdio && tool.Name == "local-tool" {
			found = true
		}
	}
	if !found {
		t.Fatalf("open allowlist must still permit a stdio server: %v", req.Tools)
	}
	client, token := mcpClientFor(t, req)
	_, _ = client.callTool("report_complete", fmt.Sprintf(`{"token":%q,"changed":false}`, token))
	<-done
}

func TestRunAgentMcpServersReachSpawnAcrossTransports(t *testing.T) {
	RegisterMcpModule("test-witness-module", func() *ModuleMcpServer {
		return &ModuleMcpServer{
			Name: "loopback",
			Tools: []ModuleMcpTool{{
				Definition: ToolDefinition{
					Name:        "witness",
					Description: "test witness",
					InputSchema: map[string]any{"type": "object"},
				},
				Handler: func(args map[string]any) (string, bool, error) {
					return "witness-called", false, nil
				},
			}},
		}
	})
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{McpServers: []McpServerInput{
		{Transport: "http", Name: "search", URL: "https://mcp.example.invalid/", AllowedTools: []string{"query"}},
		{Transport: "stdio", Name: "local-tool", Command: "/bin/tool", Args: []string{"--serve"}},
		{Transport: "module", Name: "loopback", Module: "test-witness-module"},
	}}
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	req := runner.waitForSpawn(t)

	if len(req.Tools) != 4 {
		t.Fatalf("expected callback + 3 host servers, got %v", req.Tools)
	}
	if req.Tools[0].Name != CallbackMCPServerName {
		t.Fatalf("first tool must be the callback server: %v", req.Tools[0])
	}
	if req.Tools[1].Kind != CliToolKindMcpHTTP || req.Tools[1].URL != "https://mcp.example.invalid/" {
		t.Fatalf("http server wrong: %v", req.Tools[1])
	}
	if req.Tools[2].Kind != CliToolKindMcpStdio || req.Tools[2].Command != "/bin/tool" {
		t.Fatalf("stdio server wrong: %v", req.Tools[2])
	}
	if req.Tools[3].Kind != CliToolKindMcpHTTP || !strings.HasPrefix(req.Tools[3].URL, "http://127.0.0.1:") {
		t.Fatalf("module server must be a loopback http url: %v", req.Tools[3])
	}
	moduleAuth := req.Tools[3].Headers["Authorization"]
	if !strings.HasPrefix(moduleAuth, "Bearer ") {
		t.Fatalf("module loopback tool config must carry a bearer Authorization header: %v", req.Tools[3].Headers)
	}
	found := false
	for _, tool := range req.AllowedTools {
		if tool == "mcp__search__query" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected mcp__search__query in allowed tools: %v", req.AllowedTools)
	}

	unauth := &mcpTestClient{t: t, url: req.Tools[3].URL}
	if resp, _ := unauth.post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("module loopback must reject a request with no bearer token, got status %d", resp.StatusCode)
	}

	witnessClient := &mcpTestClient{t: t, url: req.Tools[3].URL, authHeader: moduleAuth}
	witnessClient.initialize()
	if witnessClient.serverName != "loopback" {
		t.Fatalf("module server name = %q", witnessClient.serverName)
	}
	result, rpcErr := witnessClient.callTool("witness", `{}`)
	if rpcErr != nil || witnessClient.firstText(result) != "witness-called" {
		t.Fatalf("module loopback not serving: %+v %+v", result, rpcErr)
	}

	client, token := mcpClientFor(t, req)
	_, _ = client.callTool("report_complete", fmt.Sprintf(`{"token":%q,"changed":false}`, token))
	<-done
}

func TestRunAgentUnknownModuleErrors(t *testing.T) {
	runner := &fakeRunner{}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{McpServers: []McpServerInput{
		{Transport: "module", Name: "loopback", Module: "no-such-module"},
	}}
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/attribute_invalid" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentUnknownTransportErrors(t *testing.T) {
	runner := &fakeRunner{}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{McpServers: []McpServerInput{
		{Transport: "carrier-pigeon", Name: "x"},
	}}
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/attribute_invalid" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentCwdResolutionFailure(t *testing.T) {
	runner := &fakeRunner{}
	opts := baseRunOpts(runner)
	opts.CwdOverride = "/definitely/not/a/real/path"
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/attribute_invalid" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentSpawnFailure(t *testing.T) {
	runner := &fakeRunner{spawnErr: fmt.Errorf("binary not found")}
	opts := baseRunOpts(runner)
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/cli_spawn_failed" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentSilenceTimeout(t *testing.T) {
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	silence := 200
	opts.CliConfig = &CliConfig{SilenceTimeoutMs: &silence}
	outcome := RunAgent(opts)
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/timeout" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentSilenceTimeoutFiresWithOpenToolUse(t *testing.T) {
	h := newFakeHandle(true)
	runner := &fakeRunner{spawnHandles: []*fakeHandle{h}}
	opts := baseRunOpts(runner)
	silence := 100
	opts.CliConfig = &CliConfig{SilenceTimeoutMs: &silence}
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	h.waitStdoutRegistered()
	h.emitStdout(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_hung","name":"Bash"}]}}` + "\n")

	outcome := <-done
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/timeout" {
		t.Fatalf("outcome = %+v, want agent/timeout (silence_timeout must still fire while a tool_use is open and tool_use_timeout is unset)", outcome)
	}
}

func TestRunAgentRateLimitParksByDefault(t *testing.T) {
	h := newFakeHandle(true)
	runner := &fakeRunner{spawnHandles: []*fakeHandle{h}}
	opts := baseRunOpts(runner)
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	runner.waitForSpawn(t)
	h.emitStderr("rate_limit_error: too many requests\nretry-after: 60")
	code := 1
	h.exit(ExitResult{ExitCode: &code})
	outcome := <-done
	if outcome.Kind != OutcomeParkRequested || outcome.ResumeAt == nil {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.SessionToken != "run-1" {
		t.Fatalf("session token = %q", outcome.SessionToken)
	}
}

func TestRunAgentRateLimitErrorsWhenHandlingDisabled(t *testing.T) {
	h := newFakeHandle(true)
	runner := &fakeRunner{spawnHandles: []*fakeHandle{h}}
	opts := baseRunOpts(runner)
	off := false
	opts.CliConfig = &CliConfig{HandleRateLimits: &off}
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	runner.waitForSpawn(t)
	h.emitStderr("rate_limit_error: too many requests")
	code := 1
	h.exit(ExitResult{ExitCode: &code})
	outcome := <-done
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/rate_limited" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentCleanExitTriggersResumeReminder(t *testing.T) {
	first := newFakeHandle(true)
	retry := newFakeHandle(true)
	runner := &fakeRunner{spawnHandles: []*fakeHandle{first}, resumeHandles: []*fakeHandle{retry}}
	opts := baseRunOpts(runner)
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	runner.waitForSpawn(t)
	code := 0
	first.exit(ExitResult{ExitCode: &code})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		if len(runner.resumes) > 0 {
			runner.mu.Unlock()
			break
		}
		runner.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	runner.mu.Lock()
	if len(runner.resumes) != 1 {
		runner.mu.Unlock()
		t.Fatal("expected a resume attempt after clean exit without report")
	}
	resumeReq := runner.resumes[0]
	runner.mu.Unlock()
	if resumeReq.SessionID != "run-1" || !strings.Contains(resumeReq.Prompt, "report_complete") {
		t.Fatalf("resume request wrong: %+v", resumeReq)
	}

	retryCode := 0
	retry.exit(ExitResult{ExitCode: &retryCode})
	outcome := <-done
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/subprocess_exit/before_complete" {
		t.Fatalf("outcome = %+v", outcome)
	}
	payload := outcome.Payload.(map[string]any)
	if payload["retry_attempted"] != true {
		t.Fatalf("payload = %v", payload)
	}
}

func TestRunAgentRetryLegRateLimitParks(t *testing.T) {
	first := newFakeHandle(true)
	retry := newFakeHandle(true)
	runner := &fakeRunner{spawnHandles: []*fakeHandle{first}, resumeHandles: []*fakeHandle{retry}}
	opts := baseRunOpts(runner)
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()
	runner.waitForSpawn(t)
	code := 0
	first.exit(ExitResult{ExitCode: &code})

	for {
		runner.mu.Lock()
		n := len(runner.resumes)
		runner.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	retry.waitStderrRegistered()
	retry.emitStderr("API Error: 429 rate_limit_error; anthropic-ratelimit-requests-reset: 4070908800")
	retryCode := 1
	retry.exit(ExitResult{ExitCode: &retryCode})

	outcome := <-done
	if outcome.Kind != OutcomeParkRequested {
		t.Fatalf("a rate limit on the retry leg must park, got %+v", outcome)
	}
	if outcome.ResumeAt == nil {
		t.Fatalf("expected resume_at parsed from anthropic-ratelimit-requests-reset (1856), got nil")
	}
}

func TestRunAgentSessionTokenTriggersResumePath(t *testing.T) {
	retry := newFakeHandle(true)
	runner := &fakeRunner{resumeHandles: []*fakeHandle{retry}}
	opts := baseRunOpts(runner)
	opts.SessionToken = "prior-session"
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()

	deadline := time.Now().Add(5 * time.Second)
	var resumeReq CliResumeRequest
	found := false
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		if len(runner.resumes) > 0 {
			resumeReq = runner.resumes[0]
			found = true
			runner.mu.Unlock()
			break
		}
		runner.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	if !found {
		t.Fatal("expected resume for non-empty session token")
	}
	if resumeReq.SessionID != "prior-session" {
		t.Fatalf("resume session = %q", resumeReq.SessionID)
	}
	if resumeReq.NewSessionID != "run-1" {
		t.Fatalf("resume must rebind the resumed conversation onto the current dispatch's run id so the next resume targets an existing CLI session, got NewSessionID = %q", resumeReq.NewSessionID)
	}
	client := &mcpTestClient{t: t, url: resumeReq.Env["RIMSKY_CALLBACK_URL"]}
	client.initialize()
	_, _ = client.callTool("report_complete",
		fmt.Sprintf(`{"token":%q,"changed":false}`, resumeReq.Env["RIMSKY_CALLBACK_TOKEN"]))
	outcome := <-done
	if outcome.Kind != OutcomeComplete {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestRunAgentCleanExitOnResumedRunRetriesOnReboundSession(t *testing.T) {
	firstLeg := newFakeHandle(true)
	retryLeg := newFakeHandle(true)
	runner := &fakeRunner{resumeHandles: []*fakeHandle{firstLeg, retryLeg}}
	opts := baseRunOpts(runner)
	opts.SessionToken = "prior-session"
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		n := len(runner.resumes)
		runner.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	runner.mu.Lock()
	if len(runner.resumes) != 1 {
		runner.mu.Unlock()
		t.Fatal("expected the first resume attempt")
	}
	firstResumeReq := runner.resumes[0]
	runner.mu.Unlock()
	if firstResumeReq.SessionID != "prior-session" || firstResumeReq.NewSessionID != "run-1" {
		t.Fatalf("first resume wrong: %+v", firstResumeReq)
	}

	code := 0
	firstLeg.exit(ExitResult{ExitCode: &code})

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		n := len(runner.resumes)
		runner.mu.Unlock()
		if n > 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	runner.mu.Lock()
	if len(runner.resumes) != 2 {
		runner.mu.Unlock()
		t.Fatal("expected a reminder-retry resume after the clean exit on the resumed leg")
	}
	retryReq := runner.resumes[1]
	runner.mu.Unlock()
	if retryReq.SessionID != "run-1" {
		t.Fatalf("reminder retry must resume the session the run actually started with (rebound to run-1 via --session-id on the first resume), got SessionID = %q", retryReq.SessionID)
	}

	client := &mcpTestClient{t: t, url: retryReq.Env["RIMSKY_CALLBACK_URL"]}
	client.initialize()
	_, _ = client.callTool("report_complete",
		fmt.Sprintf(`{"token":%q,"changed":false}`, retryReq.Env["RIMSKY_CALLBACK_TOKEN"]))
	outcome := <-done
	if outcome.Kind != OutcomeComplete {
		t.Fatalf("expected the reminder retry to recover the dispatch, got %+v", outcome)
	}
}
