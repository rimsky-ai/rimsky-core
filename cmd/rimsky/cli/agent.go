// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
// @story: host-agent-control-plane
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// @story: host-agent-control-plane
const agentReadinessTimeout = 10 * time.Second

const agentReadinessPollInterval = 100 * time.Millisecond

var agentStopGraceTimeout = 35 * time.Second

var agentStopKillTimeout = 5 * time.Second

func RunAgent(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rimsky agent {start|status|stop}")
		return 2
	}
	switch args[0] {
	case "start":
		return runAgentStart(args[1:])
	case "status":
		return runAgentStatus(args[1:])
	case "stop":
		return runAgentStop(args[1:])
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky agent {start|status|stop}")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "rimsky agent: unknown subcommand %q\n", args[0])
		return 2
	}
}

func runAgentStart(args []string) int {
	fs := flag.NewFlagSet("agent start", flag.ContinueOnError)
	allowPaths := fs.String("allow-paths", "", "comma-separated glob patterns for binary path validation")
	listen := fs.String("listen", "", "agent local listener addr (default 127.0.0.1:0)")
	proxy := fs.String("proxy", "", "host-agent-proxy endpoint host:port (overrides $RIMSKY_URL)")
	apiKey := fs.String("api-key", "", "api-key plaintext presented to the proxy on Register (overrides $RIMSKY_API_KEY); omit to register anonymously")
	tls := fs.Bool("tls", false, "dial the proxy over TLS, verifying its server cert against --tls-ca (overrides $RIMSKY_AGENT_TLS)")
	tlsCA := fs.String("tls-ca", "", "path to the pinned deployment CA root PEM used to verify the proxy server cert (overrides $RIMSKY_AGENT_TLS_CA)")
	stateDir := fs.String("state-dir", "", "directory for pid and status files (default ~/.rimsky)")
	foreground := fs.Bool("foreground", false, "run in foreground (don't daemonize)")
	if err := parseInterspersed(fs, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	cfg := hostagent.LoadConfigFromEnv()
	if *allowPaths != "" {
		cfg.AllowPaths = splitNonEmpty(*allowPaths, ",")
	}
	if *listen != "" {
		cfg.ListenAddr = *listen
	}
	if *proxy != "" {
		cfg.RimskyURL = *proxy
	}
	if *apiKey != "" {
		cfg.APIKey = *apiKey
	}
	if *tls {
		cfg.TLSEnabled = true
	}
	if *tlsCA != "" {
		cfg.TLSCAPath = *tlsCA
	}

	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	statusPath := filepath.Join(dir, "agent.status")
	cfg.StatusFile = statusPath

	if cfg.RimskyURL == "" {
		fmt.Fprintln(os.Stderr, "rimsky agent: --proxy is required (or set RIMSKY_URL)")
		return 2
	}

	if *foreground {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		if err := hostagent.Run(ctx, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	return daemonizeAgent(args, dir, statusPath, cfg.RimskyURL)
}

// @story: host-agent-control-plane
func daemonizeAgent(startArgs []string, stateDir, statusPath, proxy string) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if rmErr := os.Remove(statusPath); rmErr != nil && !os.IsNotExist(rmErr) {
		fmt.Fprintln(os.Stderr, rmErr)
		return 1
	}

	forkArgs := append([]string{"agent", "start", "--foreground", "--state-dir", stateDir}, startArgs...)
	cmd := exec.Command(self, forkArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	childPid := cmd.Process.Pid

	pidPath := filepath.Join(stateDir, "agent.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(childPid)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := cmd.Process.Release(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	deadline := time.Now().Add(agentReadinessTimeout)
	for time.Now().Before(deadline) {
		snap, ok, readErr := readStatusFile(statusPath)
		if readErr == nil && ok && snap.Connected {
			fmt.Fprintf(os.Stdout, "rimsky agent started (pid %d, connected to %s)\n", childPid, snap.Proxy)
			return 0
		}
		if !processAlive(childPid) {
			_ = os.Remove(pidPath)
			fmt.Fprintf(os.Stderr, "rimsky agent: daemon exited during startup (proxy %q unreachable or misconfigured)\n", proxy)
			return 1
		}
		time.Sleep(agentReadinessPollInterval)
	}

	killProcess(childPid)
	_ = os.Remove(pidPath)
	fmt.Fprintf(os.Stderr,
		"rimsky agent: daemon did not connect to proxy %q within %s; killed (proxy unreachable or misconfigured)\n",
		proxy, agentReadinessTimeout)
	return 1
}

// @story: host-agent-control-plane
func runAgentStatus(args []string) int {
	fs := flag.NewFlagSet("agent status", flag.ContinueOnError)
	stateDir := fs.String("state-dir", "", "directory for pid and status files (default ~/.rimsky)")
	if err := parseInterspersed(fs, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	pid, ok, err := readAgentPIDFrom(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "rimsky agent: not running")
		return 0
	}
	if !processAlive(pid) {
		fmt.Fprintf(os.Stdout, "rimsky agent: not running (stale pid %d)\n", pid)
		return 0
	}

	statusPath := filepath.Join(dir, "agent.status")
	snap, present, readErr := readStatusFile(statusPath)
	if readErr != nil {
		fmt.Fprintf(os.Stdout, "rimsky agent: running (pid %d, status unreadable: %v)\n", pid, readErr)
		return 0
	}
	if present && snap.Connected {
		fmt.Fprintf(os.Stdout, "rimsky agent: connected (pid %d, proxy %s, since %s)\n",
			pid, snap.Proxy, snap.Since)
		printAgentChildren(snap.Children)
		return 0
	}
	fmt.Fprintf(os.Stdout, "rimsky agent: running, disconnected (pid %d)\n", pid)
	return 0
}

// @story: host-agent-control-plane
func printAgentChildren(children []childSnapshot) {
	if len(children) == 0 {
		fmt.Fprintln(os.Stdout, "  spawned children: none")
		return
	}
	fmt.Fprintln(os.Stdout, "  spawned children:")
	for _, c := range children {
		fmt.Fprintf(os.Stdout, "    run-scope=%s binding=%s spawn-id=%s\n", c.RunScopeID, c.Binding, c.SpawnID)
	}
}

func runAgentStop(args []string) int {
	fs := flag.NewFlagSet("agent stop", flag.ContinueOnError)
	stateDir := fs.String("state-dir", "", "directory for pid and status files (default ~/.rimsky)")
	if err := parseInterspersed(fs, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	pid, ok, err := readAgentPIDFrom(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "rimsky agent: not running")
		return 0
	}

	if processAlive(pid) {
		if termErr := terminateProcess(pid); termErr != nil {
			fmt.Fprintf(os.Stderr, "rimsky agent: signal pid %d: %v\n", pid, termErr)
			return 1
		}
		waitForExit(pid, agentStopGraceTimeout)
		if processAlive(pid) {
			killProcess(pid)
			waitForExit(pid, agentStopKillTimeout)
		}
		if processAlive(pid) {
			fmt.Fprintf(os.Stderr, "rimsky agent: pid %d did not exit after SIGTERM and SIGKILL\n", pid)
			return 1
		}
	}

	pidPath := filepath.Join(dir, "agent.pid")
	if rmErr := os.Remove(pidPath); rmErr != nil && !os.IsNotExist(rmErr) {
		fmt.Fprintln(os.Stderr, rmErr)
		return 1
	}
	_ = os.Remove(filepath.Join(dir, "agent.status"))
	fmt.Fprintf(os.Stdout, "rimsky agent: stopped (pid %d)\n", pid)
	return 0
}

func resolveStateDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rimsky"), nil
}

func readAgentPID() (int, bool, error) {
	dir, err := resolveStateDir("")
	if err != nil {
		return 0, false, err
	}
	return readAgentPIDFrom(dir)
}

func readAgentPIDFrom(dir string) (int, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "agent.pid"))
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, false, nil
	}
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, false, fmt.Errorf("agent pid file: %w", err)
	}
	return pid, true, nil
}

type statusSnapshot struct {
	Connected bool            `json:"connected"`
	Proxy     string          `json:"proxy"`
	Since     string          `json:"since"`
	Children  []childSnapshot `json:"children"`
}

// @story: host-agent-control-plane
type childSnapshot struct {
	SpawnID    string `json:"spawn_id"`
	RunScopeID string `json:"run_scope_id"`
	Binding    string `json:"binding"`
}

func readStatusFile(path string) (statusSnapshot, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return statusSnapshot{}, false, nil
	}
	if err != nil {
		return statusSnapshot{}, false, err
	}
	var snap statusSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return statusSnapshot{}, false, fmt.Errorf("agent status file: %w", err)
	}
	return snap, true, nil
}

func waitForExit(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func splitNonEmpty(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
