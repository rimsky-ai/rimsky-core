// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func writeFixtureBinary(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestNewLeveledHandler_HonorsLogLevel(t *testing.T) {
	t.Setenv("RIMSKY_LOG_LEVEL", "error")
	h := newLeveledHandler()
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("newLeveledHandler() with RIMSKY_LOG_LEVEL=error still enables Info; the entrypoint's own logger ignores the level knob")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("newLeveledHandler() with RIMSKY_LOG_LEVEL=error should enable Error")
	}
}

func TestNameOf(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"rimsky-scheduler", "scheduler"},
		{"rimsky-migrate", "migrate"},
		{"rimsky-control-api", "control-api"},
		{"weird-name", "weird-name"},
	} {
		if got := nameOf(tc.in); got != tc.want {
			t.Errorf("nameOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStartOnce_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}
	dir := t.TempDir()
	writeFixtureBinary(t, dir, "rimsky-migrate", `echo "migrate ok"; exit 0`)
	t.Cleanup(func() { binaryDir = "/usr/local/bin" })
	binaryDir = dir
	_, exitCh, err := startOnce("rimsky-migrate")
	if err != nil {
		t.Fatalf("startOnce: %v", err)
	}
	ce := <-exitCh
	if ce.err != nil {
		t.Fatalf("migrate fixture exit: %v", ce.err)
	}
}

func TestStartOnce_FailurePropagates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}
	dir := t.TempDir()
	writeFixtureBinary(t, dir, "rimsky-migrate", `exit 7`)
	t.Cleanup(func() { binaryDir = "/usr/local/bin" })
	binaryDir = dir
	_, exitCh, err := startOnce("rimsky-migrate")
	if err != nil {
		t.Fatalf("startOnce: %v", err)
	}
	ce := <-exitCh
	if ce.err == nil {
		t.Fatal("startOnce exit channel should have carried an error for non-zero exit")
	}
	if ee, ok := ce.err.(*exec.ExitError); ok {
		if ee.ExitCode() != 7 {
			t.Fatalf("exit code = %d, want 7", ee.ExitCode())
		}
	} else {
		t.Fatalf("err = %T, want *exec.ExitError", ce.err)
	}
}

func TestRunMigrateIfOwned_SignalInterrupts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}
	if os.Getenv("ENTRYPOINT_TEST_MIGRATE_SIGNAL") == "1" {
		dir := os.Getenv("ENTRYPOINT_TEST_FIXTURE_DIR")
		binaryDir = dir
		sigCh := make(chan os.Signal, 1)
		sigCh <- syscall.SIGTERM
		plan, err := newLaunchPlan([]string{"rimsky-control-api"})
		if err != nil {
			os.Exit(2)
		}
		runMigrateIfOwned(plan, sigCh)
		os.Exit(99)
	}

	dir := t.TempDir()
	writeFixtureBinary(t, dir, "rimsky-migrate", `exec sleep 60`)

	cmd := exec.Command(os.Args[0], "-test.run", "TestRunMigrateIfOwned_SignalInterrupts")
	cmd.Env = append(os.Environ(),
		"ENTRYPOINT_TEST_MIGRATE_SIGNAL=1",
		"ENTRYPOINT_TEST_FIXTURE_DIR="+dir,
		"RIMSKY_ENTRYPOINT_MIGRATE=1",
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("interrupted migrate should exit 0, got: %v", err)
	}
}

func TestExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}
	if got := exitCode(nil); got != 0 {
		t.Errorf("exitCode(nil) = %d, want 0", got)
	}

	dir := t.TempDir()
	writeFixtureBinary(t, dir, "exit12", `exit 12`)
	c := exec.Command(filepath.Join(dir, "exit12"))
	err := c.Run()
	if got := exitCode(err); got != 12 {
		t.Errorf("exitCode(%v) = %d, want 12", err, got)
	}
}

func TestEntrypointRoleSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}

	allThree := []string{"rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api"}

	t.Run("mapping", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			args    []string
			want    []string
			wantErr bool
		}{
			{"no args selects all three", nil, allThree, false},
			{"empty args selects all three", []string{}, allThree, false},
			{"single scheduler", []string{"rimsky-scheduler"}, []string{"rimsky-scheduler"}, false},
			{"single supervisor", []string{"rimsky-supervisor"}, []string{"rimsky-supervisor"}, false},
			{"single control-api", []string{"rimsky-control-api"}, []string{"rimsky-control-api"}, false},
			{"unknown role errors", []string{"bogus"}, nil, true},
			{"unknown role rimsky-migrate is not a runtime role", []string{"rimsky-migrate"}, nil, true},
			{"too many args errors", []string{"rimsky-scheduler", "rimsky-supervisor"}, nil, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := selectRoles(tc.args)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("selectRoles(%v) = %v, want error", tc.args, got)
					}
					return
				}
				if err != nil {
					t.Fatalf("selectRoles(%v) returned error: %v", tc.args, err)
				}
				if !equalStrings(got, tc.want) {
					t.Fatalf("selectRoles(%v) = %v, want %v", tc.args, got, tc.want)
				}
			})
		}
	})

	t.Run("single role spawns only that role", func(t *testing.T) {
		dir := t.TempDir()
		markerDir := t.TempDir()
		for _, n := range allThree {
			writeFixtureBinary(t, dir, n,
				`touch "`+filepath.Join(markerDir, n)+`"; exec sleep 60`)
		}
		t.Cleanup(func() { binaryDir = "/usr/local/bin" })
		binaryDir = dir

		cmd, exitCh, err := spawnRole("rimsky-scheduler")
		if err != nil {
			t.Fatalf("spawnRole: %v", err)
		}
		t.Cleanup(func() { terminateAndReap(cmd, exitCh) })

		waitForFile(filepath.Join(markerDir, "rimsky-scheduler"))

		for _, n := range []string{"rimsky-supervisor", "rimsky-control-api"} {
			if _, err := os.Stat(filepath.Join(markerDir, n)); err == nil {
				t.Fatalf("role %q was spawned but only rimsky-scheduler should run", n)
			}
		}
	})

	t.Run("single role child does not get RIMSKY_PROCESS_ROLE=unified", func(t *testing.T) {
		dir := t.TempDir()
		envFile := filepath.Join(t.TempDir(), "env-dump")
		writeFixtureBinary(t, dir, "rimsky-scheduler",
			`env > "`+envFile+`.tmp" && mv "`+envFile+`.tmp" "`+envFile+`"; exec sleep 60`)
		t.Cleanup(func() { binaryDir = "/usr/local/bin" })
		binaryDir = dir

		cmd, exitCh, err := spawnRole("rimsky-scheduler")
		if err != nil {
			t.Fatalf("spawnRole: %v", err)
		}
		t.Cleanup(func() { terminateAndReap(cmd, exitCh) })

		waitForFile(envFile)
		dump, err := os.ReadFile(envFile)
		if err != nil {
			t.Fatalf("read env dump: %v", err)
		}
		if strings.Contains(string(dump), "RIMSKY_PROCESS_ROLE=unified") {
			t.Fatal("spawned single-role child carries RIMSKY_PROCESS_ROLE=unified; the marker belongs only to the single-process no-command path")
		}
		if !strings.Contains(string(dump), "RIMSKY_LOG_BINARY=scheduler") {
			t.Fatal("spawned single-role child is missing RIMSKY_LOG_BINARY=scheduler")
		}
	})
}

func terminateAndReap(cmd *exec.Cmd, exitCh chan childExit) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	<-exitCh
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func waitForFile(path string) {
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestShouldMigrate(t *testing.T) {
	allThree := []string{"rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api"}

	t.Run("default rules (no env override)", func(t *testing.T) {
		t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", "")

		for _, tc := range []struct {
			name     string
			selected []string
			want     bool
		}{
			{"all-in-one (no args path) migrates", allThree, true},
			{"single rimsky-control-api migrates", []string{"rimsky-control-api"}, true},
			{"single rimsky-scheduler does NOT migrate", []string{"rimsky-scheduler"}, false},
			{"single rimsky-supervisor does NOT migrate", []string{"rimsky-supervisor"}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := shouldMigrate(tc.selected)
				if err != nil {
					t.Fatalf("shouldMigrate(%v) returned error: %v", tc.selected, err)
				}
				if got != tc.want {
					t.Fatalf("shouldMigrate(%v) = %v, want %v", tc.selected, got, tc.want)
				}
			})
		}
	})

	t.Run("RIMSKY_ENTRYPOINT_MIGRATE=1 forces migrate everywhere", func(t *testing.T) {
		t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", "1")
		for _, sel := range [][]string{
			allThree,
			{"rimsky-control-api"},
			{"rimsky-scheduler"},
			{"rimsky-supervisor"},
		} {
			got, err := shouldMigrate(sel)
			if err != nil {
				t.Fatalf("shouldMigrate(%v) returned error: %v", sel, err)
			}
			if !got {
				t.Errorf("shouldMigrate(%v) with =1 override = false, want true", sel)
			}
		}
	})

	t.Run("RIMSKY_ENTRYPOINT_MIGRATE=0 skips migrate everywhere", func(t *testing.T) {
		t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", "0")
		for _, sel := range [][]string{
			allThree,
			{"rimsky-control-api"},
			{"rimsky-scheduler"},
			{"rimsky-supervisor"},
		} {
			got, err := shouldMigrate(sel)
			if err != nil {
				t.Fatalf("shouldMigrate(%v) returned error: %v", sel, err)
			}
			if got {
				t.Errorf("shouldMigrate(%v) with =0 override = true, want false", sel)
			}
		}
	})

	t.Run("unrecognized RIMSKY_ENTRYPOINT_MIGRATE values error", func(t *testing.T) {
		for _, v := range []string{"true", "false", "yes", "no", "2", " 1", "on"} {
			t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", v)
			for _, sel := range [][]string{allThree, {"rimsky-control-api"}, {"rimsky-scheduler"}} {
				got, err := shouldMigrate(sel)
				if err == nil {
					t.Errorf("shouldMigrate(%v) with RIMSKY_ENTRYPOINT_MIGRATE=%q = %v, want error", sel, v, got)
					continue
				}
				if !strings.Contains(err.Error(), v) {
					t.Errorf("shouldMigrate error %q does not name the offending value %q", err.Error(), v)
				}
			}
		}
	})

	t.Run("three-container split migrates exactly once", func(t *testing.T) {
		t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", "")
		count := 0
		for _, role := range allThree {
			got, err := shouldMigrate([]string{role})
			if err != nil {
				t.Fatalf("shouldMigrate([%q]) returned error: %v", role, err)
			}
			if got {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("three-container split: %d roles migrate, want exactly 1", count)
		}
	})
}

func TestRoleOwnsMigration(t *testing.T) {
	for _, tc := range []struct {
		role Role
		want bool
	}{
		{RoleScheduler, false},
		{RoleSupervisor, false},
		{RoleControlAPI, true},
	} {
		if got := tc.role.OwnsMigration(); got != tc.want {
			t.Errorf("Role(%q).OwnsMigration() = %v, want %v", tc.role, got, tc.want)
		}
	}
}

func TestNewLaunchPlan(t *testing.T) {
	t.Run("no args: unified topology, all roles, migrate owner", func(t *testing.T) {
		t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", "")
		plan, err := newLaunchPlan(nil)
		if err != nil {
			t.Fatalf("newLaunchPlan(nil): %v", err)
		}
		if plan.Topology != persistence.TopologyUnified {
			t.Errorf("Topology = %q, want %q", plan.Topology, persistence.TopologyUnified)
		}
		if !equalStrings(plan.Roles, []string{"rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api"}) {
			t.Errorf("Roles = %v, want all three", plan.Roles)
		}
		if !plan.MigrateOwner {
			t.Error("MigrateOwner = false, want true for the all-in-one plan")
		}
	})

	t.Run("single control-api arg: split topology, migrate owner", func(t *testing.T) {
		t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", "")
		plan, err := newLaunchPlan([]string{"rimsky-control-api"})
		if err != nil {
			t.Fatalf("newLaunchPlan: %v", err)
		}
		if plan.Topology != persistence.TopologySplit {
			t.Errorf("Topology = %q, want %q", plan.Topology, persistence.TopologySplit)
		}
		if !plan.MigrateOwner {
			t.Error("MigrateOwner = false, want true for the control-api role")
		}
	})

	t.Run("single scheduler arg: split topology, not migrate owner", func(t *testing.T) {
		t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", "")
		plan, err := newLaunchPlan([]string{"rimsky-scheduler"})
		if err != nil {
			t.Fatalf("newLaunchPlan: %v", err)
		}
		if plan.Topology != persistence.TopologySplit {
			t.Errorf("Topology = %q, want %q", plan.Topology, persistence.TopologySplit)
		}
		if plan.MigrateOwner {
			t.Error("MigrateOwner = true, want false for the scheduler-only role")
		}
	})

	t.Run("unknown role propagates the selectRoles error", func(t *testing.T) {
		if _, err := newLaunchPlan([]string{"bogus"}); err == nil {
			t.Fatal("newLaunchPlan([\"bogus\"]) = nil error, want error")
		}
	})

	t.Run("invalid migrate override propagates the shouldMigrate error", func(t *testing.T) {
		t.Setenv("RIMSKY_ENTRYPOINT_MIGRATE", "yes")
		if _, err := newLaunchPlan([]string{"rimsky-control-api"}); err == nil {
			t.Fatal("newLaunchPlan with an invalid override = nil error, want error")
		}
	})
}

func TestRunMigrateIfOwned_InvalidOverrideExitsNonZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}
	if os.Getenv("ENTRYPOINT_TEST_MIGRATE_INVALID") == "1" {
		binaryDir = os.Getenv("ENTRYPOINT_TEST_FIXTURE_DIR")
		sigCh := make(chan os.Signal, 1)
		plan, err := newLaunchPlan([]string{"rimsky-control-api"})
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid migrate override: %v\n", err)
			os.Exit(2)
		}
		runMigrateIfOwned(plan, sigCh)
		os.Exit(0)
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "migrate-ran")
	writeFixtureBinary(t, dir, "rimsky-migrate", `touch "`+marker+`"; exit 0`)

	cmd := exec.Command(os.Args[0], "-test.run", "TestRunMigrateIfOwned_InvalidOverrideExitsNonZero")
	cmd.Env = append(os.Environ(),
		"ENTRYPOINT_TEST_MIGRATE_INVALID=1",
		"ENTRYPOINT_TEST_FIXTURE_DIR="+dir,
		"RIMSKY_ENTRYPOINT_MIGRATE=yes",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid RIMSKY_ENTRYPOINT_MIGRATE should exit non-zero; output:\n%s", out)
	}
	if !strings.Contains(string(out), "RIMSKY_ENTRYPOINT_MIGRATE") || !strings.Contains(string(out), "yes") {
		t.Fatalf("error output should name the variable and the offending value %q; output:\n%s", "yes", out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("migrate child ran despite the invalid override; the error must fire before the store is touched")
	}
}

func TestShutdownChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}
	dir := t.TempDir()
	writeFixtureBinary(t, dir, "rimsky-scheduler", `exec sleep 60`)
	t.Cleanup(func() { binaryDir = "/usr/local/bin" })
	binaryDir = dir

	cmd, exitCh, err := spawnRole("rimsky-scheduler")
	if err != nil {
		t.Fatalf("spawnRole: %v", err)
	}

	shutdownChild(cmd, exitCh, make(chan os.Signal))

	if cmd.ProcessState == nil {
		t.Fatal("child was not reaped by shutdownChild")
	}
	if sig := terminatingSignal(cmd.ProcessState); sig != syscall.SIGTERM {
		t.Fatalf("child was terminated by %v, want SIGTERM; shutdownChild fell through to the hard-kill deadline instead of shutting the child down gracefully", sig)
	}
}

func terminatingSignal(state *os.ProcessState) syscall.Signal {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0
	}
	return status.Signal()
}

const hardExitHelperEnv = "ENTRYPOINT_HARD_EXIT_HELPER_DIR"

// @decision: graceful-shutdown
func TestShutdownChildHardExitsOnSecondSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures unavailable on windows")
	}
	if dir := os.Getenv(hardExitHelperEnv); dir != "" {
		runHardExitHelper(dir)
		return
	}

	dir := t.TempDir()
	blockFifo := filepath.Join(dir, "block.fifo")
	if out, err := exec.Command("mkfifo", blockFifo).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo %s: %v: %s", blockFifo, err, out)
	}
	writeFixtureBinary(t, dir, "rimsky-scheduler", strings.Join([]string{
		"exec >/dev/null 2>&1",
		"trap '' TERM INT",
		": > " + hardExitReadyPath(dir),
		"read line < " + blockFifo,
	}, "\n"))

	helper := exec.Command(os.Args[0], "-test.run=^TestShutdownChildHardExitsOnSecondSignal$")
	helper.Env = append(os.Environ(), hardExitHelperEnv+"="+dir)
	err := helper.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper should have hard-exited on the second signal; got err=%v", err)
	}
	if exitErr.ExitCode() != shared.HardExitCode {
		t.Errorf("second signal exit code = %d, want %d", exitErr.ExitCode(), shared.HardExitCode)
	}
}

func hardExitReadyPath(dir string) string { return filepath.Join(dir, "signals-ignored") }

func runHardExitHelper(dir string) {
	binaryDir = dir
	cmd, exitCh, err := spawnRole("rimsky-scheduler")
	if err != nil {
		os.Exit(3)
	}
	waitForFile(hardExitReadyPath(dir))

	sigCh := make(chan os.Signal, 1)
	sigCh <- syscall.SIGINT
	shutdownChild(cmd, exitCh, sigCh)
	blockUntilHardExitWins()
}

func blockUntilHardExitWins() { select {} }
