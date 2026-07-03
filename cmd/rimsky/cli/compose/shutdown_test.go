// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		ReadyTimeout: 30 * time.Second,
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

	if elapsed > 8*time.Second {
		t.Fatalf("drain took %v, want <= 8s (grace window + slack)", elapsed)
	}

	if processStillAlive(pid) {
		t.Fatalf("pid %d still alive after Drain (elapsed %v)", pid, elapsed)
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
