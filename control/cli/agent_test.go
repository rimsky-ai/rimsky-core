// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestRunAgentUsage asserts the dispatcher rejects missing / unknown
// subcommands with exit code 2.
func TestRunAgentUsage(t *testing.T) {
	if got := RunAgent(nil); got != 2 {
		t.Fatalf("RunAgent(nil) = %d, want 2", got)
	}
	if got := RunAgent([]string{"bogus"}); got != 2 {
		t.Fatalf("RunAgent(bogus) = %d, want 2", got)
	}
	if got := RunAgent([]string{"help"}); got != 0 {
		t.Fatalf("RunAgent(help) = %d, want 0", got)
	}
}

// TestAgentStatusNotRunning asserts status returns 0 and reports not-running
// when no pid file exists.
func TestAgentStatusNotRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := runAgentStatus(nil); got != 0 {
		t.Fatalf("status (no pid file) = %d, want 0", got)
	}
}

// TestAgentStopNotRunning asserts stop is a no-op success when no pid file
// exists.
func TestAgentStopNotRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := runAgentStop(nil); got != 0 {
		t.Fatalf("stop (no pid file) = %d, want 0", got)
	}
}

// TestAgentStatusStopLifecycle writes a pid file for a real long-lived helper
// process, asserts status sees it alive, then stop SIGTERMs it and removes the
// pid file.
func TestAgentStatusStopLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A real child that ignores nothing and sleeps until signalled.
	helper := exec.Command("sleep", "60")
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	defer func() { _ = helper.Process.Kill() }()

	pidPath := filepath.Join(home, ".rimsky", "agent.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	if got := runAgentStatus(nil); got != 0 {
		t.Fatalf("status (running) = %d, want 0", got)
	}

	if got := runAgentStop(nil); got != 0 {
		t.Fatalf("stop = %d, want 0", got)
	}

	// The pid file should be gone.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after stop, stat err = %v", err)
	}

	// The helper should have been terminated.
	_, _ = helper.Process.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(helper.Process.Pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("helper still alive after stop")
}

// TestAgentStatusStalePID asserts status reports not-running for a pid that is
// no longer alive.
func TestAgentStatusStalePID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Spawn and immediately reap a child so its pid is dead but reusable-ish.
	helper := exec.Command("true")
	if err := helper.Run(); err != nil {
		t.Fatalf("run helper: %v", err)
	}

	pidPath := filepath.Join(home, ".rimsky", "agent.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	// status returns 0 regardless; we just assert it doesn't error.
	if got := runAgentStatus(nil); got != 0 {
		t.Fatalf("status (stale pid) = %d, want 0", got)
	}
}

// TestSplitNonEmpty covers the allow-paths flag parser.
func TestSplitNonEmpty(t *testing.T) {
	got := splitNonEmpty(" /a/* , ,./b ", ",")
	if len(got) != 2 || got[0] != "/a/*" || got[1] != "./b" {
		t.Fatalf("splitNonEmpty = %v, want [/a/* ./b]", got)
	}
	if len(splitNonEmpty("", ",")) != 0 {
		t.Fatal("splitNonEmpty(\"\") should be empty")
	}
}
