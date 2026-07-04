// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
)

const shutdownDeadline = 30 * time.Second

var roles = []string{"rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api"}

var binaryDir = "/usr/local/bin"

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
	return len(selected) == 1 && selected[0] == "rimsky-control-api", nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("binary", "entrypoint"))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	args := os.Args[1:]
	selected, err := selectRoles(args)
	if err != nil {
		slog.Error("invalid role argument", "err", err)
		os.Exit(2)
	}
	slog.Info("selected roles", "roles", selected)

	if len(args) == 0 {
		if err := os.Setenv("RIMSKY_PROCESS_ROLE", "unified"); err != nil {
			slog.Error("set RIMSKY_PROCESS_ROLE", "err", err)
			os.Exit(1)
		}
		runMigrateIfOwned(selected, sigCh)
		runUnified(sigCh)
		return
	}

	runMigrateIfOwned(selected, sigCh)
	runSingleRole(selected[0], sigCh)
}

func runMigrateIfOwned(selected []string, sigCh <-chan os.Signal) {
	migrate, err := shouldMigrate(selected)
	if err != nil {
		slog.Error("invalid migrate override", "err", err)
		os.Exit(2)
	}
	if !migrate {
		slog.Info("skipping migrations for this role", "roles", selected)
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
		shutdownChild(cmd, exitCh)
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
	level := parseLogLevel(os.Getenv("RIMSKY_LOG_LEVEL"))
	base := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

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

	select {
	case sig := <-sigCh:
		slog.Info("received signal; shutting down", "signal", sig.String())
		stack.Drain(context.Background(), shutdownDeadline)
		closeDriver()
		os.Exit(0)
	case rf := <-stack.FailCh():
		slog.Error("role failed; shutting down", "role", rf.Role, "err", rf.Err)
		stack.Drain(context.Background(), shutdownDeadline)
		closeDriver()
		os.Exit(1)
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
		shutdownChild(cmd, exitCh)
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
	c := exec.Command(binaryDir + "/" + name)
	c.Env = append(envWithoutProcessRole(), "RIMSKY_LOG_BINARY="+nameOf(name))
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return nil, nil, fmt.Errorf("spawn %s: %w", name, err)
	}
	exitCh := make(chan childExit, 1)
	go func() {
		err := c.Wait()
		exitCh <- childExit{name: name, err: err}
	}()
	return c, exitCh, nil
}

func startOnce(binary string) (*exec.Cmd, chan childExit, error) {
	c := exec.Command(binaryDir + "/" + binary)
	c.Env = append(os.Environ(), "RIMSKY_LOG_BINARY="+nameOf(binary))
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", binary, err)
	}
	exitCh := make(chan childExit, 1)
	go func() {
		err := c.Wait()
		exitCh <- childExit{name: binary, err: err}
	}()
	return c, exitCh, nil
}

func shutdownChild(cmd *exec.Cmd, exitCh chan childExit) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exitCh:
	case <-time.After(shutdownDeadline):
		_ = cmd.Process.Kill()
	}
}

func envWithoutProcessRole() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "RIMSKY_PROCESS_ROLE=") {
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

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type childExit struct {
	name string
	err  error
}
