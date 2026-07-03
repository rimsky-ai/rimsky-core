// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
)

func RequiredCallbackTools() []string {
	defs := toolDefinitions()
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, "mcp__"+CallbackMCPServerName+"__"+d.Name)
	}
	return out
}

func BuildAllowedTools(templateAllowed []string) []string {
	merged := append(RequiredCallbackTools(), templateAllowed...)
	seen := make(map[string]bool, len(merged))
	out := make([]string, 0, len(merged))
	for _, t := range merged {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

const (
	CliToolKindMcpHTTP  = "mcp-http"
	CliToolKindMcpStdio = "mcp-stdio"
)

type CliToolConfig struct {
	Kind    string
	Name    string
	URL     string
	Headers map[string]string
	Command string
	Args    []string
	Env     map[string]string
}

type CliSpawnRequest struct {
	Model           string
	SystemPrompt    string
	UserPrompt      string
	Tools           []CliToolConfig
	Env             map[string]string
	ExposeEnvNames  []string
	Cwd             string
	SessionID       string
	Bare            bool
	PermissionMode  string
	AllowedTools    []string
	DisallowedTools []string
	AddDirs         []string
	MaxBudgetUSD    string
}

type CliResumeRequest struct {
	SessionID      string
	Prompt         string
	Tools          []CliToolConfig
	AllowedTools   []string
	Env            map[string]string
	ExposeEnvNames []string
	Cwd            string
}

type ExitResult struct {
	ExitCode *int
	Signal   string
}

type CliHandle interface {
	Pid() int
	OnStdout(cb func(chunk string))
	OnStderr(cb func(chunk string))
	OnExit(cb func(result ExitResult))
	SendSigterm()
	SendSigkill()
	WaitExit() ExitResult
}

type CliRunner interface {
	Spawn(req CliSpawnRequest) (CliHandle, error)
	Resume(req CliResumeRequest) (CliHandle, error)
}

type CliArgPaths struct {
	SystemPromptPath string
	McpConfigPath    string
}

func BuildClaudeCliArgs(req CliSpawnRequest, paths CliArgPaths) []string {
	permissionMode := req.PermissionMode
	if permissionMode == "" {
		permissionMode = "bypassPermissions"
	}
	maxBudgetUSD := req.MaxBudgetUSD
	if maxBudgetUSD == "" {
		maxBudgetUSD = os.Getenv("RIMSKY_DISPATCH_MAX_USD")
	}
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", req.Model,
		"--permission-mode", permissionMode,
	}
	if req.SessionID != "" {
		args = append(args, "--session-id", req.SessionID)
	}
	if req.Bare {
		args = append(args, "--bare")
	}
	args = append(args, "--allowedTools", joinTools(BuildAllowedTools(req.AllowedTools)))
	if len(req.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", joinTools(req.DisallowedTools))
	}
	if len(req.AddDirs) > 0 {
		args = append(args, "--add-dir")
		args = append(args, req.AddDirs...)
	}
	args = append(args,
		"--system-prompt-file", paths.SystemPromptPath,
		"--mcp-config", paths.McpConfigPath,
	)
	if maxBudgetUSD != "" {
		args = append(args, "--max-budget-usd", maxBudgetUSD)
	}
	args = append(args, "-p", req.UserPrompt)
	return args
}

func BuildClaudeCliResumeArgs(req CliResumeRequest, paths CliArgPaths) []string {
	return []string{
		"--resume", req.SessionID,
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", joinTools(BuildAllowedTools(req.AllowedTools)),
		"--mcp-config", paths.McpConfigPath,
		"-p", req.Prompt,
	}
}

func joinTools(tools []string) string {
	out := ""
	for i, t := range tools {
		if i > 0 {
			out += " "
		}
		out += t
	}
	return out
}

func mcpConfigJSON(tools []CliToolConfig) ([]byte, error) {
	mcpServers := map[string]any{}
	for _, t := range tools {
		switch t.Kind {
		case CliToolKindMcpHTTP:
			headers := t.Headers
			if headers == nil {
				headers = map[string]string{}
			}
			mcpServers[t.Name] = map[string]any{
				"type":    "http",
				"url":     t.URL,
				"headers": headers,
			}
		case CliToolKindMcpStdio:
			entry := map[string]any{
				"type":    "stdio",
				"command": t.Command,
			}
			if len(t.Args) > 0 {
				entry["args"] = t.Args
			}
			if t.Env != nil {
				entry["env"] = t.Env
			}
			mcpServers[t.Name] = entry
		}
	}
	return json.Marshal(map[string]any{"mcpServers": mcpServers})
}

type CliRunnerOpts struct {
	Auth       CliAuthConfig
	BinaryPath string
}

type claudeCliRunner struct {
	auth   CliAuthConfig
	binary string
}

// @decision: cli-spawn-mechanism
func NewClaudeCliRunner(opts CliRunnerOpts) CliRunner {
	binary := opts.BinaryPath
	if binary == "" {
		binary = "claude"
	}
	return &claudeCliRunner{auth: opts.Auth, binary: binary}
}

func collectExposedEnv(names []string) map[string]string {
	out := map[string]string{}
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			out[name] = v
		}
	}
	return out
}

func (r *claudeCliRunner) Spawn(req CliSpawnRequest) (CliHandle, error) {
	tmp, err := os.MkdirTemp("", "rimsky-cli-")
	if err != nil {
		return nil, err
	}
	systemPromptPath := filepath.Join(tmp, "system.md")
	mcpConfigPath := filepath.Join(tmp, "mcp.json")
	if err := os.WriteFile(systemPromptPath, []byte(req.SystemPrompt), 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	mcpJSON, err := mcpConfigJSON(req.Tools)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if err := os.WriteFile(mcpConfigPath, mcpJSON, 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	authEnv, err := BuildCliEnv(r.auth)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	args := BuildClaudeCliArgs(req, CliArgPaths{
		SystemPromptPath: systemPromptPath,
		McpConfigPath:    mcpConfigPath,
	})
	cleanup := func() {
		_ = os.RemoveAll(tmp)
		authEnv.Cleanup()
	}
	return spawnChild(r.binary, args, req.Cwd, mergeEnv(collectExposedEnv(req.ExposeEnvNames), authEnv.Env, req.Env), cleanup)
}

func (r *claudeCliRunner) Resume(req CliResumeRequest) (CliHandle, error) {
	tmp, err := os.MkdirTemp("", "rimsky-cli-")
	if err != nil {
		return nil, err
	}
	mcpConfigPath := filepath.Join(tmp, "mcp.json")
	mcpJSON, err := mcpConfigJSON(req.Tools)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if err := os.WriteFile(mcpConfigPath, mcpJSON, 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	authEnv, err := BuildCliEnv(r.auth)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	args := BuildClaudeCliResumeArgs(req, CliArgPaths{McpConfigPath: mcpConfigPath})
	cleanup := func() {
		_ = os.RemoveAll(tmp)
		authEnv.Cleanup()
	}
	return spawnChild(r.binary, args, req.Cwd, mergeEnv(collectExposedEnv(req.ExposeEnvNames), authEnv.Env, req.Env), cleanup)
}

func mergeEnv(layers ...map[string]string) []string {
	merged := map[string]string{}
	for _, layer := range layers {
		for k, v := range layer {
			merged[k] = v
		}
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

type realCliHandle struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdoutCbs  []func(string)
	stderrCbs  []func(string)
	exitCbs    []func(ExitResult)
	stdoutHist []string
	stderrHist []string
	exited     bool
	exitResult ExitResult
	done       chan struct{}
}

func spawnChild(binary string, args []string, cwd string, env []string, cleanup func()) (CliHandle, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, err
	}

	h := &realCliHandle{cmd: cmd, done: make(chan struct{})}
	var pumps sync.WaitGroup
	pumps.Add(2)
	go h.pump(stdout, &h.stdoutCbs, &h.stdoutHist, &pumps)
	go h.pump(stderr, &h.stderrCbs, &h.stderrHist, &pumps)
	go func() {
		pumps.Wait()
		err := cmd.Wait()
		result := exitResultFrom(cmd, err)
		h.mu.Lock()
		h.exited = true
		h.exitResult = result
		cbs := append([]func(ExitResult){}, h.exitCbs...)
		h.mu.Unlock()
		close(h.done)
		for _, cb := range cbs {
			cb(result)
		}
		cleanup()
	}()
	return h, nil
}

func exitResultFrom(cmd *exec.Cmd, waitErr error) ExitResult {
	state := cmd.ProcessState
	if state == nil {
		return ExitResult{}
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ExitResult{Signal: ws.Signal().String()}
	}
	code := state.ExitCode()
	if code < 0 && waitErr != nil {
		return ExitResult{}
	}
	return ExitResult{ExitCode: &code}
}

func (h *realCliHandle) pump(r io.Reader, cbs *[]func(string), hist *[]string, pumps *sync.WaitGroup) {
	defer pumps.Done()
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			h.mu.Lock()
			registered := append([]func(string){}, *cbs...)
			if len(registered) == 0 {
				*hist = append(*hist, chunk)
			}
			h.mu.Unlock()
			for _, cb := range registered {
				cb(chunk)
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *realCliHandle) Pid() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *realCliHandle) OnStdout(cb func(string)) {
	h.registerStream(&h.stdoutCbs, &h.stdoutHist, cb)
}

func (h *realCliHandle) OnStderr(cb func(string)) {
	h.registerStream(&h.stderrCbs, &h.stderrHist, cb)
}

func (h *realCliHandle) registerStream(cbs *[]func(string), hist *[]string, cb func(string)) {
	h.mu.Lock()
	replay := append([]string{}, *hist...)
	*hist = nil
	*cbs = append(*cbs, cb)
	h.mu.Unlock()
	for _, chunk := range replay {
		cb(chunk)
	}
}

func (h *realCliHandle) OnExit(cb func(ExitResult)) {
	h.mu.Lock()
	if h.exited {
		result := h.exitResult
		h.mu.Unlock()
		cb(result)
		return
	}
	h.exitCbs = append(h.exitCbs, cb)
	h.mu.Unlock()
}

func (h *realCliHandle) SendSigterm() {
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Signal(syscall.SIGTERM)
	}
}

func (h *realCliHandle) SendSigkill() {
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
}

func (h *realCliHandle) WaitExit() ExitResult {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exitResult
}
