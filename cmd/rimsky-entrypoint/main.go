// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-entrypoint is the unified-image PID-1 process supervisor. Runs
// rimsky-migrate synchronously, then spawns the three runtime binaries
// (rimsky-scheduler, rimsky-supervisor, rimsky-control-api) and forwards
// SIGTERM/SIGINT. Exits when any child exits or all clean up. Per spec
// §7.3.
package main

import (
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

// children is the spawn list. Order is informational only — processes
// are started concurrently after migrate completes.
var children = []string{"rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api"}

// binaryDir is overridden in tests to point at a fixture-binary directory.
var binaryDir = "/usr/local/bin"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("binary", "entrypoint"))

	// Step 1: migrate synchronously.
	slog.Info("running migrations")
	if err := runOnce("rimsky-migrate"); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	slog.Info("migrations complete")

	// Step 2: spawn children.
	cmds := make([]*exec.Cmd, 0, len(children))
	exitCh := make(chan childExit, len(children))
	for _, name := range children {
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
			slog.Error("spawn failed", "binary", name, "err", err)
			killAll(cmds)
			os.Exit(1)
		}
		cmds = append(cmds, c)
		go func(c *exec.Cmd, name string) {
			err := c.Wait()
			exitCh <- childExit{name: name, err: err}
		}(c, name)
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
