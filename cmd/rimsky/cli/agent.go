// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// agent.go — `rimsky agent` dispatcher. Wraps the importable
// runtime/hostagent daemon as a CLI subcommand: `start` runs (or daemonizes)
// the host-agent main loop, `status` checks the pid-file for a live daemon,
// and `stop` SIGTERMs it. The daemon itself lives in runtime/hostagent so it
// can also be run as the standalone cmd/rimsky-host-agent binary.
//
// @concept: host-agent
package cli

import (
	"context"
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

	if *foreground {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		if err := hostagent.Run(ctx, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	return daemonizeAgent(args)
}

// daemonizeAgent forks the current executable with --foreground, records its
// pid in the pid file, and returns. The child inherits the environment so its
// hostagent.LoadConfigFromEnv reads the same RIMSKY_* values.
func daemonizeAgent(startArgs []string) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	forkArgs := append([]string{"agent", "start", "--foreground"}, startArgs...)
	cmd := exec.Command(self, forkArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	pidPath, err := agentPIDPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "rimsky agent started (pid %d)\n", cmd.Process.Pid)
	return 0
}

// runAgentStatus reports whether the recorded agent daemon is alive.
func runAgentStatus(_ []string) int {
	pid, ok, err := readAgentPID()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "rimsky agent: not running")
		return 0
	}
	if processAlive(pid) {
		fmt.Fprintf(os.Stdout, "rimsky agent: running (pid %d)\n", pid)
	} else {
		fmt.Fprintf(os.Stdout, "rimsky agent: not running (stale pid %d)\n", pid)
	}
	return 0
}

// runAgentStop SIGTERMs the recorded daemon, waits briefly for exit, and
// removes the pid file.
func runAgentStop(_ []string) int {
	pid, ok, err := readAgentPID()
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
		waitForExit(pid, 5*time.Second)
	}

	pidPath, pathErr := agentPIDPath()
	if pathErr != nil {
		fmt.Fprintln(os.Stderr, pathErr)
		return 1
	}
	if rmErr := os.Remove(pidPath); rmErr != nil && !os.IsNotExist(rmErr) {
		fmt.Fprintln(os.Stderr, rmErr)
		return 1
	}
	fmt.Fprintf(os.Stdout, "rimsky agent: stopped (pid %d)\n", pid)
	return 0
}

// agentPIDPath is ~/.rimsky/agent.pid.
func agentPIDPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rimsky", "agent.pid"), nil
}

// readAgentPID reads the pid from the pid file. ok=false when the file is
// absent or empty.
func readAgentPID() (int, bool, error) {
	pidPath, err := agentPIDPath()
	if err != nil {
		return 0, false, err
	}
	raw, err := os.ReadFile(pidPath)
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
		return 0, false, fmt.Errorf("agent pid file %q: %w", pidPath, err)
	}
	return pid, true, nil
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
