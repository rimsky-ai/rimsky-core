// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

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

func TestAgentStatusNotRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := runAgentStatus(nil); got != 0 {
		t.Fatalf("status (no pid file) = %d, want 0", got)
	}
}

func TestAgentStopNotRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := runAgentStop(nil); got != 0 {
		t.Fatalf("stop (no pid file) = %d, want 0", got)
	}
}

func TestAgentStatusStopLifecycle(t *testing.T) {
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

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after stop, stat err = %v", err)
	}

	<-waitDone
	if processAlive(helper.Process.Pid) {
		t.Fatal("helper still alive after stop")
	}
}

func TestAgentStatusStalePID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

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

	if got := runAgentStatus(nil); got != 0 {
		t.Fatalf("status (stale pid) = %d, want 0", got)
	}
}

func TestAgentStatusPrintsSpawnedChildren(t *testing.T) {
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
	pidPath := filepath.Join(stateDir, "agent.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	snap := hostagent.StatusSnapshot{
		Connected: true,
		Proxy:     "proxy.example:9000",
		Since:     "2026-07-19T00:00:00Z",
		Children: []hostagent.ChildStatus{
			{SpawnID: "spawn-1", RunScopeID: "run-scope-abc", Binding: "/usr/local/bin/codegen"},
		},
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "agent.status"), body, 0o600); err != nil {
		t.Fatalf("write status: %v", err)
	}

	var got int
	out := captureStdout(t, func() { got = runAgentStatus(nil) })
	if got != 0 {
		t.Fatalf("status = %d, want 0", got)
	}
	if !strings.Contains(out, "run-scope-abc") || !strings.Contains(out, "/usr/local/bin/codegen") || !strings.Contains(out, "spawn-1") {
		t.Fatalf("status output missing spawned-children detail, got: %s", out)
	}
}

func TestAgentStopEscalatesToSigkillWhenProcessIgnoresSigterm(t *testing.T) {
	savedGrace, savedKill := agentStopGraceTimeout, agentStopKillTimeout
	agentStopGraceTimeout = 200 * time.Millisecond
	agentStopKillTimeout = 5 * time.Second
	t.Cleanup(func() { agentStopGraceTimeout, agentStopKillTimeout = savedGrace, savedKill })

	home := t.TempDir()
	t.Setenv("HOME", home)

	helper := exec.Command("sh", "-c", "trap '' TERM; sleep 60")
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	waitDone := make(chan struct{})
	go func() {
		_, _ = helper.Process.Wait()
		close(waitDone)
	}()
	defer func() { _ = helper.Process.Kill() }()

	pidPath := filepath.Join(home, ".rimsky", "agent.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	if got := runAgentStop(nil); got != 0 {
		t.Fatalf("stop = %d, want 0 (SIGKILL escalation should still confirm exit)", got)
	}

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after confirmed stop, stat err = %v", err)
	}

	<-waitDone
	if processAlive(helper.Process.Pid) {
		t.Fatal("helper still alive after stop escalated to SIGKILL")
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
