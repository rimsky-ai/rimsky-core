// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// shutdown.go — graceful drain coordinator for `rimsky compose run`.
// Owns the bounded SIGTERM-then-SIGKILL teardown of spawned
// `--service` children, the reverse-order role-runner drain, and the
// exit-code classification per @decision: exit-codes /
// graceful-shutdown.
//
// The coordinator runs exactly once per verb invocation. The verb's
// signal-handling path also installs a second-SIGINT escalator that
// hard-exits the process if the operator interrupts during drain —
// the safety valve for a wedged drain.
//
// @blessed-invariant: spawn-child-reaped-on-exit — every spawned
// `--service` child is signalled, reaped (SIGTERM-then-SIGKILL),
// and observed exited before the verb returns. No child outlives
// the verb's process.
package compose

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// ShutdownReason classifies why the verb is shutting down. Each
// reason maps to a specific operator-facing exit code per
// @decision: exit-codes.
type ShutdownReason int

// ReasonAllSuccess indicates every declared instance reached terminal with outcome success; maps to exit 0.
const ReasonAllSuccess ShutdownReason = 0

// ReasonAnyFailure indicates at least one instance terminated with a failure or parked-timeout outcome, or a role runner failed during the run; maps to exit 1.
const ReasonAnyFailure ShutdownReason = 1

// ReasonTimeout indicates the --timeout deadline expired before every instance reached terminal; maps to exit 2.
const ReasonTimeout ShutdownReason = 2

// ReasonSignal indicates a SIGINT or SIGTERM interrupted the run before natural completion; maps to exit 130 (the conventional SIGINT exit).
const ReasonSignal ShutdownReason = 3

// childGraceWindow bounds the time we wait for a spawned child to
// exit after SIGTERM before escalating to SIGKILL. Kept short so the
// verb's overall shutdown stays bounded even with a misbehaving
// child; the BI test pins the bound at 5s + slack.
//
// @deliberate: hardcoded grace window. The spec leaves the grace
// duration to the implementer; a 5s window matches the role-stack
// drain deadline so the two phases compose into a predictable
// overall ceiling (≤10s + signal-delivery slack on a healthy
// shutdown, ≤5s when no children were spawned).
const childGraceWindow = 5 * time.Second

// roleStackDrainWindow bounds the per-role-runner stop. The
// scheduler/supervisor each hold long-lived loops; 5s is enough for
// the in-flight transactions in a one-shot test to settle but short
// enough to bound a wedged drain.
const roleStackDrainWindow = 5 * time.Second

// ShutdownCoordinator orchestrates the verb's drain. The verb
// constructs one of these once the role stack and any spawned
// `--service` children are up; it calls Drain exactly once on the
// terminal-wait classifier's reason.
//
// @agent-contract guarantees: Drain returns only after every
// registered child has been observed exited (the @blessed-invariant:
// spawn-child-reaped-on-exit obligation) and the role stack's
// reverse-order Drain has run. The returned exit code matches the
// reason per @decision: exit-codes. Does NOT install signal
// handlers — the verb's main loop owns the signal channel and calls
// Drain with the classified reason.
type ShutdownCoordinator struct {
	Stack    *RoleStack
	Services []*hostagent.SpawnedService
	Logger   *slog.Logger

	// drainOnce guards against a double-Drain call. The verb's main
	// loop is single-threaded but the second-SIGINT escalator runs
	// in a goroutine that may race a natural-completion drain; the
	// once-guard makes Drain safe to call from either path.
	drainOnce sync.Once
	// finalCode caches the exit code from the first Drain so a
	// second call returns the same value.
	finalCode int
}

// Drain stops accepting new work, SIGTERMs every spawned child,
// waits up to childGraceWindow for them to exit, SIGKILLs any
// stragglers, and reverse-order-drains the role stack. Returns the
// exit code mapped from reason per @decision: exit-codes.
//
// Idempotent — the first call performs the drain; subsequent calls
// return the cached exit code without re-signalling.
func (c *ShutdownCoordinator) Drain(ctx context.Context, reason ShutdownReason) int {
	c.drainOnce.Do(func() {
		c.finalCode = c.doDrain(ctx, reason)
	})
	return c.finalCode
}

func (c *ShutdownCoordinator) doDrain(ctx context.Context, reason ShutdownReason) int {
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("compose run: draining",
		"reason", reasonString(reason),
		"services", len(c.Services),
		"stack_present", c.Stack != nil,
	)

	// @constraint: phase 1 SIGTERMs every spawned child then waits up to
	// childGraceWindow for them to exit; SIGKILL escalation runs against
	// any straggler. The grace window is shared across all children (not
	// per-child) so a non-cooperating child cannot extend the drain beyond
	// the window — load-bearing for the @blessed-invariant:
	// spawn-child-reaped-on-exit duration bound.
	c.reapSpawnedChildren(logger)

	// @constraint: phase 2 is a reverse-order role-stack drain; the stack
	// itself owns the per-runner stop functions and the deadline-bounded
	// drainCtx.
	if c.Stack != nil {
		c.Stack.Drain(ctx, roleStackDrainWindow)
	}

	switch reason {
	case ReasonAllSuccess:
		return 0
	case ReasonAnyFailure:
		return 1
	case ReasonTimeout:
		return 2
	case ReasonSignal:
		return 130
	default:
		// @constraint: unknown reason is a programming error in the
		// caller's classifier; treat as failure so a scripted gate
		// that polls on exit code does not interpret it as success.
		logger.Warn("compose run: drain with unknown reason; defaulting to failure exit", "reason_int", int(reason))
		return 1
	}
}

// reapSpawnedChildren signals every Services entry SIGTERM, waits
// for them to exit within childGraceWindow, then SIGKILLs any
// straggler. After the window every child is either exited
// naturally or signalled-and-waited.
//
// @constraint: the wait is any-child-can-fire, not head-of-queue: a
// per-child watcher goroutine forwards each child's Exited signal
// onto a shared channel so the loop wakes on whichever child exits
// next. The previous head-of-queue rotation made cooperative siblings
// wait for an uncooperative head, so when the deadline finally fired
// every still-in-remaining child got SIGKILLed even if N-1 of them
// were cooperative and just blocked on the stubborn one. Switching to
// the shared-wakeup channel narrows the SIGKILL set to the actual
// stragglers.
func (c *ShutdownCoordinator) reapSpawnedChildren(logger *slog.Logger) {
	if len(c.Services) == 0 {
		return
	}
	for _, s := range c.Services {
		if s == nil || s.Cmd == nil || s.Cmd.Process == nil {
			continue
		}
		if err := s.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
			// @deliberate: ErrProcessDone here means the child already
			// exited; the Exited channel will fire and the wait below
			// will observe it cleanly, so we suppress the warning.
			if !errors.Is(err, os.ErrProcessDone) {
				logger.Warn("compose run: SIGTERM child failed",
					"pid", s.Cmd.Process.Pid,
					"err", err.Error(),
				)
			}
		}
	}

	// @constraint: remaining is keyed by PID so each per-child watcher
	// can identify itself when it wakes the loop; the deadline branch
	// below assumes membership semantics (a child removed from this map
	// has already been observed exited and must not be SIGKILLed).
	remaining := map[int]*hostagent.SpawnedService{}
	for _, s := range c.Services {
		if s == nil || s.Cmd == nil || s.Cmd.Process == nil || s.Exited == nil {
			continue
		}
		remaining[s.Cmd.Process.Pid] = s
	}
	if len(remaining) == 0 {
		return
	}

	// @deliberate: exitWake capacity = len(remaining) so a burst of
	// simultaneous child exits never blocks the per-child watchers.
	type exitEvent struct{ pid int }
	exitWake := make(chan exitEvent, len(remaining))
	// @constraint: stopWatchers signals each watcher to exit once the
	// loop has resolved, so a watcher whose child fires Exited after we
	// return cannot leak.
	stopWatchers := make(chan struct{})
	for pid, s := range remaining {
		go func(pid int, exited <-chan struct{}) {
			select {
			case <-exited:
				select {
				case exitWake <- exitEvent{pid: pid}:
				case <-stopWatchers:
				}
			case <-stopWatchers:
			}
		}(pid, s.Exited)
	}
	defer close(stopWatchers)

	deadline := time.NewTimer(childGraceWindow)
	defer deadline.Stop()

	for len(remaining) > 0 {
		select {
		case ev := <-exitWake:
			delete(remaining, ev.pid)
		case <-deadline.C:
			// @constraint: SIGKILL fires only on children still in
			// remaining; cooperative siblings already exited and were
			// removed above, so they are not in the kill set. Narrowing
			// the kill set to actual stragglers is the whole point of
			// the shared-wakeup-channel design above.
			for _, s := range remaining {
				if s.Cmd == nil || s.Cmd.Process == nil {
					continue
				}
				logger.Warn("compose run: SIGKILL straggler child",
					"pid", s.Cmd.Process.Pid,
				)
				_ = s.Cmd.Process.Kill()
			}
			// @blessed-invariant: spawn-child-reaped-on-exit — wait
			// for the post-SIGKILL Exited signals so every child is
			// observed exited before Drain returns. SIGKILL is
			// unstoppable so this is a bounded wait, not a possible
			// infinite hang.
			for _, s := range remaining {
				if s.Exited != nil {
					<-s.Exited
				}
			}
			return
		}
	}
}

// InstallSecondSignalEscalator starts a goroutine that watches for a
// second signal on sigCh during shutdown. The first signal triggers
// the natural drain via the caller's select loop; if a second signal
// arrives while drain is running, the goroutine SIGKILLs every still-
// registered spawned child and then hard-exits the process with 130
// (the conventional SIGINT exit). This is the safety valve for a
// wedged drain — without it an operator who has already pressed
// Ctrl-C once cannot escape a hung shutdown without kill -9 from
// another terminal.
//
// The done channel signals the goroutine to exit cleanly when the
// drain finishes naturally so a routine test invocation does not
// leak a goroutine.
//
// @blessed-invariant: spawn-child-reaped-on-exit (safety-valve
// branch) — even when the cooperative drain stalls, a second
// operator signal escapes the verb. exec.Cmd-spawned children do
// NOT receive a signal when the parent calls os.Exit — they inherit
// the parent's pgroup but get reparented to init and survive. The
// escalator therefore walks the services slice and explicitly
// SIGKILLs each child before os.Exit, so no spawned child outlives
// the hard-exit path either.
func InstallSecondSignalEscalator(sigCh <-chan os.Signal, done <-chan struct{}, services []*hostagent.SpawnedService, logger *slog.Logger) {
	go func() {
		select {
		case <-done:
			return
		case <-sigCh:
			if logger != nil {
				logger.Warn("compose run: second signal received; hard-exit 130")
			}
			for _, s := range services {
				if s == nil || s.Cmd == nil || s.Cmd.Process == nil {
					continue
				}
				if err := s.Cmd.Process.Kill(); err != nil && logger != nil && !errors.Is(err, os.ErrProcessDone) {
					logger.Warn("compose run: SIGKILL on hard-exit failed",
						"pid", s.Cmd.Process.Pid,
						"err", err.Error(),
					)
				}
			}
			os.Exit(130)
		}
	}()
}
