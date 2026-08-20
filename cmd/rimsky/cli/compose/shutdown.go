// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// @decision: exit-codes
type ShutdownReason int

const ReasonAllSuccess ShutdownReason = 0

const ReasonAnyFailure ShutdownReason = 1

const ReasonTimeout ShutdownReason = 2

const ReasonSignal ShutdownReason = 3

// @decision: graceful-shutdown
const childGraceWindow = serverkit.CLIChildGrace

// @decision: graceful-shutdown
const roleStackDrainWindow = serverkit.CLIChildGrace

type SpawnedRegistry struct {
	mu   sync.Mutex
	list []*hostagent.SpawnedService
}

func (r *SpawnedRegistry) Set(services []*hostagent.SpawnedService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = services
}

func (r *SpawnedRegistry) All() []*hostagent.SpawnedService {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.list
}

type ShutdownCoordinator struct {
	Stack    *RoleStack
	Services []*hostagent.SpawnedService
	Logger   *slog.Logger

	drainOnce sync.Once
	finalCode int
}

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

	c.reapSpawnedChildren(logger)

	if c.Stack != nil {
		c.Stack.Drain(ctx, roleStackDrainWindow)
	}

	switch reason {
	case ReasonAllSuccess:
		return cli.ExitAllSuccess
	case ReasonAnyFailure:
		return cli.ExitAnyFailure
	case ReasonTimeout:
		return cli.ExitTimeout
	case ReasonSignal:
		return cli.ExitInterrupt
	default:
		logger.Warn("compose run: drain with unknown reason; defaulting to failure exit", "reason_int", int(reason))
		return 1
	}
}

func (c *ShutdownCoordinator) reapSpawnedChildren(logger *slog.Logger) {
	if len(c.Services) == 0 {
		return
	}
	for _, s := range c.Services {
		if s == nil || s.Cmd == nil || s.Cmd.Process == nil {
			continue
		}
		if err := s.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
			if !errors.Is(err, os.ErrProcessDone) {
				logger.Warn("compose run: SIGTERM child failed",
					"pid", s.Cmd.Process.Pid,
					"err", err.Error(),
				)
			}
		}
	}

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

	type exitEvent struct{ pid int }
	exitWake := make(chan exitEvent, len(remaining))
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
			for _, s := range remaining {
				if s.Cmd == nil || s.Cmd.Process == nil {
					continue
				}
				logger.Warn("compose run: SIGKILL straggler child",
					"pid", s.Cmd.Process.Pid,
				)
				_ = s.Cmd.Process.Kill()
			}
			for _, s := range remaining {
				if s.Exited != nil {
					<-s.Exited
				}
			}
			return
		}
	}
}

// @decision: graceful-shutdown
type SignalEscalation struct {
	sigCh   chan os.Signal
	spawned *SpawnedRegistry
	logger  *slog.Logger
	done    chan struct{}
	armOnce sync.Once
	retire  sync.Once
}

// @decision: graceful-shutdown
func NewSignalEscalation(spawned *SpawnedRegistry, logger *slog.Logger) (*SignalEscalation, func()) {
	sigCh, stopNotify := serverkit.NotifyShutdownSignals()
	e := &SignalEscalation{
		sigCh:   sigCh,
		spawned: spawned,
		logger:  logger,
		done:    make(chan struct{}),
	}
	return e, func() {
		e.Retire()
		stopNotify()
	}
}

func (e *SignalEscalation) Signals() chan os.Signal { return e.sigCh }

func (e *SignalEscalation) Arm() {
	e.armOnce.Do(func() {
		InstallSecondSignalEscalator(e.sigCh, e.done, e.spawned.All, e.logger)
	})
}

func (e *SignalEscalation) Retire() {
	e.retire.Do(func() { close(e.done) })
}

// @decision: graceful-shutdown
func (e *SignalEscalation) ArmOnFirstSignal(logger *slog.Logger, verb string) <-chan struct{} {
	observed := make(chan struct{})
	go func() {
		defer close(observed)
		select {
		case <-e.done:
		case sig := <-e.sigCh:
			if logger != nil {
				logger.Info(verb+": signal while draining; a second signal exits immediately", "signal", sig.String())
			}
			e.Arm()
		}
	}()
	return observed
}

// @decision: graceful-shutdown
func InstallSecondSignalEscalator(sigCh <-chan os.Signal, done <-chan struct{}, services func() []*hostagent.SpawnedService, logger *slog.Logger) {
	var log *slog.Logger
	if logger != nil {
		log = logger.With("path", "compose run")
	}
	serverkit.InstallSecondSignalHardExit(sigCh, done, log, func() {
		for _, s := range services() {
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
		os.Exit(serverkit.HardExitCode)
	})
}
