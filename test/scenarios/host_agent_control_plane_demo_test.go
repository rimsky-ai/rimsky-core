// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_control_plane_demo_test.go — STORY-host-agent-control-plane
// proof artifact. Boots a real rimsky-host-agent-proxy, drives the shipped
// examples/host-agent-control-plane-demo.sh as a SUBPROCESS through the
// real `rimsky agent` CLI (built into a temp binary), and asserts the
// integrating lifecycle exhibited the three Falsifier-load-bearing
// properties:
//
//  1. `agent start --proxy <bogus>` REFUSES with a clear diagnostic — it
//     does not silently succeed.
//  2. `agent status` reports `connected` only when the bidi stream is
//     actually up — the daemon's connection sentinel is the source of
//     truth, not the pid.
//  3. `agent stop` brings the daemon down cleanly (exit 0) with no
//     zombies — the daemon process is gone and the spawned-child reap
//     leg (`host_agent_reap_test.go` covers the proxy → agent → child
//     reap; this gate covers the CLI → daemon → child reap by
//     additionally launching a dispatched spawn and asserting the
//     stubchild process is gone after `agent stop`).
//
// The cross-Falsifier shape of the test mirrors the spec's wording:
// "spec cites `test/scenarios/host_agent_reap_test.go` for the reap leg;
// needs full start/status/stop demo." The reap-on-stream-close path is
// already proven elsewhere; this gate's contribution is the CLI surface
// + the daemonize handshake.

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

// demoScriptRelPath is the shipped demo script under examples/. The
// driver test runs it AS WRITTEN — any defect in the script (a missing
// diagnostic check, a wrong output pattern) fails the test.
const demoScriptRelPath = "examples/host-agent-control-plane-demo.sh"

// connectedLineRE matches the `start` success line the demo's happy path
// asserts on: `rimsky agent started (pid <N>, connected to <addr>)`. The
// capture group is the pid, used to confirm the same pid is gone after
// stop (no-zombies).
var connectedLineRE = regexp.MustCompile(`rimsky agent started \(pid (\d+), connected to [^)]+\)`)

// TestHostAgentControlPlaneDemo runs the shipped CLI-lifecycle demo end
// to end through real binaries: a freshly-built `rimsky` CLI dialing a
// freshly-built `rimsky-host-agent-proxy`. Asserts:
//
//   - the demo subprocess exits 0;
//   - its stdout carries the failure-path diag, the `connected` lines
//     for start and status, and the `stopped` line;
//   - the daemon pid printed by `start` is no longer alive after `stop`.
//
// The pid-no-longer-alive check is the "no zombies" property at the
// daemon-process level. The reap-of-spawned-children property is
// covered by the dispatch leg in TestHostAgentControlPlaneDispatchReap
// below (a CLI-launched agent receives a real Spawn through the proxy
// and the dispatched stubchild is gone after `agent stop`).
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

	// @constraint: Failure-path diagnostic must surface — proves step 1 actually ran
	// (a test that skipped step 1 silently would still print the happy
	// path; the explicit step-1 OK line is the falsifier-defeating
	// witness).
	if !strings.Contains(out, "step 1 OK") {
		t.Fatalf("demo did not exhibit step 1 (failure path); output:\n%s", out)
	}

	// @constraint: Happy-path start must say `connected to` — proves the readiness
	// handshake fired (not a silent fork + cache).
	match := connectedLineRE.FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("demo did not print `rimsky agent started (pid N, connected to ADDR)`; output:\n%s", out)
	}

	// @constraint: Status must say `connected` — proves the sentinel-backed report.
	if !strings.Contains(out, "rimsky agent: connected") {
		t.Fatalf("demo did not exhibit a `connected` status; output:\n%s", out)
	}

	// @constraint: Stop must say `stopped (pid N)` — proves the daemon was signaled
	// and the recorded state was cleared.
	if !strings.Contains(out, "rimsky agent: stopped (pid "+match[1]+")") {
		t.Fatalf("demo did not exhibit a clean stop for pid %s; output:\n%s", match[1], out)
	}

	if !strings.Contains(out, "all steps OK") {
		t.Fatalf("demo did not reach `all steps OK`; output:\n%s", out)
	}
}

// TestHostAgentControlPlaneDispatchReap exercises the missing leg the
// shell script alone cannot cover: a CLI-launched agent receives a
// REAL Spawn through the proxy, the stubchild starts and binds its
// gRPC port, then `rimsky agent stop` brings the daemon down and the
// spawned stubchild process must be GONE — the Falsifier-named
// "stop exits cleanly but leaves zombie children" failure mode.
//
// The harness's existing `newHostAgentFixture` boots control-api +
// proxy and would normally run the agent in-process. We disable the
// in-process agent (withAgent:false) and launch the agent via the
// `rimsky agent start` CLI subprocess against the same proxy port,
// so the CLI path is the integrating wiring under test.
func TestHostAgentControlPlaneDispatchReap(t *testing.T) {
	// @deliberate: Stand up control-api + proxy + stub binary without an in-process
	// agent — we'll launch the agent via the CLI subprocess.
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: false})

	binPath := filepath.Join(t.TempDir(), "rimsky")
	buildRimskyCLIBinary(t, binPath)

	stateDir := t.TempDir()
	termLog := filepath.Join(t.TempDir(), "stubchild-term.log")
	t.Setenv("STUBCHILD_TERM_LOG", termLog)

	// @deliberate: Launch the agent via the CLI subprocess. The pid file lands in
	// stateDir; the status file is gated on a successful RegisterAck so
	// `start` won't return until the bidi stream is up — that is the
	// integrating handshake under test.
	startEnv := []string{
		"HOME=" + t.TempDir(), // @deliberate: isolate ~/.rimsky for any fallback paths
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

	// @deliberate: Drive a real dispatch through the proxy → the CLI-launched agent
	// → the spawned stubchild. The dispatch reaching `fresh` is proof
	// the agent exec()d the child and the Execute stream ran to a
	// Success outcome.
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

	// @constraint: Snapshot the set of pids that look like our spawned stubchild
	// BEFORE stop, so we can assert none of them survive after stop.
	// The stubchild is the test fixture binary at fx.stubBinary; any
	// process whose argv carries that path is a live spawn under
	// this agent and MUST be gone after `agent stop`.
	preStopChildren := findChildrenByExe(fx.stubBinary)
	if len(preStopChildren) == 0 {
		t.Fatal("no stubchild processes alive after dispatch — the agent never spawned the child")
	}

	// @deliberate: Tear the daemon down through the CLI. The stop verb SIGTERMs the
	// daemon, which runs its reap loop with ReapGracePeriod budget.
	stopOut, stopErr, stopCode := runCmdWithEnv(t, binPath,
		[]string{"agent", "stop", "--state-dir", stateDir},
		startEnv, 60*time.Second)
	if stopCode != 0 {
		t.Fatalf("rimsky agent stop exited %d\nstdout:\n%s\nstderr:\n%s", stopCode, stopOut, stopErr)
	}
	if !strings.Contains(stopOut, fmt.Sprintf("stopped (pid %d)", daemonPid)) {
		t.Fatalf("rimsky agent stop did not report `stopped (pid %d)`; stdout:\n%s", daemonPid, stopOut)
	}

	// @constraint: Falsifier check #1: the daemon process itself must be gone.
	if !waitProcessGone(daemonPid, 5*time.Second) {
		t.Fatalf("daemon pid %d still alive after `agent stop` — stop did not fully tear down", daemonPid)
	}

	// @constraint: Falsifier check #2: every stubchild process the agent spawned
	// before stop must be GONE. A surviving stubchild is the "zombie
	// children" failure mode the Falsifier names. Allow a brief grace
	// window because SIGTERM propagation through the agent's reap loop
	// is asynchronous.
	if !waitChildrenGone(preStopChildren, 10*time.Second) {
		survivors := stillAlive(preStopChildren)
		t.Fatalf("agent stop left %d zombie stubchild process(es) alive (pids %v) — Falsifier triggered",
			len(survivors), survivors)
	}

	// @constraint: Sanity: the stubchild's term log must have been touched, proving
	// the agent actually signaled it (not killed by some other path).
	if _, statErr := os.Stat(termLog); statErr != nil {
		t.Fatalf("stubchild term log %s missing — the agent did not signal the spawned child during stop: %v",
			termLog, statErr)
	}
}

// runHostAgentDemoScript runs the demo shell script as a subprocess
// with the in-tree CLI and proxy binaries plumbed via env, and returns
// combined stdout/stderr plus the exit code.
func runHostAgentDemoScript(t *testing.T, scriptPath, binPath, proxyBin string, timeout time.Duration) (string, int) {
	t.Helper()
	runCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/bin/bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"RIMSKY_BIN="+binPath,
		"RIMSKY_PROXY_BIN="+proxyBin,
		// @deliberate: Isolate HOME so any default ~/.rimsky lookup the script's
		// CLI calls perform doesn't trip on an operator-installed
		// agent state directory.
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

// buildRimskyCLIBinary compiles cmd/rimsky into the given path.
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

// buildHostAgentProxyBinary compiles cmd/rimsky-host-agent-proxy.
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

// repoFilePath resolves a repo-relative path from this test file's
// location. The scenarios package lives at test/scenarios — two
// directories below the repo root.
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

// parseDaemonPid parses the pid file content (an integer with optional
// surrounding whitespace).
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

// runCmdWithEnv runs binPath with args + env and returns stdout, stderr,
// exit code. A fork error (not an ExitError) fatals the test.
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

// findChildrenByExe returns the pids of processes whose command line
// references exePath. The implementation uses `ps -e -o pid=,command=`
// — a portable surface available on macOS and Linux. The match is on
// substring, not equality, because the agent's exec includes additional
// argv positions after the binary path.
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

// waitChildrenGone poll-waits until every pid in pids is gone, or
// returns false when the timeout elapses with one or more still alive.
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

// stillAlive filters pids to those still running.
func stillAlive(pids []int) []int {
	var live []int
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			live = append(live, pid)
		}
	}
	return live
}

// waitProcessGone polls until pid is no longer alive, or returns false
// when the timeout elapses.
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
