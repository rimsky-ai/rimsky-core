// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

const unifiedStackDrainTimeout = 5 * time.Second

type RoleFailure struct {
	Role string
	Err  error
}

type UnifiedStack struct {
	stops  []StopFunc
	failCh chan RoleFailure
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

// @decision: single-process-mode
func StartUnifiedStack(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig, bundledRegs *config.BundledRegistrations) (*UnifiedStack, error) {
	blobBackend, err := config.OpenBlobBackend(cfg.Blob, driver, cfg.Topology)
	if err != nil {
		return nil, fmt.Errorf("open blob backend: %w", err)
	}

	type roleRunner struct {
		name string
		run  func(context.Context, *slog.Logger) (StopFunc, <-chan error, error)
	}
	runners := []roleRunner{
		{"scheduler", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runSchedulerFn(c, l, driver, cfg, blobBackend)
		}},
		{"supervisor", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runSupervisorFn(c, l, driver, cfg, bundledRegs, blobBackend)
		}},
		{"control-api", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runControlAPIFn(c, l, driver, cfg, bundledRegs, blobBackend)
		}},
	}

	stack := &UnifiedStack{
		failCh: make(chan RoleFailure, len(runners)),
	}

	for _, r := range runners {
		stop, runnerFail, err := r.run(ctx, logger.With("role", r.name))
		if err != nil {
			stack.Drain(context.Background(), unifiedStackDrainTimeout)
			return nil, fmt.Errorf("start %s: %w", r.name, err)
		}
		stack.stops = append(stack.stops, stop)
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

	go func() {
		<-ctx.Done()
		stack.Drain(context.Background(), unifiedStackDrainTimeout)
	}()

	return stack, nil
}
