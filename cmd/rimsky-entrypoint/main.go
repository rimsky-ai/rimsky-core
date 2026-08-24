// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/internal/bundledwire"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

type Role string

const (
	RoleScheduler  Role = "rimsky-scheduler"
	RoleSupervisor Role = "rimsky-supervisor"
	RoleControlAPI Role = "rimsky-control-api"
)

func (r Role) OwnsMigration() bool { return r == RoleControlAPI }

var roles = []string{string(RoleScheduler), string(RoleSupervisor), string(RoleControlAPI)}

const defaultBinaryDir = "/usr/local/bin"

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

func main() {
	slog.SetDefault(serverkit.NewJSONLogger().With("binary", "entrypoint"))

	sigCh, stopNotify := serverkit.NotifyShutdownSignals()
	defer stopNotify()

	args := os.Args[1:]
	plan, err := newLaunchPlan(args)
	if err != nil {
		slog.Error("ENTRYPOINT.ARGUMENTS.INVALID", "err", err)
		os.Exit(2)
	}
	slog.Info("ENTRYPOINT.ROLES.SELECTED", "roles", plan.Roles)

	if plan.Topology.Unified() {
		if err := os.Setenv(persistence.ProcessRoleEnv, string(persistence.TopologyUnified)); err != nil {
			slog.Error("ENTRYPOINT.PROCESSROLE.SETFAILED", "err", err)
			os.Exit(1)
		}
		runMigrateIfOwned(defaultBinaryDir, plan, sigCh)
		runUnified(sigCh)
		return
	}

	runMigrateIfOwned(defaultBinaryDir, plan, sigCh)
	runSingleRole(defaultBinaryDir, plan.Roles[0], sigCh)
}

func runMigrateIfOwned(binaryDir string, plan LaunchPlan, sigCh <-chan os.Signal) {
	if !plan.MigrateOwner {
		slog.Info("ENTRYPOINT.MIGRATE.SKIPPED", "roles", plan.Roles)
		return
	}
	slog.Info("ENTRYPOINT.MIGRATE.STARTED")
	cmd, exitCh, err := startOnce(binaryDir, "rimsky-migrate")
	if err != nil {
		slog.Error("ENTRYPOINT.MIGRATE.FAILED", "err", err)
		os.Exit(1)
	}
	select {
	case sig := <-sigCh:
		slog.Info("ENTRYPOINT.MIGRATE.INTERRUPTED", "detail", "shutting down", "signal", sig.String())
		shutdownChild(cmd, exitCh, sigCh)
		os.Exit(0)
	case ce := <-exitCh:
		if ce.err != nil {
			slog.Error("ENTRYPOINT.MIGRATE.FAILED", "err", ce.err)
			os.Exit(1)
		}
	}
	slog.Info("ENTRYPOINT.MIGRATE.COMPLETED")
}

func runUnified(sigCh <-chan os.Signal) {
	base := serverkit.NewJSONLogger()

	ctx := context.Background()

	driver, cfg, err := launch.OpenDriverFromEnv(ctx, base)
	if err != nil {
		slog.Error("ENTRYPOINT.PERSISTENCE.OPENFAILED", "err", err)
		os.Exit(1)
	}
	closeDriver := func() { _ = driver.Close() }

	bundledRegs, err := bundledwire.CollectBundled(ctx, base.With("role", "bundled"))
	if err != nil {
		slog.Error("ENTRYPOINT.BUNDLEDSERVICE.REGISTERFAILED", "err", err)
		closeDriver()
		os.Exit(1)
	}

	stack, err := launch.StartUnifiedStack(ctx, base, driver, cfg, bundledRegs)
	if err != nil {
		slog.Error("ENTRYPOINT.ROLE.STARTFAILED", "err", err)
		closeDriver()
		os.Exit(1)
	}

	drain := func(code int) {
		drained := make(chan struct{})
		defer close(drained)
		installHardExitOnSecondSignal(sigCh, drained, nil)
		stack.Drain(context.Background(), serverkit.DeployedCoreGrace)
		closeDriver()
		os.Exit(code)
	}

	select {
	case sig := <-sigCh:
		slog.Info("ENTRYPOINT.PROCESS.SIGNALLED", "detail", "shutting down", "signal", sig.String())
		drain(0)
	case rf := <-stack.FailCh():
		slog.Error("ENTRYPOINT.ROLE.FAILED", "detail", "shutting down", "role", rf.Role, "err", rf.Err)
		drain(1)
	}
}

func runSingleRole(binaryDir string, name string, sigCh <-chan os.Signal) {
	cmd, exitCh, err := spawnRole(binaryDir, name)
	if err != nil {
		slog.Error("ENTRYPOINT.CHILD.SPAWNFAILED", "err", err)
		os.Exit(1)
	}

	select {
	case sig := <-sigCh:
		slog.Info("ENTRYPOINT.PROCESS.SIGNALLED", "detail", "shutting down", "signal", sig.String())
		shutdownChild(cmd, exitCh, sigCh)
		os.Exit(0)
	case ce := <-exitCh:
		if ce.err == nil {
			slog.Info("ENTRYPOINT.CHILD.EXITED", "binary", ce.name)
		} else {
			slog.Error("ENTRYPOINT.CHILD.EXITEDUNEXPECTEDLY", "binary", ce.name, "err", ce.err)
		}
		os.Exit(exitCode(ce.err))
	}
}

func spawnRole(binaryDir string, name string) (*exec.Cmd, chan childExit, error) {
	return spawn(binaryDir, name, envWithoutProcessRole())
}

func startOnce(binaryDir string, binary string) (*exec.Cmd, chan childExit, error) {
	return spawn(binaryDir, binary, os.Environ())
}

func spawn(binaryDir string, binary string, baseEnv []string) (*exec.Cmd, chan childExit, error) {
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
	case <-time.After(serverkit.DeployedCoreGrace):
		_ = cmd.Process.Kill()
	}
}

// @decision: graceful-shutdown
func installHardExitOnSecondSignal(sigCh <-chan os.Signal, drained <-chan struct{}, child *os.Process) {
	serverkit.InstallSecondSignalHardExit(sigCh, drained, slog.Default(), func() {
		if child != nil {
			_ = child.Kill()
		}
		os.Exit(serverkit.HardExitCode)
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
