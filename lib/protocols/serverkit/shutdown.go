// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const HardExitCode = 130

// @decision: graceful-shutdown
const CLIChildGrace = 5 * time.Second

// @decision: graceful-shutdown
const BundledServiceGrace = 10 * time.Second

// @decision: graceful-shutdown
const DeployedCoreGrace = 30 * time.Second

func NotifyShutdownSignals() (chan os.Signal, func()) {
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	return sigCh, func() { signal.Stop(sigCh) }
}

// @decision: graceful-shutdown
func InstallSecondSignalHardExit(sigCh <-chan os.Signal, done <-chan struct{}, log *slog.Logger, hardExit func()) <-chan struct{} {
	retired := make(chan struct{})
	go func() {
		defer close(retired)
		select {
		case <-done:
			return
		case s := <-sigCh:
			if log != nil {
				log.Warn("SERVERKIT.SECONDSIGNAL.ESCALATED", "detail", "escalating to a hard exit", "signal", s.String())
			}
			hardExit()
		}
	}()
	return retired
}

// @decision: graceful-shutdown
func WatchShutdownSignals(parent context.Context, sigCh <-chan os.Signal, log *slog.Logger, hardExit func()) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	released := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	go func() {
		select {
		case <-released:
			return
		case s := <-sigCh:
			if log != nil {
				log.Info("SERVERKIT.SIGNAL.RECEIVED", "detail", "shutting down", "signal", s.String())
			}
			cancel()
			InstallSecondSignalHardExit(sigCh, released, log, hardExit)
		}
	}()
	return ctx, func() {
		release()
		cancel()
	}
}

// @decision: graceful-shutdown
func ShutdownContext(parent context.Context, log *slog.Logger) (context.Context, context.CancelFunc) {
	sigCh, stopNotify := NotifyShutdownSignals()
	ctx, release := WatchShutdownSignals(parent, sigCh, log, func() { os.Exit(HardExitCode) })
	return ctx, func() {
		release()
		stopNotify()
	}
}
