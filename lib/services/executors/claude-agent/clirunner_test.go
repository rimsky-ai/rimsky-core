// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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

func mustArgs(t *testing.T, req CliSpawnRequest) []string {
	t.Helper()
	args, err := BuildClaudeCliArgs(req, testPaths)
	if err != nil {
		t.Fatalf("BuildClaudeCliArgs: %v", err)
	}
	return args
}

func TestBuildClaudeCliArgsEmitsFixedCoreWithDefaults(t *testing.T) {
	t.Setenv("RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD", "")
	args := mustArgs(t, baseReq(nil))
	want := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "claude-sonnet-4-6",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", strings.Join(RequiredCallbackTools(), " "),
		"--system-prompt-file", "/tmp/sys.md",
		"--mcp-config", "/tmp/mcp.json",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v\nwant %v", args, want)
	}
	if slices.Contains(args, "-p") {
		t.Fatalf("user prompt must not appear on argv (delivered via stdin): %v", args)
	}
}

func TestBuildClaudeCliArgsRejectsFlagLikeConfigValues(t *testing.T) {
	cases := []struct {
		name     string
		override func(*CliSpawnRequest)
	}{
		{"add_dirs", func(r *CliSpawnRequest) { r.AddDirs = []string{"--mcp-config", "/agent-writable.json"} }},
		{"model", func(r *CliSpawnRequest) { r.Model = "--dangerously-skip-permissions" }},
		{"permission_mode", func(r *CliSpawnRequest) { r.PermissionMode = "--add-dir" }},
		{"max_budget_usd", func(r *CliSpawnRequest) { r.MaxBudgetUSD = "--foo" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildClaudeCliArgs(baseReq(tc.override), testPaths); err == nil {
				t.Fatalf("expected a flag-injection rejection for %s beginning with '-'", tc.name)
			}
		})
	}
}

func TestBuildClaudeCliArgsSplicesBare(t *testing.T) {
	args := mustArgs(t, baseReq(func(r *CliSpawnRequest) { r.Bare = true }))
	i := slices.Index(args, "--bare")
	if i < 0 {
		t.Fatal("expected --bare")
	}
	if args[i-1] != "bypassPermissions" {
		t.Fatalf("--bare not after permission mode: %v", args)
	}
	if slices.Contains(mustArgs(t, baseReq(nil)), "--bare") {
		t.Fatal("expected no --bare when unset")
	}
}

func TestBuildClaudeCliArgsUsesSuppliedPermissionMode(t *testing.T) {
	args := mustArgs(t, baseReq(func(r *CliSpawnRequest) { r.PermissionMode = "acceptEdits" }))
	i := slices.Index(args, "--permission-mode")
	if args[i+1] != "acceptEdits" {
		t.Fatalf("permission mode = %q", args[i+1])
	}
}

func TestBuildClaudeCliArgsMergesAllowedToolsAndJoinsDisallowed(t *testing.T) {
	args := mustArgs(t, baseReq(func(r *CliSpawnRequest) {
		r.AllowedTools = []string{"Read", "Edit", "mcp__rimsky-callback__report_complete"}
		r.DisallowedTools = []string{"Bash"}
	}))
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
	args := mustArgs(t, baseReq(func(r *CliSpawnRequest) {
		r.AllowedTools = []string{}
		r.DisallowedTools = []string{}
	}))
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
	args := mustArgs(t, baseReq(func(r *CliSpawnRequest) {
		r.AddDirs = []string{"../specs", "../guidance"}
	}))
	i := slices.Index(args, "--add-dir")
	if i < 0 || args[i+1] != "../specs" || args[i+2] != "../guidance" {
		t.Fatalf("add dirs wrong: %v", args)
	}
}

func TestBuildClaudeCliArgsMaxBudgetPrecedence(t *testing.T) {
	t.Setenv("RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD", "10.00")
	args := mustArgs(t, baseReq(func(r *CliSpawnRequest) { r.MaxBudgetUSD = "0.50" }))
	i := slices.Index(args, "--max-budget-usd")
	if i < 0 || args[i+1] != "0.50" {
		t.Fatalf("expected request budget to win: %v", args)
	}

	args = mustArgs(t, baseReq(nil))
	i = slices.Index(args, "--max-budget-usd")
	if i < 0 || args[i+1] != "10.00" {
		t.Fatalf("expected env fallback: %v", args)
	}

	t.Setenv("RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD", "")
	args = mustArgs(t, baseReq(nil))
	if slices.Contains(args, "--max-budget-usd") {
		t.Fatal("expected no budget flag when neither source set")
	}
}

func TestBuildClaudeCliArgsSessionID(t *testing.T) {
	args := mustArgs(t, baseReq(func(r *CliSpawnRequest) {
		r.SessionID = "550e8400-e29b-41d4-a716-446655440000"
	}))
	i := slices.Index(args, "--session-id")
	if i < 0 || args[i+1] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("session id wrong: %v", args)
	}
	if slices.Contains(mustArgs(t, baseReq(nil)), "--session-id") {
		t.Fatal("expected no --session-id when unset")
	}
}

func TestBuildClaudeCliResumeArgs(t *testing.T) {
	t.Setenv("RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD", "")
	args, err := BuildClaudeCliResumeArgs(CliResumeRequest{
		SessionID: "550e8400-e29b-41d4-a716-446655440000",
		Prompt:    "finish what you started",
		Tools:     []CliToolConfig{{Kind: CliToolKindMcpHTTP, Name: "rimsky-callback", URL: "http://x/mcp"}},
	}, CliArgPaths{McpConfigPath: "/tmp/mcp.json"})
	if err != nil {
		t.Fatal(err)
	}

	mcpIdx := slices.Index(args, "--mcp-config")
	if mcpIdx < 0 || args[mcpIdx+1] != "/tmp/mcp.json" {
		t.Fatalf("resume must carry --mcp-config: %v", args)
	}
	if args[0] != "--resume" || args[1] != "550e8400-e29b-41d4-a716-446655440000" || args[2] != "--print" {
		t.Fatalf("resume prefix wrong: %v", args)
	}
	if slices.Contains(args, "-p") {
		t.Fatalf("resume prompt must not appear on argv (delivered via stdin): %v", args)
	}
	if slices.Contains(args, "--system-prompt-file") {
		t.Fatal("resume must not carry --system-prompt-file")
	}
	aIdx := slices.Index(args, "--allowedTools")
	if aIdx < 0 || args[aIdx+1] != strings.Join(RequiredCallbackTools(), " ") {
		t.Fatalf("resume must carry callback surface: %v", args)
	}
}

func TestBuildClaudeCliResumeArgsMaxBudgetEnvFallback(t *testing.T) {
	t.Setenv("RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD", "10.00")
	args, err := BuildClaudeCliResumeArgs(CliResumeRequest{
		SessionID: "550e8400-e29b-41d4-a716-446655440000",
		Prompt:    "resume",
	}, CliArgPaths{McpConfigPath: "/tmp/mcp.json"})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--max-budget-usd")
	if i < 0 || args[i+1] != "10.00" {
		t.Fatalf("resume without an explicit request budget must fall back to RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD, same as spawn: %v", args)
	}

	t.Setenv("RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD", "")
	args, err = BuildClaudeCliResumeArgs(CliResumeRequest{
		SessionID: "550e8400-e29b-41d4-a716-446655440000",
		Prompt:    "resume",
	}, CliArgPaths{McpConfigPath: "/tmp/mcp.json"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "--max-budget-usd") {
		t.Fatalf("expected no budget flag when neither the request nor the env var is set: %v", args)
	}
}

func TestBuildClaudeCliResumeArgsCarriesRestrictionsAndBudget(t *testing.T) {
	t.Setenv("RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD", "")
	args, err := BuildClaudeCliResumeArgs(CliResumeRequest{
		SessionID:       "550e8400-e29b-41d4-a716-446655440000",
		Prompt:          "resume",
		Model:           "claude-sonnet-4-6",
		PermissionMode:  "acceptEdits",
		Bare:            true,
		DisallowedTools: []string{"Bash"},
		AddDirs:         []string{"../specs"},
		MaxBudgetUSD:    "0.50",
	}, CliArgPaths{McpConfigPath: "/tmp/mcp.json"})
	if err != nil {
		t.Fatal(err)
	}
	dIdx := slices.Index(args, "--disallowedTools")
	if dIdx < 0 || args[dIdx+1] != "Bash" {
		t.Fatalf("resume must carry --disallowedTools (restriction survives resume): %v", args)
	}
	bIdx := slices.Index(args, "--max-budget-usd")
	if bIdx < 0 || args[bIdx+1] != "0.50" {
		t.Fatalf("resume must carry --max-budget-usd (cap survives resume): %v", args)
	}
	if !slices.Contains(args, "--bare") {
		t.Fatalf("resume must carry --bare: %v", args)
	}
	mIdx := slices.Index(args, "--model")
	if mIdx < 0 || args[mIdx+1] != "claude-sonnet-4-6" {
		t.Fatalf("resume must carry --model: %v", args)
	}
	if pIdx := slices.Index(args, "--permission-mode"); pIdx < 0 || args[pIdx+1] != "acceptEdits" {
		t.Fatalf("resume must carry --permission-mode: %v", args)
	}
	if addIdx := slices.Index(args, "--add-dir"); addIdx < 0 || args[addIdx+1] != "../specs" {
		t.Fatalf("resume must carry --add-dir: %v", args)
	}
}

func TestBuildClaudeCliResumeArgsRebindsToNewSessionID(t *testing.T) {
	args, err := BuildClaudeCliResumeArgs(CliResumeRequest{
		SessionID:    "prior-session",
		NewSessionID: "next-session",
		Prompt:       "resume",
	}, CliArgPaths{McpConfigPath: "/tmp/mcp.json"})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--session-id")
	if i < 0 || args[i+1] != "next-session" {
		t.Fatalf("expected --session-id next-session to rebind the resumed conversation: %v", args)
	}
	if slices.Contains(mustResumeArgs(t, CliResumeRequest{SessionID: "s", Prompt: "p"}), "--session-id") {
		t.Fatal("expected no --session-id when NewSessionID unset")
	}
}

func mustResumeArgs(t *testing.T, req CliResumeRequest) []string {
	t.Helper()
	args, err := BuildClaudeCliResumeArgs(req, CliArgPaths{McpConfigPath: "/tmp/mcp.json"})
	if err != nil {
		t.Fatalf("BuildClaudeCliResumeArgs: %v", err)
	}
	return args
}

func TestBuildClaudeCliResumeArgsRejectsFlagLikeAddDir(t *testing.T) {
	if _, err := BuildClaudeCliResumeArgs(CliResumeRequest{
		SessionID: "s",
		Prompt:    "p",
		AddDirs:   []string{"--mcp-config"},
	}, CliArgPaths{McpConfigPath: "/tmp/mcp.json"}); err == nil {
		t.Fatal("expected resume to reject a flag-like add_dir")
	}
}

func TestBuildClaudeCliArgsPromptStaysOffArgv(t *testing.T) {
	t.Setenv("RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD", "5.00")
	args := mustArgs(t, baseReq(func(r *CliSpawnRequest) {
		r.Bare = true
		r.PermissionMode = "acceptEdits"
		r.AllowedTools = []string{"Read"}
		r.DisallowedTools = []string{"Bash"}
		r.AddDirs = []string{"../specs"}
		r.MaxBudgetUSD = "1.00"
	}))
	if args[0] != "--print" {
		t.Fatalf("ordering wrong: %v", args)
	}
	if slices.Contains(args, "-p") || slices.Contains(args, "U") {
		t.Fatalf("user prompt must not appear on argv: %v", args)
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

func TestSpawnSigkillTerminatesWholeProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	binary := writeFakeCli(t, fmt.Sprintf(`(sleep 30 & echo $! > %q); wait`, pidFile))
	runner := NewClaudeCliRunner(CliRunnerOpts{BinaryPath: binary})
	handle, err := runner.Spawn(baseReq(nil))
	if err != nil {
		t.Fatal(err)
	}

	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil && len(data) > 0 {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("grandchild pid was never written")
	}

	handle.SendSigkill()
	handle.WaitExit()

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); err != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("grandchild pid %d is still alive after SendSigkill; SendSigkill must terminate the whole process group, not just the direct child", childPID)
}

func TestSpawnMissingBinaryReturnsError(t *testing.T) {
	runner := NewClaudeCliRunner(CliRunnerOpts{BinaryPath: "/nonexistent/claude-binary"})
	if _, err := runner.Spawn(baseReq(nil)); err == nil {
		t.Fatal("expected spawn error for missing binary")
	}
}

// @story: local-orchestrator-zero-config
func TestSpawnWritesRealSystemPromptAndMcpConfigFilesUnderFreshTempDirForARealSubprocessToRead(t *testing.T) {
	binary := writeFakeCli(t, `
prev=""
for arg in "$@"; do
  if [ "$prev" = "--system-prompt-file" ]; then
    echo "SYSTEM_PROMPT_PATH:$arg"
    echo "SYSTEM_PROMPT_CONTENT:$(cat "$arg")"
  fi
  if [ "$prev" = "--mcp-config" ]; then
    echo "MCP_CONFIG_PATH:$arg"
    echo "MCP_CONFIG_CONTENT:$(cat "$arg")"
  fi
  prev="$arg"
done
exit 0
`)
	runner := NewClaudeCliRunner(CliRunnerOpts{BinaryPath: binary})
	req := baseReq(func(r *CliSpawnRequest) {
		r.SystemPrompt = "REAL-FS-SIDE-EFFECT-SYSTEM-PROMPT-9f3c1"
		r.Tools = []CliToolConfig{
			{Kind: CliToolKindMcpHTTP, Name: "docs", URL: "http://127.0.0.1:9999/mcp"},
		}
	})
	handle, err := runner.Spawn(req)
	if err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	var mu sync.Mutex
	handle.OnStdout(func(chunk string) { mu.Lock(); stdout.WriteString(chunk); mu.Unlock() })
	result := handle.WaitExit()
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exit = %+v, want code 0", result)
	}
	mu.Lock()
	out := stdout.String()
	mu.Unlock()

	if !strings.Contains(out, "SYSTEM_PROMPT_CONTENT:"+req.SystemPrompt) {
		t.Fatalf("the real subprocess did not read back the system-prompt file's real content; stdout=%q", out)
	}
	if !strings.Contains(out, `"docs"`) || !strings.Contains(out, "http://127.0.0.1:9999/mcp") {
		t.Fatalf("the real subprocess did not read back the mcp-config file's real content; stdout=%q", out)
	}

	tmpRoot := os.TempDir()
	for _, marker := range []string{"SYSTEM_PROMPT_PATH:", "MCP_CONFIG_PATH:"} {
		idx := strings.Index(out, marker)
		if idx < 0 {
			t.Fatalf("missing %s marker in stdout=%q", marker, out)
		}
		line := out[idx+len(marker):]
		if nl := strings.IndexByte(line, '\n'); nl >= 0 {
			line = line[:nl]
		}
		path, err := filepath.EvalSymlinks(filepath.Dir(line))
		if err != nil {
			t.Fatalf("resolve %s dir %q: %v", marker, line, err)
		}
		resolvedTmpRoot, err := filepath.EvalSymlinks(tmpRoot)
		if err != nil {
			t.Fatalf("resolve os.TempDir(): %v", err)
		}
		if !strings.HasPrefix(path, resolvedTmpRoot) {
			t.Fatalf("%s %q was not written under a real OS temp dir %q — "+
				"a stub/canned reply would not need to materialize a real, fresh, per-run "+
				"working directory on disk", marker, line, resolvedTmpRoot)
		}
		if !strings.Contains(filepath.Base(filepath.Dir(line)), "rimsky-cli-") {
			t.Fatalf("%s %q is not under a fresh rimsky-cli-* run directory", marker, line)
		}
	}
}

type presetChunkReader struct {
	ch <-chan []byte
}

func (r *presetChunkReader) Read(buf []byte) (int, error) {
	chunk, ok := <-r.ch
	if !ok {
		return 0, io.EOF
	}
	return copy(buf, chunk), nil
}

func TestRegisterStreamConcurrentWithPump_NeverReordersChunks(t *testing.T) {
	const iterations = 300
	const chunks = 50

	for iter := 0; iter < iterations; iter++ {
		h := &realCliHandle{}
		ch := make(chan []byte, chunks)
		for i := 0; i < chunks; i++ {
			ch <- []byte(strconv.Itoa(i))
		}
		close(ch)
		reader := &presetChunkReader{ch: ch}

		var mu sync.Mutex
		var received []int
		record := func(chunk string) {
			n, err := strconv.Atoi(chunk)
			if err != nil {
				t.Errorf("iteration %d: non-numeric chunk %q delivered to callback", iter, chunk)
				return
			}
			mu.Lock()
			received = append(received, n)
			mu.Unlock()
		}

		var pumps sync.WaitGroup
		pumps.Add(2)
		var starters sync.WaitGroup
		starters.Add(2)
		start := make(chan struct{})
		go func() {
			starters.Done()
			<-start
			h.pump(reader, &h.stdoutCbs, &h.stdoutHist, &pumps)
		}()
		go func() {
			defer pumps.Done()
			starters.Done()
			<-start
			h.registerStream(&h.stdoutCbs, &h.stdoutHist, record)
		}()
		starters.Wait()
		close(start)
		pumps.Wait()

		mu.Lock()
		got := append([]int{}, received...)
		mu.Unlock()
		if len(got) != chunks {
			t.Fatalf("iteration %d: delivered %d chunks, want %d (no chunk may be lost or duplicated): %v",
				iter, len(got), chunks, got)
		}
		for i := 1; i < len(got); i++ {
			if got[i] <= got[i-1] {
				t.Fatalf("iteration %d: chunks delivered out of order: %v", iter, got)
			}
		}
	}
}
