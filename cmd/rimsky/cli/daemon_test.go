// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
)

func writeDaemonFixtureBinary(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestDaemonize_ReapsCrashedChildAndLogsStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}
	dir := t.TempDir()
	fixture := writeDaemonFixtureBinary(t, dir, "fake-daemon",
		`echo "boom: proxy unreachable" >&2; exit 7`)

	stateDir := filepath.Join(dir, "state")
	statusPath := filepath.Join(stateDir, "daemon.status")

	var got int
	out := captureStderr(t, func() {
		got = daemonize(nil, stateDir, statusPath, "proxy.example:9000", func() (string, error) { return fixture, nil })
	})
	if got != 1 {
		t.Fatalf("daemonize = %d, want 1 (child crashed during startup)", got)
	}

	logPath := filepath.Join(stateDir, "daemon.log")
	if !strings.Contains(out, logPath) {
		t.Fatalf("failure message should point at %q, got: %s", logPath, out)
	}

	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	if !strings.Contains(string(logBody), "boom: proxy unreachable") {
		t.Fatalf("daemon.log should capture the daemon child's stderr, got: %q", string(logBody))
	}

	pidPath := filepath.Join(stateDir, "daemon.pid")
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after a crashed startup, stat err = %v", err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 64*1024)
		tmp := make([]byte, 4096)
		for {
			n, rerr := r.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if rerr != nil {
				break
			}
		}
		out <- string(buf)
	}()
	fn()
	os.Stdout = saved
	_ = w.Close()
	return <-out
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 64*1024)
		tmp := make([]byte, 4096)
		for {
			n, rerr := r.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if rerr != nil {
				break
			}
		}
		out <- string(buf)
	}()
	fn()
	os.Stderr = saved
	_ = w.Close()
	return <-out
}

func TestRunDaemonUsage(t *testing.T) {
	if got := RunDaemon(nil); got != 2 {
		t.Fatalf("RunDaemon(nil) = %d, want 2", got)
	}
	if got := RunDaemon([]string{"bogus"}); got != 2 {
		t.Fatalf("RunDaemon(bogus) = %d, want 2", got)
	}
	if got := RunDaemon([]string{"help"}); got != 0 {
		t.Fatalf("RunDaemon(help) = %d, want 0", got)
	}
}

func TestDaemonStatusNotRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := runDaemonStatus(nil); got != 0 {
		t.Fatalf("status (no pid file) = %d, want 0", got)
	}
}

func TestDaemonStopNotRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := runDaemonStop(nil); got != 0 {
		t.Fatalf("stop (no pid file) = %d, want 0", got)
	}
}

func TestDaemonStatusStopLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	helper := exec.Command("sleep", "60")
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	waitDone := make(chan struct{})
	go func() {
		_, _ = helper.Process.Wait()
		close(waitDone)
	}()
	defer func() { _ = helper.Process.Kill() }()

	pidPath := filepath.Join(home, ".rimsky", "daemon.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	if got := runDaemonStatus(nil); got != 0 {
		t.Fatalf("status (running) = %d, want 0", got)
	}

	if got := runDaemonStop(nil); got != 0 {
		t.Fatalf("stop = %d, want 0", got)
	}

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after stop, stat err = %v", err)
	}

	<-waitDone
	if processAlive(helper.Process.Pid) {
		t.Fatal("helper still alive after stop")
	}
}

func TestDaemonStatusStalePID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	helper := exec.Command("true")
	if err := helper.Run(); err != nil {
		t.Fatalf("run helper: %v", err)
	}

	pidPath := filepath.Join(home, ".rimsky", "daemon.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	if got := runDaemonStatus(nil); got != 0 {
		t.Fatalf("status (stale pid) = %d, want 0", got)
	}
}

func TestDaemonStatusPrintsSpawnedChildren(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	helper := exec.Command("sleep", "60")
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	defer func() { _ = helper.Process.Kill() }()

	stateDir := filepath.Join(home, ".rimsky")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pidPath := filepath.Join(stateDir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	snap := hostdaemon.StatusSnapshot{
		Connected: true,
		Proxy:     "proxy.example:9000",
		Since:     "2026-07-19T00:00:00Z",
		Children: []hostdaemon.ChildStatus{
			{SpawnID: "spawn-1", RunScopeID: "run-scope-abc", Binding: "/usr/local/bin/codegen"},
		},
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "daemon.status"), body, 0o600); err != nil {
		t.Fatalf("write status: %v", err)
	}

	var got int
	out := captureStdout(t, func() { got = runDaemonStatus(nil) })
	if got != 0 {
		t.Fatalf("status = %d, want 0", got)
	}
	if !strings.Contains(out, "run-scope-abc") || !strings.Contains(out, "/usr/local/bin/codegen") || !strings.Contains(out, "spawn-1") {
		t.Fatalf("status output missing spawned-children detail, got: %s", out)
	}
}

type virtualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *virtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *virtualClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return nil
}

// @story: host-daemon-control-plane
func TestDaemonStopEscalatesToSigkillWhenProcessIgnoresSigterm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const pid = 4242
	pidPath := filepath.Join(home, ".rimsky", "daemon.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	clock := &virtualClock{now: time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	alive := true
	var signals []string
	var sigkillAt time.Time
	pc := daemonProcessControl{
		alive: func(int) bool {
			mu.Lock()
			defer mu.Unlock()
			return alive
		},
		terminate: func(int) error {
			mu.Lock()
			defer mu.Unlock()
			signals = append(signals, "SIGTERM")
			return nil
		},
		kill: func(int) {
			mu.Lock()
			defer mu.Unlock()
			signals = append(signals, "SIGKILL")
			sigkillAt = clock.Now()
			alive = false
		},
		clock:         clock,
		graceTimeout:  daemonStopGraceTimeout,
		sigkillWindow: daemonStopKillTimeout,
	}

	startedAt := clock.Now()
	var got int
	out := captureStdout(t, func() { got = stopDaemon(nil, pc) })
	if got != 0 {
		t.Fatalf("stop = %d, want 0 (SIGKILL escalation should still confirm exit); output: %s", got, out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(signals) != 2 || signals[0] != "SIGTERM" || signals[1] != "SIGKILL" {
		t.Fatalf("signals = %v, want [SIGTERM SIGKILL]", signals)
	}
	if waited := sigkillAt.Sub(startedAt); waited < daemonStopGraceTimeout {
		t.Fatalf("SIGKILL escalated after %s of the daemon's clock, want at least the %s grace window",
			waited, daemonStopGraceTimeout)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after confirmed stop, stat err = %v", err)
	}
}

func TestSplitNonEmpty(t *testing.T) {
	got := splitNonEmpty(" /a/* , ,./b ", ",")
	if len(got) != 2 || got[0] != "/a/*" || got[1] != "./b" {
		t.Fatalf("splitNonEmpty = %v, want [/a/* ./b]", got)
	}
	if len(splitNonEmpty("", ",")) != 0 {
		t.Fatal("splitNonEmpty(\"\") should be empty")
	}
}

// @concept: host-daemon
func TestDaemonStartAllowPaths_FlagOverridesEnv(t *testing.T) {
	t.Setenv("RIMSKY_DAEMON_ALLOW_PATHS", "/from/env/*")
	cfg, err := hostdaemon.LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv: %v", err)
	}
	if len(cfg.AllowPaths) != 1 || cfg.AllowPaths[0] != "/from/env/*" {
		t.Fatalf("env parsing: AllowPaths = %v, want [/from/env/*]", cfg.AllowPaths)
	}

	got := applyDaemonStartFlags(cfg, daemonStartFlags{allowPaths: "/from/flag/*"})
	if len(got.AllowPaths) != 1 || got.AllowPaths[0] != "/from/flag/*" {
		t.Fatalf("--allow-paths must override the env value; got %v", got.AllowPaths)
	}

	kept := applyDaemonStartFlags(cfg, daemonStartFlags{})
	if len(kept.AllowPaths) != 1 || kept.AllowPaths[0] != "/from/env/*" {
		t.Fatalf("empty flag must keep the env value; got %v", kept.AllowPaths)
	}
}
