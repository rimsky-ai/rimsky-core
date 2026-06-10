// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// agent.go — `rimsky agent` dispatcher. Wraps the importable
// runtime/hostagent daemon as a CLI subcommand: `start` runs (or daemonizes)
// the host-agent main loop, `status` checks the pid-file for a live daemon
// AND reads the daemon's connection sentinel so it reports the actual
// bidi-stream state (not just pid liveness), and `stop` SIGTERMs it.
// The daemon itself lives in runtime/hostagent so it can also be run as the
// standalone cmd/rimsky-host-agent binary.
//
// @concept: host-agent
// @story: host-agent-control-plane — `start` performs a synchronous
// readiness handshake (poll-waits the daemon's status sentinel) so a
// misconfigured `--proxy` URL surfaces as a non-zero exit with a clear
// diagnostic rather than a silent fork + cache. `status` reports
// `connected` only when the status sentinel says the live stream is up.
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

// agentReadinessTimeout bounds how long `rimsky agent start` waits for the
// forked daemon to establish its proxy connection (write a `connected:true`
// sentinel) before declaring startup a failure and SIGKILLing the child.
// Picked to comfortably cover a real Connect+RegisterAck roundtrip while
// still surfacing a misconfigured URL within a developer-tolerable wait.
//
// @story: host-agent-control-plane — falsifier "start silently succeeds
// with a misconfigured proxy URL" is defeated by this synchronous wait.
const agentReadinessTimeout = 10 * time.Second

// agentReadinessPollInterval is the cadence the parent re-checks the
// daemon's pid liveness + status sentinel during the readiness window.
const agentReadinessPollInterval = 100 * time.Millisecond

// RunAgent dispatches `rimsky agent <subcommand> ...`.
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

// runAgentStart parses flags and either runs the daemon in the foreground or
// daemonizes by forking self with --foreground and writing the pid file.
func runAgentStart(args []string) int {
	fs := flag.NewFlagSet("agent start", flag.ContinueOnError)
	allowPaths := fs.String("allow-paths", "", "comma-separated glob patterns for binary path validation")
	listen := fs.String("listen", "", "agent local listener addr (default 127.0.0.1:0)")
	proxy := fs.String("proxy", "", "host-agent-proxy endpoint host:port (overrides $RIMSKY_URL)")
	apiKey := fs.String("api-key", "", "api-key presented to the proxy on Register (overrides $RIMSKY_API_KEY)")
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

	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	statusPath := filepath.Join(dir, "agent.status")
	cfg.StatusFile = statusPath

	if cfg.RimskyURL == "" {
		// The hostagent daemon would error on this too, but catching it at
		// the CLI gate gives the operator a one-line diagnostic instead of
		// a daemonize-then-fail sequence.
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

// daemonizeAgent forks the current executable with --foreground, records its
// pid in the pid file, and poll-waits the daemon's status sentinel until
// either the bidi stream is `connected:true` (success) or the readiness
// window elapses / the child exits (failure → SIGKILL + diagnostic).
//
// The synchronous handshake is what makes `start` REFUSE on a misconfigured
// proxy URL rather than silently fork a daemon that loops on dial-failures.
// @story: host-agent-control-plane.
func daemonizeAgent(startArgs []string, stateDir, statusPath, proxy string) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// Make sure the state dir is writable + clear any stale status file
	// from a previous run so the readiness poll doesn't read a phantom
	// `connected:true` left behind by an unclean exit.
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if rmErr := os.Remove(statusPath); rmErr != nil && !os.IsNotExist(rmErr) {
		fmt.Fprintln(os.Stderr, rmErr)
		return 1
	}

	// Forward state-dir/proxy/api-key to the foreground child so the
	// daemon writes its sentinel to the same file the parent polls and
	// dials the same proxy the parent vetted. The original argv may or
	// may not have carried these flags; pass them through explicitly.
	forkArgs := append([]string{"agent", "start", "--foreground", "--state-dir", stateDir}, startArgs...)
	cmd := exec.Command(self, forkArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// Snapshot the child's pid BEFORE calling Process.Release. Go's
	// os.Process.Release zeroes out the underlying handle and sets Pid
	// to -1 to signal "no longer tracked" — which would silently break
	// every downstream processAlive / syscall.Kill probe in this routine.
	childPid := cmd.Process.Pid

	pidPath := filepath.Join(stateDir, "agent.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(childPid)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// Release the child to the OS so its eventual exit isn't held in our
	// process table — we manage liveness via pid probe + status file.
	if err := cmd.Process.Release(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// Poll-wait for connection. The success criterion is a status file
	// whose `connected` is true; failure is the child exiting (no live
	// process) OR the readiness window elapsing without success.
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

	// Readiness window blew. The daemon is alive but never connected —
	// SIGKILL it so a misconfigured `--proxy` doesn't leave a background
	// process loop-dialing forever, and remove the pid file so a follow-up
	// `agent status` doesn't claim it's "running".
	_ = syscall.Kill(childPid, syscall.SIGKILL)
	_ = os.Remove(pidPath)
	fmt.Fprintf(os.Stderr,
		"rimsky agent: daemon did not connect to proxy %q within %s; killed (proxy unreachable or misconfigured)\n",
		proxy, agentReadinessTimeout)
	return 1
}

// runAgentStatus reports whether the recorded agent daemon is alive AND
// whether its bidi stream is currently up. The connection state comes from
// the daemon's status sentinel — a pid being alive proves the process is
// running, but the falsifier "status reports `connected` when the bidi
// stream is actually down" demands the report track the LIVE stream.
//
// @story: host-agent-control-plane.
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
		// Pid is alive but we couldn't read the sentinel — surface it as
		// "running but status unknown" rather than fabricating "connected".
		fmt.Fprintf(os.Stdout, "rimsky agent: running (pid %d, status unreadable: %v)\n", pid, readErr)
		return 0
	}
	if present && snap.Connected {
		fmt.Fprintf(os.Stdout, "rimsky agent: connected (pid %d, proxy %s, since %s)\n",
			pid, snap.Proxy, snap.Since)
		return 0
	}
	// Pid alive but the sentinel is absent → the bidi stream is down (the
	// daemon is dialing/backoff-sleeping). DO NOT report "connected".
	fmt.Fprintf(os.Stdout, "rimsky agent: running, disconnected (pid %d)\n", pid)
	return 0
}

// runAgentStop SIGTERMs the recorded daemon, waits briefly for exit, and
// removes the pid file.
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
		if termErr := syscall.Kill(pid, syscall.SIGTERM); termErr != nil {
			fmt.Fprintf(os.Stderr, "rimsky agent: signal pid %d: %v\n", pid, termErr)
			return 1
		}
		// Give the daemon time to run its reap loop. ReapGracePeriod
		// defaults to 30s; bound the wait at 35s to cover it plus the
		// gRPC stream teardown.
		waitForExit(pid, 35*time.Second)
	}

	pidPath := filepath.Join(dir, "agent.pid")
	if rmErr := os.Remove(pidPath); rmErr != nil && !os.IsNotExist(rmErr) {
		fmt.Fprintln(os.Stderr, rmErr)
		return 1
	}
	// The daemon removes its own status file on shutdown, but if it crashed
	// without doing so, clear it here so a subsequent `status` doesn't see
	// a phantom `connected:true`.
	_ = os.Remove(filepath.Join(dir, "agent.status"))
	fmt.Fprintf(os.Stdout, "rimsky agent: stopped (pid %d)\n", pid)
	return 0
}

// resolveStateDir returns the explicit dir flag value if non-empty,
// otherwise ~/.rimsky.
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

// readAgentPID reads the pid from the default state dir (~/.rimsky).
// Retained for callers that don't take a state-dir flag (e.g. the `run`
// verb's ensureAgentRunning probe).
func readAgentPID() (int, bool, error) {
	dir, err := resolveStateDir("")
	if err != nil {
		return 0, false, err
	}
	return readAgentPIDFrom(dir)
}

// readAgentPIDFrom reads the pid from <dir>/agent.pid. ok=false when the
// file is absent or empty.
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

// statusSnapshot mirrors hostagent.statusSnapshot at the CLI boundary.
// Duplicated rather than importing hostagent's internal shape so the CLI
// owns its parse layer and a hostagent refactor doesn't trip the CLI.
//
// @source: lib/runtime/hostagent/run.go::statusSnapshot
type statusSnapshot struct {
	Connected bool   `json:"connected"`
	Proxy     string `json:"proxy"`
	Since     string `json:"since"`
}

// readStatusFile loads the daemon's connection sentinel from path. The
// return is (snapshot, present, error): present=false when the file does
// not exist (the daemon either hasn't connected yet or is disconnected),
// error when the file exists but is unreadable or unparseable.
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

// processAlive reports whether pid names a live process (signal 0 probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// On unix, signal 0 performs error checking without delivering a signal.
	return syscall.Kill(pid, 0) == nil
}

// waitForExit polls until pid is gone or the deadline elapses.
func waitForExit(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// splitNonEmpty splits s on sep, trimming spaces and dropping empty fields.
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
