// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon
// @story: host-daemon-control-plane
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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
)

// @story: host-daemon-control-plane
const daemonReadinessTimeout = 10 * time.Second

const daemonReadinessPollInterval = 100 * time.Millisecond

const daemonStopGraceTimeout = 35 * time.Second

const daemonStopKillTimeout = 5 * time.Second

const daemonExitPollInterval = 50 * time.Millisecond

// @story: host-daemon-control-plane
type daemonProcessControl struct {
	alive         func(pid int) bool
	terminate     func(pid int) error
	kill          func(pid int)
	clock         shared.Clock
	graceTimeout  time.Duration
	sigkillWindow time.Duration
}

func systemDaemonProcessControl() daemonProcessControl {
	return daemonProcessControl{
		alive:         processAlive,
		terminate:     terminateProcess,
		kill:          killProcess,
		clock:         shared.SystemClock{},
		graceTimeout:  daemonStopGraceTimeout,
		sigkillWindow: daemonStopKillTimeout,
	}
}

func RunDaemon(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rimsky daemon {start|status|stop}")
		return 2
	}
	switch args[0] {
	case "start":
		return runDaemonStart(args[1:])
	case "status":
		return runDaemonStatus(args[1:])
	case "stop":
		return runDaemonStop(args[1:])
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky daemon {start|status|stop}")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "rimsky daemon: unknown subcommand %q\n", args[0])
		return 2
	}
}

type daemonStartFlags struct {
	allowPaths   string
	listen       string
	proxy        string
	apiKey       string
	insecure     bool
	tlsCA        string
	label        string
	identityFile string
}

func applyDaemonStartFlags(cfg hostdaemon.Config, f daemonStartFlags) hostdaemon.Config {
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
	// @decision: host-daemon-proxy-tls
	if f.insecure {
		cfg.Insecure = true
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

func runDaemonStart(args []string) int {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	SetUsage(fs, UsageLine("daemon start", "--proxy <host:port> [flags]"))
	allowPaths := fs.String("allow-paths", "", "comma-separated glob patterns for binary path validation")
	listen := fs.String("listen", "", "daemon local listener addr (default 127.0.0.1:0)")
	proxy := fs.String("proxy", "", "host-daemon-proxy endpoint host:port (overrides $RIMSKY_HOST_DAEMON_PROXY_URL)")
	apiKey := fs.String("api-key", "", "api-key plaintext presented to the proxy on Register (overrides $RIMSKY_API_KEY); omit to register anonymously")
	insecureHop := fs.Bool("insecure", false, "dial the proxy in plaintext instead of TLS; the proxy must run with the same switch (overrides $RIMSKY_HOST_DAEMON_INSECURE)")
	tlsCA := fs.String("tls-ca", "", "path to the pinned CA root PEM used to verify the proxy server cert (overrides $RIMSKY_DAEMON_TLS_CA)")
	stateDir := fs.String("state-dir", "", "directory for pid and status files (default ~/.rimsky)")
	foreground := fs.Bool("foreground", false, "run in foreground (don't daemonize)")
	label := fs.String("label", "", "anonymous routing label (silly-name) the daemon asks the proxy to adopt; only meaningful in anonymous mode (no --api-key)")
	identityFile := fs.String("identity-file", "", "path to the anonymous identity JSON file the daemon reads/writes (default $XDG_CONFIG_HOME/rimsky/host-daemon/identity.json)")
	if code, done := ParseVerbFlags(fs, args); done {
		return code
	}

	cfg, err := hostdaemon.LoadConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cfg = applyDaemonStartFlags(cfg, daemonStartFlags{
		allowPaths:   *allowPaths,
		listen:       *listen,
		proxy:        *proxy,
		apiKey:       *apiKey,
		insecure:     *insecureHop,
		tlsCA:        *tlsCA,
		label:        *label,
		identityFile: *identityFile,
	})

	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	statusPath := filepath.Join(dir, "daemon.status")
	cfg.StatusFile = statusPath

	if cfg.ProxyURL == "" {
		fmt.Fprintln(os.Stderr, "rimsky daemon: --proxy is required (or set RIMSKY_HOST_DAEMON_PROXY_URL)")
		return 2
	}

	if *foreground {
		logger := serverkit.NewJSONLoggerForLevel(cfg.LogLevel)
		slog.SetDefault(logger)

		// @decision: graceful-shutdown
		ctx, stopSignals := serverkit.ShutdownContext(context.Background(), logger)
		defer stopSignals()
		if err := hostdaemon.Run(ctx, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	return daemonize(args, dir, statusPath, cfg.ProxyURL, os.Executable)
}

// @story: host-daemon-control-plane
func daemonize(startArgs []string, stateDir, statusPath, proxy string, selfExecutable func() (string, error)) int {
	self, err := selfExecutable()
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

	logPath := filepath.Join(stateDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	forkArgs := append([]string{"daemon", "start", "--foreground", "--state-dir", stateDir}, startArgs...)
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

	pidPath := filepath.Join(stateDir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(childPid)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(daemonReadinessTimeout)
	for time.Now().Before(deadline) {
		snap, ok, readErr := readStatusFile(statusPath)
		if readErr == nil && ok && snap.Connected {
			fmt.Fprintf(os.Stdout, "rimsky daemon started (pid %d, connected to %s)\n", childPid, snap.Proxy)
			return 0
		}
		if !processAlive(childPid) {
			_ = os.Remove(pidPath)
			fmt.Fprintf(os.Stderr, "rimsky daemon: daemon exited during startup (proxy %q unreachable or misconfigured); see %s for details\n", proxy, logPath)
			return 1
		}
		time.Sleep(daemonReadinessPollInterval)
	}

	killProcess(childPid)
	_ = os.Remove(pidPath)
	fmt.Fprintf(os.Stderr,
		"rimsky daemon: daemon did not connect to proxy %q within %s; killed (proxy unreachable or misconfigured); see %s for details\n",
		proxy, daemonReadinessTimeout, logPath)
	return 1
}

// @story: host-daemon-control-plane
type daemonStatusReport struct {
	Running   bool                     `json:"running"`
	Connected bool                     `json:"connected"`
	PID       int                      `json:"pid,omitempty"`
	StalePID  bool                     `json:"stale_pid,omitempty"`
	Proxy     string                   `json:"proxy,omitempty"`
	Since     string                   `json:"since,omitempty"`
	Detail    string                   `json:"detail,omitempty"`
	Children  []hostdaemon.ChildStatus `json:"children,omitempty"`
}

func runDaemonStatus(args []string) int {
	fs := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	SetUsage(fs, UsageLine("daemon status", "[--state-dir <dir>]"))
	stateDir := fs.String("state-dir", "", "directory for pid and status files (default ~/.rimsky)")
	var common CommonFlags
	RegisterOutputFlags(fs, &common)
	if code, done := ParseVerbFlags(fs, args); done {
		return code
	}
	if err := common.ResolveFormat("daemon status", NoTable); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	SetActiveCommonFlags(&common)
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	report, err := gatherDaemonStatus(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return Render(common.Format, report, func() {
		printDaemonStatus(report)
	})
}

// @story: host-daemon-control-plane
func gatherDaemonStatus(dir string) (daemonStatusReport, error) {
	pid, ok, err := readDaemonPIDFrom(dir)
	if err != nil {
		return daemonStatusReport{}, err
	}
	if !ok {
		return daemonStatusReport{}, nil
	}
	if !processAlive(pid) {
		return daemonStatusReport{PID: pid, StalePID: true}, nil
	}
	report := daemonStatusReport{Running: true, PID: pid}
	snap, present, readErr := readStatusFile(filepath.Join(dir, "daemon.status"))
	if readErr != nil {
		report.Detail = "status unreadable: " + readErr.Error()
		return report, nil
	}
	if present && snap.Connected {
		report.Connected = true
		report.Proxy = snap.Proxy
		report.Since = snap.Since
		report.Children = snap.Children
	}
	return report, nil
}

func printDaemonStatus(report daemonStatusReport) {
	switch {
	case !report.Running && report.StalePID:
		fmt.Fprintf(os.Stdout, "rimsky daemon: not running (stale pid %d)\n", report.PID)
	case !report.Running:
		fmt.Fprintln(os.Stdout, "rimsky daemon: not running")
	case report.Detail != "":
		fmt.Fprintf(os.Stdout, "rimsky daemon: running (pid %d, %s)\n", report.PID, report.Detail)
	case report.Connected:
		fmt.Fprintf(os.Stdout, "rimsky daemon: connected (pid %d, proxy %s, since %s)\n",
			report.PID, report.Proxy, report.Since)
		printDaemonChildren(report.Children)
	default:
		fmt.Fprintf(os.Stdout, "rimsky daemon: running, disconnected (pid %d)\n", report.PID)
	}
}

// @story: host-daemon-control-plane
func printDaemonChildren(children []hostdaemon.ChildStatus) {
	if len(children) == 0 {
		fmt.Fprintln(os.Stdout, "  spawned children: none")
		return
	}
	fmt.Fprintln(os.Stdout, "  spawned children:")
	for _, c := range children {
		fmt.Fprintf(os.Stdout, "    run-scope=%s binding=%s spawn-id=%s\n", c.RunScopeID, c.Binding, c.SpawnID)
	}
}

func runDaemonStop(args []string) int {
	return stopDaemon(args, systemDaemonProcessControl())
}

func stopDaemon(args []string, pc daemonProcessControl) int {
	fs := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	SetUsage(fs, UsageLine("daemon stop", "[--state-dir <dir>]"))
	stateDir := fs.String("state-dir", "", "directory for pid and status files (default ~/.rimsky)")
	if code, done := ParseVerbFlags(fs, args); done {
		return code
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	pid, ok, err := readDaemonPIDFrom(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "rimsky daemon: not running")
		return 0
	}

	if pc.alive(pid) {
		if termErr := pc.terminate(pid); termErr != nil {
			fmt.Fprintf(os.Stderr, "rimsky daemon: signal pid %d: %v\n", pid, termErr)
			return 1
		}
		waitForExit(pc, pid, pc.graceTimeout)
		if pc.alive(pid) {
			pc.kill(pid)
			waitForExit(pc, pid, pc.sigkillWindow)
		}
		if pc.alive(pid) {
			fmt.Fprintf(os.Stderr, "rimsky daemon: pid %d did not exit after SIGTERM and SIGKILL\n", pid)
			return 1
		}
	}

	pidPath := filepath.Join(dir, "daemon.pid")
	if rmErr := os.Remove(pidPath); rmErr != nil && !os.IsNotExist(rmErr) {
		fmt.Fprintln(os.Stderr, rmErr)
		return 1
	}
	_ = os.Remove(filepath.Join(dir, "daemon.status"))
	fmt.Fprintf(os.Stdout, "rimsky daemon: stopped (pid %d)\n", pid)
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

func readDaemonPID() (int, bool, error) {
	dir, err := resolveStateDir("")
	if err != nil {
		return 0, false, err
	}
	return readDaemonPIDFrom(dir)
}

func readDaemonPIDFrom(dir string) (int, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "daemon.pid"))
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
		return 0, false, fmt.Errorf("daemon pid file: %w", err)
	}
	return pid, true, nil
}

func readStatusFile(path string) (hostdaemon.StatusSnapshot, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hostdaemon.StatusSnapshot{}, false, nil
	}
	if err != nil {
		return hostdaemon.StatusSnapshot{}, false, err
	}
	var snap hostdaemon.StatusSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return hostdaemon.StatusSnapshot{}, false, fmt.Errorf("daemon status file: %w", err)
	}
	return snap, true, nil
}

func waitForExit(pc daemonProcessControl, pid int, timeout time.Duration) {
	deadline := pc.clock.Now().Add(timeout)
	for pc.clock.Now().Before(deadline) {
		if !pc.alive(pid) {
			return
		}
		if err := pc.clock.Sleep(context.Background(), daemonExitPollInterval); err != nil {
			return
		}
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
