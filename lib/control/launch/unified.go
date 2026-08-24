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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// @decision: lifecycle-drain-per-role
type RoleOptions struct {
	Bundled *config.BundledRegistrations

	SharedLifecycleDrain *runtime.LifecycleReconciler
}

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

type runRoleFunc func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, RoleOptions) (StopFunc, <-chan error, error)

type roleRunners struct {
	scheduler  runRoleFunc
	supervisor runRoleFunc
	controlAPI runRoleFunc
}

func defaultRoleRunners() roleRunners {
	return roleRunners{scheduler: RunScheduler, supervisor: RunSupervisor, controlAPI: RunControlAPI}
}

// @decision: single-process-mode
func StartUnifiedStack(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig, bundledRegs *config.BundledRegistrations) (*UnifiedStack, error) {
	return startUnifiedStack(ctx, logger, driver, cfg, bundledRegs, defaultRoleRunners())
}

func startUnifiedStack(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig, bundledRegs *config.BundledRegistrations, runs roleRunners) (*UnifiedStack, error) {
	type roleRunner struct {
		name string
		run  func(context.Context, *slog.Logger) (StopFunc, <-chan error, error)
	}
	// @decision: lifecycle-drain-per-role
	drain, stopDrain, err := config.StartSharedLifecycleDrain(config.SharedLifecycleDrainConfig{
		Driver:         driver,
		Clock:          shared.SystemClock{},
		Logger:         shared.NewSlogLogger(logger),
		ClaimProducers: cfg.ClaimProducers,
		Executors:      cfg.Executors,
		Publishers:     cfg.Publishers,
		StallAfter:     cfg.ServiceDelivery.StallAfter,
	})
	if err != nil {
		return nil, fmt.Errorf("start lifecycle drain: %w", err)
	}
	opts := RoleOptions{Bundled: bundledRegs, SharedLifecycleDrain: drain}

	runners := []roleRunner{
		{"scheduler", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runs.scheduler(c, l, driver, cfg, opts)
		}},
		{"supervisor", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runs.supervisor(c, l, driver, cfg, opts)
		}},
		{"control-api", func(c context.Context, l *slog.Logger) (StopFunc, <-chan error, error) {
			return runs.controlAPI(c, l, driver, cfg, opts)
		}},
	}

	stack := &UnifiedStack{
		failCh: make(chan RoleFailure, len(runners)),
	}
	stack.stops = append(stack.stops, func(context.Context) error {
		stopDrain()
		return nil
	})

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
