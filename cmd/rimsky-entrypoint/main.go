// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-entrypoint is the unified-image PID-1 process supervisor. With no
// command argument it runs rimsky-migrate synchronously, then spawns all
// three runtime binaries (rimsky-scheduler, rimsky-supervisor,
// rimsky-control-api) — the zero-config all-in-one stack. When given a single
// role argument (e.g. `command: [rimsky-scheduler]`) it spawns ONLY that role,
// so multi-container deploys run one role per container with correct
// cross-container addressing. An unknown role argument is rejected loudly.
// Either way it forwards SIGTERM/SIGINT and exits when any child exits or all
// clean up. Per spec §7.3.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// shutdownDeadline bounds the wait for children to exit after SIGTERM
// before they're SIGKILL'd. Distroless containers are typically stopped
// with `docker stop --time=30`; matching that gives children full
// budget without leaving the container hanging.
const shutdownDeadline = 30 * time.Second

// children is the full spawn list for the all-in-one (no-argument) path.
// Order is informational only — processes are started concurrently after
// migrate completes. It is also the set of valid single-role arguments.
var children = []string{"rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api"}

// binaryDir is overridden in tests to point at a fixture-binary directory.
var binaryDir = "/usr/local/bin"

// selectChildren maps the entrypoint's command arguments (os.Args[1:]) to the
// list of role binaries to spawn:
//   - no args → all three roles (the zero-config all-in-one stack).
//   - one arg naming a known runtime role → only that role.
//   - anything else (unknown role, rimsky-migrate, or >1 arg) → an error.
//
// rimsky-migrate is deliberately NOT a selectable role: it is a one-shot init
// step, not a long-running process, and the entrypoint runs it separately (see
// shouldMigrate). Returning it here would leave the entrypoint supervising a
// process that exits immediately.
func selectChildren(args []string) ([]string, error) {
	if len(args) == 0 {
		return children, nil
	}
	if len(args) > 1 {
		return nil, fmt.Errorf("rimsky-entrypoint accepts at most one role argument; got %d (%v); valid roles: %s",
			len(args), args, strings.Join(children, ", "))
	}
	role := args[0]
	for _, known := range children {
		if role == known {
			return []string{role}, nil
		}
	}
	return nil, fmt.Errorf("unknown role %q; valid roles: %s", role, strings.Join(children, ", "))
}

// shouldMigrate decides whether this entrypoint invocation runs the
// synchronous rimsky-migrate step. The rule, kept deliberately simple and
// explicit:
//
//   - RIMSKY_ENTRYPOINT_MIGRATE=1 forces migrate; =0 skips it. This lets an
//     operator run a dedicated one-shot migrate container, or suppress migrate
//     in a role container that shares a store with one that already migrated.
//   - With no override: the no-arg all-in-one path always migrates (one
//     process owns the whole store). For single-role containers exactly one
//     role — rimsky-control-api — owns schema init, so a three-container
//     deploy migrates once rather than racing three concurrent migrations or
//     never migrating at all.
func shouldMigrate(selected []string) bool {
	switch os.Getenv("RIMSKY_ENTRYPOINT_MIGRATE") {
	case "1":
		return true
	case "0":
		return false
	}
	// No override: all-in-one (all three) migrates; single-role migrates only
	// for the designated control-api role.
	if len(selected) == len(children) {
		return true
	}
	return len(selected) == 1 && selected[0] == "rimsky-control-api"
}

// spawnChildren starts each selected role binary, wiring a wait goroutine per
// child onto the returned exit channel. main keeps the signal/exit select
// loop; tests drive spawnChildren directly to observe which roles ran without
// the full PID-1 lifecycle. The args→names mapping goes through selectChildren,
// so an unknown role surfaces as an error here (and the caller exits non-zero).
func spawnChildren(args []string) ([]*exec.Cmd, chan childExit, error) {
	names, err := selectChildren(args)
	if err != nil {
		return nil, nil, err
	}
	cmds := make([]*exec.Cmd, 0, len(names))
	exitCh := make(chan childExit, len(names))
	for _, name := range names {
		c := exec.Command(binaryDir + "/" + name)
		// RIMSKY_PROCESS_ROLE=unified gates the in-process "memory"
		// BlobBackend (D5 / blob_config.go::ValidateBlobConfig). Set
		// here so colocated children share the same in-process map;
		// per-process binaries leave it unset and reject memory backend.
		c.Env = append(os.Environ(),
			"RIMSKY_LOG_BINARY="+nameOf(name),
			"RIMSKY_PROCESS_ROLE=unified",
		)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			killAll(cmds)
			return nil, nil, fmt.Errorf("spawn %s: %w", name, err)
		}
		cmds = append(cmds, c)
		go func(c *exec.Cmd, name string) {
			err := c.Wait()
			exitCh <- childExit{name: name, err: err}
		}(c, name)
	}
	return cmds, exitCh, nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("binary", "entrypoint"))

	// Resolve the role argument first so an unknown role fails before we touch
	// the store with a migrate run.
	args := os.Args[1:]
	selected, err := selectChildren(args)
	if err != nil {
		slog.Error("invalid role argument", "err", err)
		os.Exit(2)
	}
	slog.Info("selected roles", "roles", selected)

	// Step 1: migrate synchronously (only when this invocation owns migrate).
	if shouldMigrate(selected) {
		slog.Info("running migrations")
		if err := runOnce("rimsky-migrate"); err != nil {
			slog.Error("migrate failed", "err", err)
			os.Exit(1)
		}
		slog.Info("migrations complete")
	} else {
		slog.Info("skipping migrations for this role", "roles", selected)
	}

	// Step 2: spawn the selected children.
	cmds, exitCh, err := spawnChildren(args)
	if err != nil {
		slog.Error("spawn failed", "err", err)
		os.Exit(1)
	}

	// Step 3: forward signals; wait for first exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		slog.Info("received signal; shutting down", "signal", sig.String())
		shutdown(cmds, exitCh, "")
		os.Exit(0)
	case ce := <-exitCh:
		slog.Error("child exited unexpectedly", "binary", ce.name, "err", ce.err)
		// Pass the already-exited child's name so shutdown does not wait
		// for a second receive from its (already-drained) wait goroutine.
		shutdown(cmds, exitCh, ce.name)
		os.Exit(exitCode(ce.err))
	}
}

// runOnce runs a binary to completion, mirroring stdout/stderr to the
// entrypoint's. Used for the synchronous migrate step.
func runOnce(binary string) error {
	c := exec.Command(binaryDir + "/" + binary)
	c.Env = append(os.Environ(),
		"RIMSKY_LOG_BINARY="+nameOf(binary),
		"RIMSKY_PROCESS_ROLE=unified",
	)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// shutdown sends SIGTERM to every spawned child, then waits up to
// shutdownDeadline for the remaining wait-goroutine messages to drain.
// After the deadline, still-running processes are SIGKILL'd.
//
// Counting model: every wait goroutine sends exactly once on exitCh. The
// expected number of receives is `len(cmds)` minus any messages that
// have already been drained outside this function. `drainedAlready`
// names a child whose message was consumed by the unexpected-child-
// exit `select` in main; passing its name (or "" when no message has
// been drained) keeps the receive count accurate so shutdown neither
// blocks forever nor returns before all goroutines have reported.
//
// SIGTERM fan-out targets every spawned child with Process != nil
// regardless of whether the process has exited. `Process.Signal` on an
// already-reaped process returns "os: process already finished" — we
// ignore it. This avoids the race between checking ProcessState and a
// concurrent Wait completion that could otherwise miscount.
func shutdown(cmds []*exec.Cmd, exitCh chan childExit, drainedAlready string) {
	for _, c := range cmds {
		if c.Process == nil {
			continue
		}
		_ = c.Process.Signal(syscall.SIGTERM)
	}
	expected := 0
	for _, c := range cmds {
		if c.Process == nil {
			continue
		}
		expected++
	}
	if drainedAlready != "" {
		// One message was already consumed by the caller (the unexpected-
		// exit select in main).
		expected--
	}
	deadline := time.After(shutdownDeadline)
	for expected > 0 {
		select {
		case <-exitCh:
			expected--
		case <-deadline:
			for _, c := range cmds {
				if c.Process == nil {
					continue
				}
				_ = c.Process.Kill()
			}
			return
		}
	}
}

// killAll force-kills every started child (used on spawn failure).
func killAll(cmds []*exec.Cmd) {
	for _, c := range cmds {
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	}
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

// childExit is the signal sent on the wait goroutine when a child exits.
type childExit struct {
	name string
	err  error
}
