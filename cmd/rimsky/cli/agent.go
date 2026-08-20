// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent
// @story: host-agent-control-plane
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// @story: host-agent-control-plane
const agentReadinessTimeout = 10 * time.Second

const agentReadinessPollInterval = 100 * time.Millisecond

var agentStopGraceTimeout = 35 * time.Second

var agentStopKillTimeout = 5 * time.Second

var agentSelfExecutable = os.Executable

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

type agentStartFlags struct {
	allowPaths   string
	listen       string
	proxy        string
	apiKey       string
	tls          bool
	tlsCA        string
	label        string
	identityFile string
}

func applyAgentStartFlags(cfg hostagent.Config, f agentStartFlags) hostagent.Config {
	if f.allowPaths != "" {
		cfg.AllowPaths = splitNonEmpty(f.allowPaths, ",")
	}
	if f.listen != "" {
		cfg.ListenAddr = f.listen
	}
	if f.proxy != "" {
		cfg.ProxyURL = f.proxy
	}
	if f.apiKey != "" {
		cfg.APIKey = f.apiKey
	}
	if f.tls {
		cfg.TLSEnabled = true
	}
	if f.tlsCA != "" {
		cfg.TLSCAPath = f.tlsCA
	}
	if f.label != "" {
		cfg.RoutingLabel = f.label
	}
	if f.identityFile != "" {
		cfg.IdentityFile = f.identityFile
	}
	return cfg
}

func runAgentStart(args []string) int {
	fs := flag.NewFlagSet("agent start", flag.ContinueOnError)
	allowPaths := fs.String("allow-paths", "", "comma-separated glob patterns for binary path validation")
	listen := fs.String("listen", "", "agent local listener addr (default 127.0.0.1:0)")
	proxy := fs.String("proxy", "", "host-agent-proxy endpoint host:port (overrides $RIMSKY_HOST_AGENT_PROXY_URL)")
	apiKey := fs.String("api-key", "", "api-key plaintext presented to the proxy on Register (overrides $RIMSKY_API_KEY); omit to register anonymously")
	tls := fs.Bool("tls", false, "dial the proxy over TLS, verifying its server cert against --tls-ca (overrides $RIMSKY_AGENT_TLS)")
	tlsCA := fs.String("tls-ca", "", "path to the pinned deployment CA root PEM used to verify the proxy server cert (overrides $RIMSKY_AGENT_TLS_CA)")
	stateDir := fs.String("state-dir", "", "directory for pid and status files (default ~/.rimsky)")
	foreground := fs.Bool("foreground", false, "run in foreground (don't daemonize)")
	label := fs.String("label", "", "anonymous routing label (silly-name) the agent asks the proxy to adopt; only meaningful in anonymous mode (no --api-key)")
	identityFile := fs.String("identity-file", "", "path to the anonymous identity JSON file the agent reads/writes (default $XDG_CONFIG_HOME/rimsky/host-agent/identity.json)")
	if err := parseInterspersed(fs, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	cfg, err := hostagent.LoadConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cfg = applyAgentStartFlags(cfg, agentStartFlags{
		allowPaths:   *allowPaths,
		listen:       *listen,
		proxy:        *proxy,
		apiKey:       *apiKey,
		tls:          *tls,
		tlsCA:        *tlsCA,
		label:        *label,
		identityFile: *identityFile,
	})

	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	statusPath := filepath.Join(dir, "agent.status")
	cfg.StatusFile = statusPath

	if cfg.ProxyURL == "" {
		fmt.Fprintln(os.Stderr, "rimsky agent: --proxy is required (or set RIMSKY_HOST_AGENT_PROXY_URL)")
		return 2
	}

	if *foreground {
		logger := serverkit.NewJSONLoggerForLevel(cfg.LogLevel)
		slog.SetDefault(logger)

		// @decision: graceful-shutdown
		ctx, stopSignals := serverkit.ShutdownContext(context.Background(), logger)
		defer stopSignals()
		if err := hostagent.Run(ctx, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	return daemonizeAgent(args, dir, statusPath, cfg.ProxyURL)
}

// @story: host-agent-control-plane
func daemonizeAgent(startArgs []string, stateDir, statusPath, proxy string) int {
	self, err := agentSelfExecutable()
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

	logPath := filepath.Join(stateDir, "agent.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	forkArgs := append([]string{"agent", "start", "--foreground", "--state-dir", stateDir}, startArgs...)
	cmd := exec.Command(self, forkArgs...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = logFile.Close()

	childPid := cmd.Process.Pid

	pidPath := filepath.Join(stateDir, "agent.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(childPid)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(agentReadinessTimeout)
	for time.Now().Before(deadline) {
		snap, ok, readErr := readStatusFile(statusPath)
		if readErr == nil && ok && snap.Connected {
			fmt.Fprintf(os.Stdout, "rimsky agent started (pid %d, connected to %s)\n", childPid, snap.Proxy)
			return 0
		}
		if !processAlive(childPid) {
			_ = os.Remove(pidPath)
			fmt.Fprintf(os.Stderr, "rimsky agent: daemon exited during startup (proxy %q unreachable or misconfigured); see %s for details\n", proxy, logPath)
			return 1
		}
		time.Sleep(agentReadinessPollInterval)
	}

	killProcess(childPid)
	_ = os.Remove(pidPath)
	fmt.Fprintf(os.Stderr,
		"rimsky agent: daemon did not connect to proxy %q within %s; killed (proxy unreachable or misconfigured); see %s for details\n",
		proxy, agentReadinessTimeout, logPath)
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
func printAgentChildren(children []hostagent.ChildStatus) {
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

func readStatusFile(path string) (hostagent.StatusSnapshot, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hostagent.StatusSnapshot{}, false, nil
	}
	if err != nil {
		return hostagent.StatusSnapshot{}, false, err
	}
	var snap hostagent.StatusSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return hostagent.StatusSnapshot{}, false, fmt.Errorf("agent status file: %w", err)
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
