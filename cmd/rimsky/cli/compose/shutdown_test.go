// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @blessed-invariant: spawn-child-reaped-on-exit — the
// TestDrain_SIGTERMThenSIGKILLChildren_BoundedTime case below spawns
// a child binary that ignores SIGTERM, runs the coordinator's Drain,
// and asserts the child is dead within the SIGTERM-then-SIGKILL
// grace window. Without the SIGKILL escalation OR the post-kill
// wait, the test fails on either the process-still-alive check or
// the duration bound — both falsifier surfaces named in the spec.
package compose_test

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// buildSigtermIgnorer compiles a tiny Go program that traps SIGTERM
// and continues running for 10 minutes, so the only path off the
// process is SIGKILL (or natural timeout, which a passing test never
// reaches). Returns the absolute path of the built binary.
func buildSigtermIgnorer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte(sigtermIgnorerSource), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	bin := filepath.Join(dir, "sigterm-ignorer")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, srcPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return bin
}

// sigtermIgnorerSource is the real source that buildSigtermIgnorer
// compiles. The child binds RIMSKY_AGENT_PORT (so SpawnService's
// readiness probe succeeds) and installs a handler that absorbs
// SIGTERM. The only way to terminate the process from outside is
// SIGKILL — exactly the path the coordinator's drain must take.
const sigtermIgnorerSource = `package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	port := os.Getenv("RIMSKY_AGENT_PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "RIMSKY_AGENT_PORT missing")
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(2)
	}
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for sig := range ch {
			fmt.Fprintln(os.Stderr, "child ignoring:", sig)
		}
	}()
	time.Sleep(10 * time.Minute)
}
`

// TestDrain_SIGTERMThenSIGKILLChildren_BoundedTime is the load-bearing
// test for @blessed-invariant: spawn-child-reaped-on-exit. The child
// ignores SIGTERM so cooperation alone cannot drain it; the
// coordinator must escalate to SIGKILL within the grace window and
// observe the child exited before Drain returns. The duration bound
// pins the upper edge — without it, a 60-second drain would still
// satisfy the "child gone" check.
func TestDrain_SIGTERMThenSIGKILLChildren_BoundedTime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM semantics differ on Windows; drain path is exercised on Unix-only CI")
	}
	bin := buildSigtermIgnorer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spawned, err := hostagent.SpawnService(ctx, hostagent.SpawnServiceParams{
		BinaryPath:   bin,
		Env:          os.Environ(),
		ReadyTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("SpawnService: %v", err)
	}
	pid := spawned.Cmd.Process.Pid

	coord := &compose.ShutdownCoordinator{
		Services: []*hostagent.SpawnedService{spawned},
		Logger:   slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	start := time.Now()
	code := coord.Drain(context.Background(), compose.ReasonAllSuccess)
	elapsed := time.Since(start)

	// @constraint: Drain must escalate SIGTERM -> SIGKILL within the
	// 5s grace window plus signal-delivery slack; the 8s bound
	// tolerates scheduler jitter on a loaded CI host while still
	// failing if the coordinator does not escalate at all.
	if elapsed > 8*time.Second {
		t.Fatalf("drain took %v, want <= 8s (grace window + slack)", elapsed)
	}

	// @constraint: signal 0 against a dead pid returns ESRCH (no such
	// process); some POSIX implementations return EPERM after reap if
	// pid recycling has not yet happened. Both outcomes are accepted
	// as "no longer a live signal target".
	if processStillAlive(pid) {
		t.Fatalf("pid %d still alive after Drain (elapsed %v)", pid, elapsed)
	}

	if code != 0 {
		t.Errorf("Drain code = %d, want 0 for ReasonAllSuccess", code)
	}
}

// processStillAlive sends signal 0 to pid and reports whether the
// kernel says the process is still a valid signal target. On a
// reaped process this returns false.
func processStillAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

// TestDrain_AllSuccessReturnsZero exercises the @decision: exit-codes
// table for the happy path: no spawns, no failures, reason all-
// success → exit 0. Empty Services slice exercises the no-op branch
// of reapSpawnedChildren so the drain returns immediately.
func TestDrain_AllSuccessReturnsZero(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if got := coord.Drain(context.Background(), compose.ReasonAllSuccess); got != 0 {
		t.Errorf("Drain(AllSuccess) = %d, want 0", got)
	}
}

// TestDrain_AnyFailureReturnsOne maps the failure reason to exit 1
// per the spec's exit-code table. The script-friendly-outcome story
// (STORY-script-friendly-outcome) depends on this distinct code.
func TestDrain_AnyFailureReturnsOne(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if got := coord.Drain(context.Background(), compose.ReasonAnyFailure); got != 1 {
		t.Errorf("Drain(AnyFailure) = %d, want 1", got)
	}
}

// TestDrain_TimeoutReturnsTwo maps the timeout reason to exit 2.
func TestDrain_TimeoutReturnsTwo(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if got := coord.Drain(context.Background(), compose.ReasonTimeout); got != 2 {
		t.Errorf("Drain(Timeout) = %d, want 2", got)
	}
}

// TestDrain_SignalReturnsOneThirty maps the signal reason to exit
// 130 (the conventional SIGINT exit). The verb's signal-handling
// path classifies a SIGINT/SIGTERM during the wait as
// ReasonSignal; Drain returns 130 so a parent shell sees the same
// exit code it would have observed if the verb had not installed
// its own signal handler.
func TestDrain_SignalReturnsOneThirty(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if got := coord.Drain(context.Background(), compose.ReasonSignal); got != 130 {
		t.Errorf("Drain(Signal) = %d, want 130", got)
	}
}

// TestDrain_Idempotent confirms the once-guard: a second Drain call
// returns the cached exit code without re-running the drain. The
// second-SIGINT escalator and the natural-completion drain may both
// fire on a fast shutdown; the guard prevents a double-reap that
// would re-send signals to children whose pids may have been
// recycled by the kernel.
func TestDrain_Idempotent(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	first := coord.Drain(context.Background(), compose.ReasonAnyFailure)
	second := coord.Drain(context.Background(), compose.ReasonAllSuccess)
	if first != 1 {
		t.Errorf("first Drain = %d, want 1", first)
	}
	if second != first {
		t.Errorf("second Drain = %d, want cached %d", second, first)
	}
}
