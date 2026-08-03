// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/internal/bundledwire"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const shutdownDeadline = 30 * time.Second

type Role string

const (
	RoleScheduler  Role = "rimsky-scheduler"
	RoleSupervisor Role = "rimsky-supervisor"
	RoleControlAPI Role = "rimsky-control-api"
)

func (r Role) OwnsMigration() bool { return r == RoleControlAPI }

var roles = []string{string(RoleScheduler), string(RoleSupervisor), string(RoleControlAPI)}

var binaryDir = "/usr/local/bin"

type LaunchPlan struct {
	Roles        []string
	Topology     persistence.Topology
	MigrateOwner bool
}

// @decision: image-entrypoint-role-selection
func newLaunchPlan(args []string) (LaunchPlan, error) {
	selected, err := selectRoles(args)
	if err != nil {
		return LaunchPlan{}, err
	}
	migrateOwner, err := shouldMigrate(selected)
	if err != nil {
		return LaunchPlan{}, err
	}
	topology := persistence.TopologySplit
	if len(args) == 0 {
		topology = persistence.TopologyUnified
	}
	return LaunchPlan{Roles: selected, Topology: topology, MigrateOwner: migrateOwner}, nil
}

func selectRoles(args []string) ([]string, error) {
	if len(args) == 0 {
		return roles, nil
	}
	if len(args) > 1 {
		return nil, fmt.Errorf("rimsky-entrypoint accepts at most one role argument; got %d (%v); valid roles: %s",
			len(args), args, strings.Join(roles, ", "))
	}
	role := args[0]
	for _, known := range roles {
		if role == known {
			return []string{role}, nil
		}
	}
	return nil, fmt.Errorf("unknown role %q; valid roles: %s", role, strings.Join(roles, ", "))
}

func shouldMigrate(selected []string) (bool, error) {
	switch v := os.Getenv("RIMSKY_ENTRYPOINT_MIGRATE"); v {
	case "":
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid RIMSKY_ENTRYPOINT_MIGRATE=%q: must be \"1\" (force migrate), \"0\" (skip migrate), or unset", v)
	}
	if len(selected) == len(roles) {
		return true, nil
	}
	return len(selected) == 1 && Role(selected[0]).OwnsMigration(), nil
}

func newLeveledHandler() slog.Handler {
	level := shared.ParseLogLevel(os.Getenv("RIMSKY_LOG_LEVEL"))
	return slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
}

func main() {
	slog.SetDefault(slog.New(newLeveledHandler()).With("binary", "entrypoint"))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	args := os.Args[1:]
	plan, err := newLaunchPlan(args)
	if err != nil {
		slog.Error("invalid launch arguments", "err", err)
		os.Exit(2)
	}
	slog.Info("selected roles", "roles", plan.Roles)

	if plan.Topology.Unified() {
		if err := os.Setenv(persistence.ProcessRoleEnv, string(persistence.TopologyUnified)); err != nil {
			slog.Error("set RIMSKY_PROCESS_ROLE", "err", err)
			os.Exit(1)
		}
		runMigrateIfOwned(plan, sigCh)
		runUnified(sigCh)
		return
	}

	runMigrateIfOwned(plan, sigCh)
	runSingleRole(plan.Roles[0], sigCh)
}

func runMigrateIfOwned(plan LaunchPlan, sigCh <-chan os.Signal) {
	if !plan.MigrateOwner {
		slog.Info("skipping migrations for this role", "roles", plan.Roles)
		return
	}
	slog.Info("running migrations")
	cmd, exitCh, err := startOnce("rimsky-migrate")
	if err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	select {
	case sig := <-sigCh:
		slog.Info("received signal during migrate; shutting down", "signal", sig.String())
		shutdownChild(cmd, exitCh, sigCh)
		os.Exit(0)
	case ce := <-exitCh:
		if ce.err != nil {
			slog.Error("migrate failed", "err", ce.err)
			os.Exit(1)
		}
	}
	slog.Info("migrations complete")
}

func runUnified(sigCh <-chan os.Signal) {
	base := slog.New(newLeveledHandler())

	ctx := context.Background()

	driver, cfg, err := launch.OpenDriverFromEnv(ctx, base)
	if err != nil {
		slog.Error("open persistence driver", "err", err)
		os.Exit(1)
	}
	closeDriver := func() { _ = driver.Close() }

	bundledRegs, err := bundledwire.CollectBundled(ctx, base.With("role", "bundled"))
	if err != nil {
		slog.Error("bundled service registration failed", "err", err)
		closeDriver()
		os.Exit(1)
	}

	stack, err := launch.StartUnifiedStack(ctx, base, driver, cfg, bundledRegs)
	if err != nil {
		slog.Error("role failed to start", "err", err)
		closeDriver()
		os.Exit(1)
	}

	drain := func(code int) {
		drained := make(chan struct{})
		defer close(drained)
		installHardExitOnSecondSignal(sigCh, drained, nil)
		stack.Drain(context.Background(), shutdownDeadline)
		closeDriver()
		os.Exit(code)
	}

	select {
	case sig := <-sigCh:
		slog.Info("received signal; shutting down", "signal", sig.String())
		drain(0)
	case rf := <-stack.FailCh():
		slog.Error("role failed; shutting down", "role", rf.Role, "err", rf.Err)
		drain(1)
	}
}

func runSingleRole(name string, sigCh <-chan os.Signal) {
	cmd, exitCh, err := spawnRole(name)
	if err != nil {
		slog.Error("spawn failed", "err", err)
		os.Exit(1)
	}

	select {
	case sig := <-sigCh:
		slog.Info("received signal; shutting down", "signal", sig.String())
		shutdownChild(cmd, exitCh, sigCh)
		os.Exit(0)
	case ce := <-exitCh:
		if ce.err == nil {
			slog.Info("child exited", "binary", ce.name)
		} else {
			slog.Error("child exited unexpectedly", "binary", ce.name, "err", ce.err)
		}
		os.Exit(exitCode(ce.err))
	}
}

func spawnRole(name string) (*exec.Cmd, chan childExit, error) {
	return spawn(name, envWithoutProcessRole())
}

func startOnce(binary string) (*exec.Cmd, chan childExit, error) {
	return spawn(binary, os.Environ())
}

func spawn(binary string, baseEnv []string) (*exec.Cmd, chan childExit, error) {
	c := exec.Command(binaryDir + "/" + binary)
	c.Env = append(baseEnv, "RIMSKY_LOG_BINARY="+nameOf(binary))
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return nil, nil, fmt.Errorf("spawn %s: %w", binary, err)
	}
	exitCh := make(chan childExit, 1)
	go func() {
		err := c.Wait()
		exitCh <- childExit{name: binary, err: err}
	}()
	return c, exitCh, nil
}

// @decision: graceful-shutdown
func shutdownChild(cmd *exec.Cmd, exitCh chan childExit, sigCh <-chan os.Signal) {
	if cmd.Process == nil {
		return
	}
	drained := make(chan struct{})
	defer close(drained)
	installHardExitOnSecondSignal(sigCh, drained, cmd.Process)

	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exitCh:
	case <-time.After(shutdownDeadline):
		_ = cmd.Process.Kill()
	}
}

// @decision: graceful-shutdown
func installHardExitOnSecondSignal(sigCh <-chan os.Signal, drained <-chan struct{}, child *os.Process) {
	shared.InstallSecondSignalHardExit(sigCh, drained, shared.NewSlogLogger(slog.Default()), func() {
		if child != nil {
			_ = child.Kill()
		}
		os.Exit(shared.HardExitCode)
	})
}

func envWithoutProcessRole() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	prefix := persistence.ProcessRoleEnv + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func nameOf(binary string) string {
	return strings.TrimPrefix(binary, "rimsky-")
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

type childExit struct {
	name string
	err  error
}
