// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// unified.go — the single in-process orchestration site for the three
// role runners (scheduler → supervisor → control-api). Two callers
// consume it: the all-in-one entrypoint (no-arg `rimsky-entrypoint`)
// and the `rimsky compose run` verb. Both want the same five things:
// start the three roles in fixed order, track each role's stop
// function, route each role's serve-loop error onto a merged failure
// channel, drain in reverse order on shutdown, and stay easy to
// extend if a fourth role is ever added — extending it here means
// both consumers pick the change up by recompile.
//
// @decision: launch-integration
package launch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// RoleFailure carries one role's serve-loop error onto the merged
// failure channel UnifiedStack exposes. role names match what the
// runners log ("scheduler", "supervisor", "control-api").
type RoleFailure struct {
	Role string
	Err  error
}

// UnifiedStack is the handle StartUnifiedStack returns: the stop
// functions for every role that started, the merged role-failure
// channel, and the role names tracked in start order so Drain can
// run them in reverse. The driver is NOT owned by the stack — the
// caller opened it and must close it after Drain returns.
type UnifiedStack struct {
	stops    []StopFunc
	names    []string
	failCh   chan RoleFailure
	failBufN int
}

// FailCh exposes the merged role-failure channel. At most one
// failure per role lands here; the channel is buffered to roleCount
// so the first failure per role always fits without blocking the
// monitor goroutine.
func (s *UnifiedStack) FailCh() <-chan RoleFailure { return s.failCh }

// Drain stops every role in reverse start order, bounded by deadline.
// Each StopFunc gets the same context; the deadline is shared so a
// stuck role cannot extend the budget for the rest.
//
// @blessed-invariant: unified-stack-reverse-drain — the operator-
// facing control-api stops FIRST so no new request lands while the
// engines under it (supervisor, scheduler) are tearing down. Reversing
// the start order is the mechanical guarantee.
func (s *UnifiedStack) Drain(ctx context.Context, deadline time.Duration) {
	drainCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for i := len(s.stops) - 1; i >= 0; i-- {
		_ = s.stops[i](drainCtx)
	}
}

// runSchedulerFn / runSupervisorFn / runControlAPIFn are seam vars
// for the three role-runner entry points. Production code uses the
// real RunX runners (the defaults set here); a test can substitute a
// fake that records the persistence.Database pointer it received and
// assert all three runners observed the SAME pointer — the
// @blessed-invariant: one-driver-per-process exhibit.
var (
	runSchedulerFn  = RunScheduler
	runSupervisorFn = RunSupervisor
	runControlAPIFn = RunControlAPI
)

// StartUnifiedStack starts scheduler, supervisor, and control-api in
// that order against the shared driver and cfg, returning a stack
// handle the caller drains on shutdown. On any role's start failure,
// the stack drains the runners that did start (reverse order, 5s
// deadline) and returns the error — the caller never sees a partial
// stack.
//
// The driver is shared across all three roles per @blessed-invariant:
// one-driver-per-process (see OpenDriverFromEnv). The caller opens
// the driver and owns its Close.
func StartUnifiedStack(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig) (*UnifiedStack, error) {
	type roleRunner struct {
		name string
		run  func(context.Context, *slog.Logger) (StopFunc, <-chan error, error)
	}
	// @constraint: ordering. Scheduler and supervisor wire up their
	// background loops first; control-api LAST so the operator-facing
	// endpoint is only listening once the engines under it are wired.
	// Drain reverses this so the operator surface drops first.
	runners := []roleRunner{
		{"scheduler", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runSchedulerFn(c, l, driver, cfg)
		}},
		{"supervisor", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runSupervisorFn(c, l, driver, cfg)
		}},
		{"control-api", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runControlAPIFn(c, l, driver, cfg)
		}},
	}

	stack := &UnifiedStack{
		failCh:   make(chan RoleFailure, len(runners)),
		failBufN: len(runners),
	}

	for _, r := range runners {
		stop, runnerFail, err := r.run(ctx, logger.With("role", r.name))
		if err != nil {
			stack.Drain(context.Background(), 5*time.Second)
			return nil, fmt.Errorf("start %s: %w", r.name, err)
		}
		stack.stops = append(stack.stops, stop)
		stack.names = append(stack.names, r.name)
		go func(name string, ch <-chan error) {
			// failureReporter.Close closes runnerFail on Drain, so a
			// receive returning ok=false is the clean-shutdown path and
			// we exit the goroutine without forwarding anything.
			err, ok := <-ch
			if !ok || err == nil {
				return
			}
			// Non-blocking send: the channel is buffered to len(runners)
			// so the first failure per role always fits; if the caller
			// has already drained and stopped consuming we still must
			// not block.
			select {
			case stack.failCh <- RoleFailure{Role: name, Err: err}:
			default:
			}
		}(r.name, runnerFail)
	}
	return stack, nil
}
