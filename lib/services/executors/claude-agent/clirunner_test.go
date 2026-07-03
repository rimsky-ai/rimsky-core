// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

var testPaths = CliArgPaths{
	SystemPromptPath: "/tmp/sys.md",
	McpConfigPath:    "/tmp/mcp.json",
}

func baseReq(overrides func(*CliSpawnRequest)) CliSpawnRequest {
	req := CliSpawnRequest{
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "S",
		UserPrompt:   "U",
	}
	if overrides != nil {
		overrides(&req)
	}
	return req
}

func TestBuildClaudeCliArgsEmitsFixedCoreWithDefaults(t *testing.T) {
	t.Setenv("RIMSKY_DISPATCH_MAX_USD", "")
	args := BuildClaudeCliArgs(baseReq(nil), testPaths)
	want := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "claude-sonnet-4-6",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", strings.Join(RequiredCallbackTools(), " "),
		"--system-prompt-file", "/tmp/sys.md",
		"--mcp-config", "/tmp/mcp.json",
		"-p", "U",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v\nwant %v", args, want)
	}
}

func TestBuildClaudeCliArgsSplicesBare(t *testing.T) {
	args := BuildClaudeCliArgs(baseReq(func(r *CliSpawnRequest) { r.Bare = true }), testPaths)
	i := slices.Index(args, "--bare")
	if i < 0 {
		t.Fatal("expected --bare")
	}
	if args[i-1] != "bypassPermissions" {
		t.Fatalf("--bare not after permission mode: %v", args)
	}
	if slices.Contains(BuildClaudeCliArgs(baseReq(nil), testPaths), "--bare") {
		t.Fatal("expected no --bare when unset")
	}
}

func TestBuildClaudeCliArgsUsesSuppliedPermissionMode(t *testing.T) {
	args := BuildClaudeCliArgs(baseReq(func(r *CliSpawnRequest) { r.PermissionMode = "acceptEdits" }), testPaths)
	i := slices.Index(args, "--permission-mode")
	if args[i+1] != "acceptEdits" {
		t.Fatalf("permission mode = %q", args[i+1])
	}
}

func TestBuildClaudeCliArgsMergesAllowedToolsAndJoinsDisallowed(t *testing.T) {
	args := BuildClaudeCliArgs(baseReq(func(r *CliSpawnRequest) {
		r.AllowedTools = []string{"Read", "Edit", "mcp__rimsky-callback__report_complete"}
		r.DisallowedTools = []string{"Bash"}
	}), testPaths)
	aIdx := slices.Index(args, "--allowedTools")
	if aIdx < 0 {
		t.Fatal("expected --allowedTools")
	}
	allowed := strings.Split(args[aIdx+1], " ")
	required := RequiredCallbackTools()
	if !reflect.DeepEqual(allowed[:len(required)], required) {
		t.Fatalf("callback tools not first: %v", allowed)
	}
	if !slices.Contains(allowed, "Read") || !slices.Contains(allowed, "Edit") {
		t.Fatalf("template tools missing: %v", allowed)
	}
	count := 0
	for _, tool := range allowed {
		if tool == "mcp__rimsky-callback__report_complete" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected de-dup, found %d occurrences", count)
	}
	dIdx := slices.Index(args, "--disallowedTools")
	if dIdx < 0 || args[dIdx+1] != "Bash" {
		t.Fatalf("disallowed tools wrong: %v", args)
	}
}

func TestBuildClaudeCliArgsAlwaysEmitsCallbackSurface(t *testing.T) {
	args := BuildClaudeCliArgs(baseReq(func(r *CliSpawnRequest) {
		r.AllowedTools = []string{}
		r.DisallowedTools = []string{}
	}), testPaths)
	aIdx := slices.Index(args, "--allowedTools")
	if args[aIdx+1] != strings.Join(RequiredCallbackTools(), " ") {
		t.Fatalf("allowed = %q", args[aIdx+1])
	}
	if slices.Contains(args, "--disallowedTools") {
		t.Fatal("expected no --disallowedTools")
	}
}

func TestRequiredCallbackToolsDerivedFromDefinitions(t *testing.T) {
	required := RequiredCallbackTools()
	defs := toolDefinitions()
	if len(required) != len(defs) {
		t.Fatalf("len = %d, want %d", len(required), len(defs))
	}
	for i, d := range defs {
		want := "mcp__" + CallbackMCPServerName + "__" + d.Name
		if required[i] != want {
			t.Fatalf("required[%d] = %q, want %q", i, required[i], want)
		}
	}
	if !slices.Contains(required, "mcp__rimsky-callback__report_complete") ||
		!slices.Contains(required, "mcp__rimsky-callback__report_park") {
		t.Fatalf("expected canonical callback tools, got %v", required)
	}
	if slices.Contains(required, "mcp__rimsky-callback__emit_named_event") {
		t.Fatal("unexpected emit_named_event")
	}
}

func TestBuildClaudeCliArgsForwardsAddDirs(t *testing.T) {
	args := BuildClaudeCliArgs(baseReq(func(r *CliSpawnRequest) {
		r.AddDirs = []string{"../specs", "../guidance"}
	}), testPaths)
	i := slices.Index(args, "--add-dir")
	if i < 0 || args[i+1] != "../specs" || args[i+2] != "../guidance" {
		t.Fatalf("add dirs wrong: %v", args)
	}
}

func TestBuildClaudeCliArgsMaxBudgetPrecedence(t *testing.T) {
	t.Setenv("RIMSKY_DISPATCH_MAX_USD", "10.00")
	args := BuildClaudeCliArgs(baseReq(func(r *CliSpawnRequest) { r.MaxBudgetUSD = "0.50" }), testPaths)
	i := slices.Index(args, "--max-budget-usd")
	if i < 0 || args[i+1] != "0.50" {
		t.Fatalf("expected request budget to win: %v", args)
	}

	args = BuildClaudeCliArgs(baseReq(nil), testPaths)
	i = slices.Index(args, "--max-budget-usd")
	if i < 0 || args[i+1] != "10.00" {
		t.Fatalf("expected env fallback: %v", args)
	}

	t.Setenv("RIMSKY_DISPATCH_MAX_USD", "")
	args = BuildClaudeCliArgs(baseReq(nil), testPaths)
	if slices.Contains(args, "--max-budget-usd") {
		t.Fatal("expected no budget flag when neither source set")
	}
}

func TestBuildClaudeCliArgsSessionID(t *testing.T) {
	args := BuildClaudeCliArgs(baseReq(func(r *CliSpawnRequest) {
		r.SessionID = "550e8400-e29b-41d4-a716-446655440000"
	}), testPaths)
	i := slices.Index(args, "--session-id")
	if i < 0 || args[i+1] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("session id wrong: %v", args)
	}
	if slices.Contains(BuildClaudeCliArgs(baseReq(nil), testPaths), "--session-id") {
		t.Fatal("expected no --session-id when unset")
	}
}

func TestBuildClaudeCliResumeArgs(t *testing.T) {
	args := BuildClaudeCliResumeArgs(CliResumeRequest{
		SessionID: "550e8400-e29b-41d4-a716-446655440000",
		Prompt:    "finish what you started",
		Tools:     []CliToolConfig{{Kind: CliToolKindMcpHTTP, Name: "rimsky-callback", URL: "http://x/mcp"}},
	}, CliArgPaths{McpConfigPath: "/tmp/mcp.json"})

	mcpIdx := slices.Index(args, "--mcp-config")
	if mcpIdx < 0 || args[mcpIdx+1] != "/tmp/mcp.json" {
		t.Fatalf("resume must carry --mcp-config: %v", args)
	}
	if args[0] != "--resume" || args[1] != "550e8400-e29b-41d4-a716-446655440000" || args[2] != "--print" {
		t.Fatalf("resume prefix wrong: %v", args)
	}
	if args[len(args)-2] != "-p" || args[len(args)-1] != "finish what you started" {
		t.Fatalf("resume suffix wrong: %v", args)
	}
	if slices.Contains(args, "--system-prompt-file") {
		t.Fatal("resume must not carry --system-prompt-file")
	}
	aIdx := slices.Index(args, "--allowedTools")
	if aIdx < 0 || args[aIdx+1] != strings.Join(RequiredCallbackTools(), " ") {
		t.Fatalf("resume must carry callback surface: %v", args)
	}
}

func TestBuildClaudeCliArgsPromptAlwaysLast(t *testing.T) {
	t.Setenv("RIMSKY_DISPATCH_MAX_USD", "5.00")
	args := BuildClaudeCliArgs(baseReq(func(r *CliSpawnRequest) {
		r.Bare = true
		r.PermissionMode = "acceptEdits"
		r.AllowedTools = []string{"Read"}
		r.DisallowedTools = []string{"Bash"}
		r.AddDirs = []string{"../specs"}
		r.MaxBudgetUSD = "1.00"
	}), testPaths)
	if args[0] != "--print" || args[len(args)-2] != "-p" || args[len(args)-1] != "U" {
		t.Fatalf("ordering wrong: %v", args)
	}
}

func TestMcpConfigJSONShapes(t *testing.T) {
	raw, err := mcpConfigJSON([]CliToolConfig{
		{Kind: CliToolKindMcpHTTP, Name: "cb", URL: "http://x/mcp"},
		{Kind: CliToolKindMcpStdio, Name: "local", Command: "/bin/tool", Args: []string{"--serve"}, Env: map[string]string{"MODE": "quiet"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]map[string]map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	servers := parsed["mcpServers"]
	cb := servers["cb"]
	if cb["type"] != "http" || cb["url"] != "http://x/mcp" {
		t.Fatalf("http entry wrong: %v", cb)
	}
	if _, ok := cb["headers"]; !ok {
		t.Fatal("http entry must carry headers (default empty object)")
	}
	local := servers["local"]
	if local["type"] != "stdio" || local["command"] != "/bin/tool" {
		t.Fatalf("stdio entry wrong: %v", local)
	}
}

func writeFakeCli(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSpawnRunsBinaryAndDeliversOutputAndExit(t *testing.T) {
	binary := writeFakeCli(t, `echo "hello from fake"; echo "warn" >&2; exit 7`)
	runner := NewClaudeCliRunner(CliRunnerOpts{BinaryPath: binary})
	handle, err := runner.Spawn(baseReq(nil))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	var mu sync.Mutex
	handle.OnStdout(func(chunk string) { mu.Lock(); stdout.WriteString(chunk); mu.Unlock() })
	handle.OnStderr(func(chunk string) { mu.Lock(); stderr.WriteString(chunk); mu.Unlock() })
	result := handle.WaitExit()
	if result.ExitCode == nil || *result.ExitCode != 7 {
		t.Fatalf("exit = %+v, want code 7", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(stdout.String(), "hello from fake") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "warn") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSpawnExposesOnlyRequestedEnv(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_TEST_EXPOSED", "visible-value")
	t.Setenv("CLAUDE_AGENT_TEST_HIDDEN", "hidden-value")
	binary := writeFakeCli(t, `echo "EXPOSED=${CLAUDE_AGENT_TEST_EXPOSED:-missing} HIDDEN=${CLAUDE_AGENT_TEST_HIDDEN:-missing}"`)
	runner := NewClaudeCliRunner(CliRunnerOpts{BinaryPath: binary})
	handle, err := runner.Spawn(baseReq(func(r *CliSpawnRequest) {
		r.ExposeEnvNames = []string{"CLAUDE_AGENT_TEST_EXPOSED"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	var mu sync.Mutex
	handle.OnStdout(func(chunk string) { mu.Lock(); stdout.WriteString(chunk); mu.Unlock() })
	handle.WaitExit()
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(stdout.String(), "EXPOSED=visible-value") {
		t.Fatalf("expected exposed env visible, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "HIDDEN=missing") {
		t.Fatalf("expected hidden env absent, got %q", stdout.String())
	}
}

func TestSpawnSignalTermination(t *testing.T) {
	binary := writeFakeCli(t, `sleep 30`)
	runner := NewClaudeCliRunner(CliRunnerOpts{BinaryPath: binary})
	handle, err := runner.Spawn(baseReq(nil))
	if err != nil {
		t.Fatal(err)
	}
	handle.SendSigkill()
	result := handle.WaitExit()
	if result.ExitCode != nil {
		t.Fatalf("expected nil exit code for signaled exit, got %+v", result)
	}
	if result.Signal == "" {
		t.Fatal("expected signal name")
	}
}

func TestSpawnMissingBinaryReturnsError(t *testing.T) {
	runner := NewClaudeCliRunner(CliRunnerOpts{BinaryPath: "/nonexistent/claude-binary"})
	if _, err := runner.Spawn(baseReq(nil)); err == nil {
		t.Fatal("expected spawn error for missing binary")
	}
}
