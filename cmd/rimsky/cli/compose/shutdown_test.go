// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

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

const sigtermIgnorerSource = `package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	port := os.Getenv("RIMSKY_DAEMON_PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "RIMSKY_DAEMON_PORT missing")
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
	receipt := os.Getenv("RIMSKY_TEST_SIGTERM_RECEIPT")
	for sig := range ch {
		fmt.Fprintln(os.Stderr, "child ignoring:", sig)
		if sig == syscall.SIGTERM && receipt != "" {
			if werr := os.WriteFile(receipt, []byte(sig.String()), 0o644); werr != nil {
				fmt.Fprintln(os.Stderr, "receipt:", werr)
			}
		}
	}
}
`

const sigtermCooperativeSource = `package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	port := os.Getenv("RIMSKY_DAEMON_PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "RIMSKY_DAEMON_PORT missing")
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(2)
	}
	for {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		_ = conn.Close()
	}
}
`

func buildSigtermCooperative(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte(sigtermCooperativeSource), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	bin := filepath.Join(dir, "sigterm-cooperative")
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

func TestDrain_GracefulChildExitBeforeDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM semantics differ on Windows; drain path is exercised on Unix-only CI")
	}
	bin := buildSigtermCooperative(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spawned, err := hostdaemon.SpawnService(ctx, hostdaemon.SpawnServiceParams{
		BinaryPath:   bin,
		Env:          os.Environ(),
		ReadyTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("SpawnService: %v", err)
	}
	pid := spawned.Cmd.Process.Pid

	var logBuf syncBuffer
	coord := &compose.ShutdownCoordinator{
		Services: []*hostdaemon.SpawnedService{spawned},
		Logger:   slog.New(slog.NewTextHandler(&logBuf, nil)),
	}

	code := coord.Drain(context.Background(), compose.ReasonAllSuccess)

	if processStillAlive(pid) {
		t.Fatalf("pid %d still alive after Drain returned", pid)
	}
	if code != 0 {
		t.Errorf("Drain code = %d, want 0 for ReasonAllSuccess", code)
	}
	if got := logBuf.String(); strings.Contains(got, "SIGKILL straggler child") {
		t.Errorf("a SIGTERM-default child that exits promptly should never reach the SIGKILL escalation branch; log = %q", got)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestInstallSecondSignalEscalator_HardExitsAndKillsChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM/SIGKILL semantics differ on Windows")
	}
	if os.Getenv("RIMSKY_TEST_SECOND_SIGNAL_ESCALATOR") == "1" {
		child := exec.Command("sleep", "60")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "start child:", err)
			os.Exit(2)
		}
		fmt.Println(child.Process.Pid)
		exited := make(chan struct{})
		go func() {
			_, _ = child.Process.Wait()
			close(exited)
		}()

		sigCh := make(chan os.Signal, 1)
		done := make(chan struct{})
		compose.InstallSecondSignalEscalator(sigCh, done, func() []*hostdaemon.SpawnedService {
			return []*hostdaemon.SpawnedService{{Cmd: child, Exited: exited}}
		}, nil)
		sigCh <- syscall.SIGINT
		<-make(chan struct{})
	}

	cmd := exec.Command(os.Args[0], "-test.run", "TestInstallSecondSignalEscalator_HardExitsAndKillsChildren")
	cmd.Env = append(os.Environ(), "RIMSKY_TEST_SECOND_SIGNAL_ESCALATOR=1")
	out, err := cmd.Output()
	if err == nil {
		t.Fatalf("subprocess should have exited non-zero (130); output: %s", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("subprocess error is not *exec.ExitError: %v", err)
	}
	if exitErr.ExitCode() != 130 {
		t.Fatalf("subprocess exit code = %d, want 130; output: %s", exitErr.ExitCode(), out)
	}

	childPID := strings.TrimSpace(string(out))
	if childPID == "" {
		t.Fatal("subprocess did not print the spawned child's pid")
	}
	pid, convErr := strconv.Atoi(childPID)
	if convErr != nil {
		t.Fatalf("parse child pid %q: %v", childPID, convErr)
	}
	awaited.Until(t,
		fmt.Sprintf("child pid %d to die under the second-signal escalator's SIGKILL", pid),
		func() bool { return !processStillAlive(pid) })
}

func TestDrain_SIGKILLsAChildThatOutlivesTheGraceWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM semantics differ on Windows; drain path is exercised on Unix-only CI")
	}
	bin := buildSigtermIgnorer(t)
	receipt := filepath.Join(t.TempDir(), "sigterm-received")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spawned, err := hostdaemon.SpawnService(ctx, hostdaemon.SpawnServiceParams{
		BinaryPath:   bin,
		Env:          append(os.Environ(), "RIMSKY_TEST_SIGTERM_RECEIPT="+receipt),
		ReadyTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("SpawnService: %v", err)
	}
	pid := spawned.Cmd.Process.Pid

	graceOver := make(chan time.Time)
	coord := &compose.ShutdownCoordinator{
		Services:   []*hostdaemon.SpawnedService{spawned},
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		ChildGrace: func() (<-chan time.Time, func()) { return graceOver, func() {} },
	}

	drained := make(chan int, 1)
	go func() { drained <- coord.Drain(context.Background(), compose.ReasonAllSuccess) }()

	awaited.Until(t, fmt.Sprintf("child %d to record the drain's SIGTERM", pid),
		func() bool { _, statErr := os.Stat(receipt); return statErr == nil })
	if !processStillAlive(pid) {
		t.Fatalf("pid %d died on the drain's SIGTERM; this test needs a child that outlives it", pid)
	}
	select {
	case code := <-drained:
		t.Fatalf("Drain returned %d before the grace window elapsed, with the child still alive — "+
			"a straggler gets the whole window before the SIGKILL escalation", code)
	default:
	}
	close(graceOver)

	code := <-drained

	if processStillAlive(pid) {
		t.Fatalf("pid %d still alive after Drain returned (a SIGTERM-resistant child must be confirmed dead, via SIGKILL escalation, before Drain returns)", pid)
	}

	if code != 0 {
		t.Errorf("Drain code = %d, want 0 for ReasonAllSuccess", code)
	}
}

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

func TestDrain_AllSuccessReturnsZero(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if got := coord.Drain(context.Background(), compose.ReasonAllSuccess); got != 0 {
		t.Errorf("Drain(AllSuccess) = %d, want 0", got)
	}
}

func TestDrain_AnyFailureReturnsOne(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if got := coord.Drain(context.Background(), compose.ReasonAnyFailure); got != 1 {
		t.Errorf("Drain(AnyFailure) = %d, want 1", got)
	}
}

func TestDrain_TimeoutReturnsTwo(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if got := coord.Drain(context.Background(), compose.ReasonTimeout); got != 2 {
		t.Errorf("Drain(Timeout) = %d, want 2", got)
	}
}

func TestDrain_SignalReturnsOneThirty(t *testing.T) {
	coord := &compose.ShutdownCoordinator{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if got := coord.Drain(context.Background(), compose.ReasonSignal); got != 130 {
		t.Errorf("Drain(Signal) = %d, want 130", got)
	}
}

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

// @decision: graceful-shutdown
func TestArmOnFirstSignal_ObservesTheFirstSignalDuringDrainAndHardExitsOnTheSecond(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGINT semantics differ on Windows")
	}
	if os.Getenv("RIMSKY_TEST_ARM_ON_FIRST_SIGNAL") == "1" {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		escalation, stopSignals := compose.NewSignalEscalation(&compose.SpawnedRegistry{}, logger)
		defer stopSignals()

		observed := escalation.ArmOnFirstSignal(logger, "compose run")
		escalation.Signals() <- syscall.SIGINT
		<-observed
		escalation.Signals() <- syscall.SIGINT
		<-make(chan struct{})
	}

	cmd := exec.Command(os.Args[0], "-test.run", "TestArmOnFirstSignal_ObservesTheFirstSignalDuringDrainAndHardExitsOnTheSecond")
	cmd.Env = append(os.Environ(), "RIMSKY_TEST_ARM_ON_FIRST_SIGNAL=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("the second signal did not exit the process: err=%v, stderr=%s", err, stderr.String())
	}
	if exitErr.ExitCode() != 130 {
		t.Fatalf("second-signal exit code = %d, want 130; stderr=%s", exitErr.ExitCode(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "COMPOSE.DRAIN.SIGNALLED") {
		t.Errorf("the first signal during the drain was swallowed rather than observed; stderr=%s", stderr.String())
	}
}
