// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

const demoScriptRelPath = "examples/host-agent-control-plane-demo.sh"

var connectedLineRE = regexp.MustCompile(`rimsky agent started \(pid (\d+), connected to [^)]+\)`)

func TestHostAgentControlPlaneDemo(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "rimsky")
	proxyBin := filepath.Join(t.TempDir(), "rimsky-host-agent-proxy")
	buildRimskyCLIBinary(t, binPath)
	buildHostAgentProxyBinary(t, proxyBin)

	scriptPath := repoFilePath(t, demoScriptRelPath)

	out, code := runHostAgentDemoScript(t, scriptPath, binPath, proxyBin, 60*time.Second)
	if code != 0 {
		t.Fatalf("host-agent-control-plane-demo.sh exited %d (want 0)\noutput:\n%s", code, out)
	}

	if !strings.Contains(out, "step 1 OK") {
		t.Fatalf("demo did not exhibit step 1 (failure path); output:\n%s", out)
	}

	match := connectedLineRE.FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("demo did not print `rimsky agent started (pid N, connected to ADDR)`; output:\n%s", out)
	}

	if !strings.Contains(out, "rimsky agent: connected") {
		t.Fatalf("demo did not exhibit a `connected` status; output:\n%s", out)
	}

	if !strings.Contains(out, "rimsky agent: stopped (pid "+match[1]+")") {
		t.Fatalf("demo did not exhibit a clean stop for pid %s; output:\n%s", match[1], out)
	}

	if !strings.Contains(out, "all steps OK") {
		t.Fatalf("demo did not reach `all steps OK`; output:\n%s", out)
	}
}

func TestHostAgentControlPlaneDispatchReap(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: false})

	binPath := filepath.Join(t.TempDir(), "rimsky")
	buildRimskyCLIBinary(t, binPath)

	stateDir := t.TempDir()
	termLog := filepath.Join(t.TempDir(), "stubchild-term.log")
	t.Setenv("STUBCHILD_TERM_LOG", termLog)

	startEnv := []string{
		"HOME=" + t.TempDir(),
		"PATH=" + os.Getenv("PATH"),
		"STUBCHILD_TERM_LOG=" + termLog,
	}
	startOut, startErr, startCode := runCmdWithEnv(t, binPath,
		[]string{"agent", "start",
			"--proxy", fx.proxyAddr,
			"--state-dir", stateDir,
			"--api-key", fx.ownerKeyID,
		}, startEnv, 30*time.Second)
	if startCode != 0 {
		t.Fatalf("rimsky agent start exited %d\nstdout:\n%s\nstderr:\n%s", startCode, startOut, startErr)
	}
	if !strings.Contains(startOut, "connected to "+fx.proxyAddr) {
		t.Fatalf("rimsky agent start did not report `connected to %s`; stdout:\n%s", fx.proxyAddr, startOut)
	}

	pidRaw, err := os.ReadFile(filepath.Join(stateDir, "agent.pid"))
	if err != nil {
		t.Fatalf("read agent.pid: %v", err)
	}
	daemonPid, err := parseDaemonPid(string(pidRaw))
	if err != nil {
		t.Fatalf("parse agent.pid: %v", err)
	}

	tid := fx.deployLateBindTemplate(t, "ctrl-plane-dispatch")
	iid := fx.createLateBindInstance(t, tid, "ck-ctrl-plane-dispatch", fx.stubBinary)
	worker := fx.h.FindNode(iid, "worker")
	if worker == nil {
		t.Fatal("worker node should exist after createLateBindInstance")
	}
	if !fx.h.WaitForNodeState(worker.ID,
		cascade.NodeStateFresh, 45*time.Second) {
		t.Fatal("late-bound worker did not reach fresh — agent did not spawn the child via dispatch")
	}

	preStopChildren := findChildrenByExe(fx.stubBinary)
	if len(preStopChildren) == 0 {
		t.Fatal("no stubchild processes alive after dispatch — the agent never spawned the child")
	}

	stopOut, stopErr, stopCode := runCmdWithEnv(t, binPath,
		[]string{"agent", "stop", "--state-dir", stateDir},
		startEnv, 60*time.Second)
	if stopCode != 0 {
		t.Fatalf("rimsky agent stop exited %d\nstdout:\n%s\nstderr:\n%s", stopCode, stopOut, stopErr)
	}
	if !strings.Contains(stopOut, fmt.Sprintf("stopped (pid %d)", daemonPid)) {
		t.Fatalf("rimsky agent stop did not report `stopped (pid %d)`; stdout:\n%s", daemonPid, stopOut)
	}

	if !waitProcessGone(daemonPid, 5*time.Second) {
		t.Fatalf("daemon pid %d still alive after `agent stop` — stop did not fully tear down", daemonPid)
	}

	if !waitChildrenGone(preStopChildren, 10*time.Second) {
		survivors := stillAlive(preStopChildren)
		t.Fatalf("agent stop left %d zombie stubchild process(es) alive (pids %v) — Falsifier triggered",
			len(survivors), survivors)
	}

	if _, statErr := os.Stat(termLog); statErr != nil {
		t.Fatalf("stubchild term log %s missing — the agent did not signal the spawned child during stop: %v",
			termLog, statErr)
	}
}

func runHostAgentDemoScript(t *testing.T, scriptPath, binPath, proxyBin string, timeout time.Duration) (string, int) {
	t.Helper()
	runCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/bin/bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"RIMSKY_BIN="+binPath,
		"RIMSKY_PROXY_BIN="+proxyBin,
		"HOME="+t.TempDir(),
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	combined := out.String()
	if errStr := errBuf.String(); errStr != "" {
		combined += "\n[stderr]\n" + errStr
	}
	if err == nil {
		return combined, 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("host-agent-control-plane-demo.sh fork error: %v\noutput:\n%s", err, combined)
	}
	return combined, exitErr.ExitCode()
}

func buildRimskyCLIBinary(t *testing.T, binPath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/rimsky")
	cmd.Dir = repoRoot(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build ./cmd/rimsky: %v\nstderr:\n%s", err, stderr.String())
	}
}

func buildHostAgentProxyBinary(t *testing.T, binPath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/rimsky-host-agent-proxy")
	cmd.Dir = repoRoot(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build ./cmd/rimsky-host-agent-proxy: %v\nstderr:\n%s", err, stderr.String())
	}
}

func repoFilePath(t *testing.T, rel string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to locate the test source file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	path := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shipped file %s not found at %s: %v", rel, path, err)
	}
	return path
}

func parseDaemonPid(s string) (int, error) {
	trim := strings.TrimSpace(s)
	if trim == "" {
		return 0, errors.New("empty pid file")
	}
	var pid int
	if _, err := fmt.Sscanf(trim, "%d", &pid); err != nil {
		return 0, err
	}
	if pid <= 0 {
		return 0, fmt.Errorf("non-positive pid %d", pid)
	}
	return pid, nil
}

func runCmdWithEnv(t *testing.T, binPath string, args, env []string, timeout time.Duration) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Env = env
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		return out.String(), errBuf.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("%s fork error: %v\nstdout:\n%s\nstderr:\n%s", binPath, err, out.String(), errBuf.String())
	}
	return out.String(), errBuf.String(), exitErr.ExitCode()
}

func findChildrenByExe(exePath string) []int {
	out, err := exec.Command("ps", "-e", "-o", "pid=,command=").CombinedOutput()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, exePath) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(fields[0], "%d", &pid); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

func waitChildrenGone(pids []int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(stillAlive(pids)) == 0 {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func stillAlive(pids []int) []int {
	var live []int
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			live = append(live, pid)
		}
	}
	return live
}

func waitProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
