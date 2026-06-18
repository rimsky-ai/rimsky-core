// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

type RoleFailure struct {
	Role string
	Err  error
}

type UnifiedStack struct {
	stops    []StopFunc
	names    []string
	failCh   chan RoleFailure
	failBufN int
}

func (s *UnifiedStack) FailCh() <-chan RoleFailure { return s.failCh }

func (s *UnifiedStack) Drain(ctx context.Context, deadline time.Duration) {
	drainCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for i := len(s.stops) - 1; i >= 0; i-- {
		_ = s.stops[i](drainCtx)
	}
}

var (
	runSchedulerFn  = RunScheduler
	runSupervisorFn = RunSupervisor
	runControlAPIFn = RunControlAPI
)

func StartUnifiedStack(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig) (*UnifiedStack, error) {
	type roleRunner struct {
		name string
		run  func(context.Context, *slog.Logger) (StopFunc, <-chan error, error)
	}
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
			err, ok := <-ch
			if !ok || err == nil {
				return
			}
			select {
			case stack.failCh <- RoleFailure{Role: name, Err: err}:
			default:
			}
		}(r.name, runnerFail)
	}
	return stack, nil
}
