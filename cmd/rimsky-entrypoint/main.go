// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-entrypoint is the unified-image PID-1. With no command argument
// it runs rimsky-migrate synchronously, then runs all three roles
// (scheduler, supervisor, control-api) IN THIS PROCESS via the
// lib/control/launch role runners — the zero-config all-in-one stack is
// genuinely single-process, so the in-process "memory" blob backend is
// actually shared across roles (RIMSKY_PROCESS_ROLE=unified is set only
// on this path, and only here is it true). When given a single role
// argument (e.g. `command: [rimsky-scheduler]`) it spawns ONLY that role
// binary as a child process, so multi-container deploys run one role per
// container with correct cross-container addressing. An unknown role
// argument is rejected loudly. Either way SIGTERM/SIGINT triggers a
// bounded graceful shutdown.
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

	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
)

// shutdownDeadline bounds the graceful-shutdown wait after SIGTERM —
// for the in-process roles' stop handles on the no-command path, and
// for the spawned child before it's SIGKILL'd on the single-role path.
// Distroless containers are typically stopped with `docker stop
// --time=30`; matching that gives shutdown full budget without leaving
// the container hanging.
const shutdownDeadline = 30 * time.Second

// roles is the set of runtime roles: the full in-process set for the
// all-in-one (no-argument) path, and the set of valid single-role
// arguments.
var roles = []string{"rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api"}

// binaryDir is overridden in tests to point at a fixture-binary directory.
var binaryDir = "/usr/local/bin"

// selectRoles maps the entrypoint's command arguments (os.Args[1:]) to
// the list of roles to run:
//   - no args → all three roles (the single-process all-in-one stack).
//   - one arg naming a known runtime role → only that role (spawned as
//     a child process).
//   - anything else (unknown role, rimsky-migrate, or >1 arg) → an error.
//
// rimsky-migrate is deliberately NOT a selectable role: it is a one-shot
// init step, not a long-running process, and the entrypoint runs it
// separately (see shouldMigrate). Returning it here would leave the
// entrypoint supervising a process that exits immediately.
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

// shouldMigrate decides whether this entrypoint invocation runs the
// synchronous rimsky-migrate step. The rule, kept deliberately simple and
// explicit:
//
//   - RIMSKY_ENTRYPOINT_MIGRATE=1 forces migrate; =0 skips it. This lets an
//     operator run a dedicated one-shot migrate container, or suppress migrate
//     in a role container that shares a store with one that already migrated.
//     Any other non-empty value ("true", "yes", a typo) is an error — the
//     caller exits non-zero rather than silently falling through to the
//     default heuristic against the operator's intent.
//   - With no override: the no-arg all-in-one path always migrates (one
//     process owns the whole store). For single-role containers exactly one
//     role — rimsky-control-api — owns schema init, so a three-container
//     deploy migrates once rather than racing three concurrent migrations or
//     never migrating at all.
func shouldMigrate(selected []string) (bool, error) {
	switch v := os.Getenv("RIMSKY_ENTRYPOINT_MIGRATE"); v {
	case "":
		// No override; fall through to the default heuristic below.
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid RIMSKY_ENTRYPOINT_MIGRATE=%q: must be \"1\" (force migrate), \"0\" (skip migrate), or unset", v)
	}
	// No override: all-in-one (all three) migrates; single-role migrates only
	// for the designated control-api role.
	if len(selected) == len(roles) {
		return true, nil
	}
	return len(selected) == 1 && selected[0] == "rimsky-control-api", nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("binary", "entrypoint"))

	// Register the signal handler FIRST — before migrate and before any
	// role starts. As container PID-1 this process gets default-ignored
	// SIGTERM until Notify runs, so a `docker stop` during a long migrate
	// (or slow role startup) would otherwise be silently dropped and the
	// container would hang until SIGKILL. The buffered channel queues a
	// signal received during any startup phase; every later phase
	// (migrate, unified roles, single-role child) consumes this one
	// channel.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Resolve the role argument first so an unknown role fails before we touch
	// the store with a migrate run.
	args := os.Args[1:]
	selected, err := selectRoles(args)
	if err != nil {
		slog.Error("invalid role argument", "err", err)
		os.Exit(2)
	}
	slog.Info("selected roles", "roles", selected)

	if len(args) == 0 {
		// No-command path: the single-process all-in-one. Mark the
		// process as the unified single-process mode BEFORE migrate and
		// role startup — the memory-blob gate
		// (persistence.ValidateBlobConfig) admits the "memory" backend
		// only under this marker, and only this path may set it: here
		// every role shares this one process, so an in-process blob map
		// is genuinely shared.
		if err := os.Setenv("RIMSKY_PROCESS_ROLE", "unified"); err != nil {
			slog.Error("set RIMSKY_PROCESS_ROLE", "err", err)
			os.Exit(1)
		}
		runMigrateIfOwned(selected, sigCh)
		runUnified(sigCh)
		return
	}

	// Single-role path: spawn the role binary as a child process,
	// unchanged from the multi-container contract. RIMSKY_PROCESS_ROLE
	// is NOT set here — a per-role process is not the single-process
	// mode, and the memory-blob gate must reject it.
	runMigrateIfOwned(selected, sigCh)
	runSingleRole(selected[0], sigCh)
}

// runMigrateIfOwned runs the synchronous rimsky-migrate step when this
// invocation owns it (see shouldMigrate). A migrate failure (the child
// exits non-zero, or it cannot be started at all) exits this process
// non-zero. An invalid RIMSKY_ENTRYPOINT_MIGRATE value also exits
// non-zero, before the store is touched. The phase is
// signal-interruptible: a SIGTERM/SIGINT received on sigCh while the
// migrate child runs forwards a graceful shutdown to the child (bounded
// by shutdownDeadline, then SIGKILL) and exits 0 — deliberately, because
// an operator-initiated stop (a `docker stop` during a long migrate) is
// a success, not a failure, and must stop the container promptly rather
// than hang until SIGKILL.
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

// runUnified starts all three roles in this process via the launch
// role runners (delegated to launch.StartUnifiedStack — the shared
// helper the compose-run verb also uses), waits for SIGTERM/SIGINT or
// a fatal role failure, then stops every role within the shutdown
// deadline. A role that fails to start tears down the roles already
// running and exits non-zero; a role whose serve loop dies after start
// (surfaced on the unified stack's fail channel) does the same — any
// dead role must restart the container rather than leave a degraded
// process running. sigCh is the entrypoint-wide signal channel
// registered at the top of main (before migrate), so a SIGTERM during
// slow role startup is already queued for the graceful path.
func runUnified(sigCh <-chan os.Signal) {
	level := parseLogLevel(os.Getenv("RIMSKY_LOG_LEVEL"))
	base := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx := context.Background()

	// One driver, shared across all three Run* runners. Opening per-
	// role would give each role its own connection pool against the
	// same backing file, which under sqlite re-introduces writer-slot
	// contention even though all three roles run inside one process.
	// See @blessed-invariant: one-driver-per-process at
	// lib/control/launch/open_driver.go.
	driver, cfg, err := launch.OpenDriverFromEnv(ctx, base)
	if err != nil {
		slog.Error("open persistence driver", "err", err)
		os.Exit(1)
	}
	// runUnified exits via os.Exit on every path, so the defer wouldn't
	// fire — call Close inline at each exit point instead. The process
	// death also releases the file lock, but an explicit Close ensures
	// pending writes flush cleanly first.
	closeDriver := func() { _ = driver.Close() }

	stack, err := launch.StartUnifiedStack(ctx, base, driver, cfg)
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

// runSingleRole spawns the named role binary as a child process,
// forwards SIGTERM/SIGINT, and exits when the child exits (mirroring
// its exit code) or after a signal-driven graceful shutdown. sigCh is
// the entrypoint-wide signal channel registered at the top of main
// (before migrate), so a SIGTERM during slow child startup is already
// queued for the graceful path.
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
		// A clean child exit (code 0) is still a reason for PID-1 to exit
		// — the container's one role is gone — but it is not an error.
		if ce.err == nil {
			slog.Info("child exited", "binary", ce.name)
		} else {
			slog.Error("child exited unexpectedly", "binary", ce.name, "err", ce.err)
		}
		os.Exit(exitCode(ce.err))
	}
}

// spawnRole starts the named role binary, wiring a wait goroutine onto
// the returned exit channel. main keeps the signal/exit select loop;
// tests drive spawnRole directly to observe which role ran without the
// full PID-1 lifecycle.
func spawnRole(name string) (*exec.Cmd, chan childExit, error) {
	c := exec.Command(binaryDir + "/" + name)
	// RIMSKY_PROCESS_ROLE is deliberately NOT set for a spawned role —
	// the single-process mode marker belongs only to the no-command
	// in-process path (see runUnified), and the memory-blob gate
	// (persistence.ValidateBlobConfig) must reject a per-role process.
	// The inherited environment is filtered too: an operator
	// copy-pasting all-in-one env (RIMSKY_PROCESS_ROLE=unified) onto a
	// split deployment must not silently pass the gate per-process —
	// that is exactly the data-loss mode the gate prevents.
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

// startOnce starts a one-shot binary, mirroring stdout/stderr to the
// entrypoint's, and wires a wait goroutine onto the returned exit
// channel so the caller can select between completion and a signal.
// Used for the synchronous (but signal-interruptible) migrate step. The
// child inherits this process's environment — on the no-command path
// that includes RIMSKY_PROCESS_ROLE=unified, which migrate's config
// load needs when the memory blob backend is configured (config
// validation gates "memory" on the marker; migrate never touches blob
// bytes, and this one-shot init step is part of the single-process boot
// it marks).
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

// shutdownChild sends SIGTERM to the spawned child, then waits up to
// shutdownDeadline for its wait goroutine to report. After the deadline
// the process is SIGKILL'd. `Process.Signal` on an already-reaped
// process returns "os: process already finished" — we ignore it, which
// avoids racing the concurrent Wait completion.
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

// envWithoutProcessRole returns the current environment minus any
// RIMSKY_PROCESS_ROLE entry. Spawned single-role children must never
// inherit the unified marker (see spawnRole).
func envWithoutProcessRole() []string {
	env := os.Environ()
	// @constraint: allocate a fresh backing slice rather than reusing
	// env's via env[:0]. os.Environ() currently returns a freshly-
	// allocated slice (so in-place reuse would be safe today), but
	// future Go runtime changes that share the backing array between
	// callers would have this loop clobber the shared state. The
	// explicit allocation is a one-line cost that pins the invariant.
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "RIMSKY_PROCESS_ROLE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// nameOf strips the "rimsky-" prefix to derive the structured-log
// `binary` field value. "rimsky-scheduler" → "scheduler".
func nameOf(binary string) string {
	return strings.TrimPrefix(binary, "rimsky-")
}

// exitCode maps a child's wait error to a process exit code. exec.ExitError
// preserves the child's exit status; other errors map to 1.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

// parseLogLevel maps RIMSKY_LOG_LEVEL to a slog.Level (default info).
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

// childExit is the signal sent on the wait goroutine when a child exits.
type childExit struct {
	name string
	err  error
}
