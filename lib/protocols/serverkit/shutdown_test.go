// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestSecondSignalEscalatesToHardExit(t *testing.T) {
	sigCh := make(chan os.Signal, 1)
	drained := make(chan struct{})
	defer close(drained)

	var logged bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logged, nil))

	fired := make(chan struct{})
	InstallSecondSignalHardExit(sigCh, drained, log, func() { close(fired) })

	sigCh <- syscall.SIGINT
	<-fired

	if !strings.Contains(logged.String(), `"level":"WARN"`) {
		t.Fatalf("escalation did not log at warn: %q", logged.String())
	}
}

func TestEscalatorRetiresWhenTheDrainCompletesAndIgnoresALaterSignal(t *testing.T) {
	sigCh := make(chan os.Signal, 1)
	drained := make(chan struct{})

	fired := make(chan struct{}, 1)
	retired := InstallSecondSignalHardExit(sigCh, drained, nil, func() { fired <- struct{}{} })

	close(drained)
	<-retired

	sigCh <- syscall.SIGTERM
	select {
	case <-fired:
		t.Fatal("retired escalator hard-exited on a later signal")
	default:
	}
}

func TestFirstSignalCancelsTheContextAndTheSecondEscalates(t *testing.T) {
	sigCh := make(chan os.Signal, 2)
	fired := make(chan struct{})

	ctx, release := WatchShutdownSignals(context.Background(), sigCh, nil, func() { close(fired) })
	defer release()

	sigCh <- syscall.SIGTERM
	<-ctx.Done()

	sigCh <- syscall.SIGTERM
	<-fired
}

func TestReleasingTheWatcherBeforeAnySignalCancelsTheContext(t *testing.T) {
	sigCh := make(chan os.Signal, 1)
	fired := make(chan struct{}, 1)

	ctx, release := WatchShutdownSignals(context.Background(), sigCh, nil, func() { fired <- struct{}{} })
	release()
	<-ctx.Done()

	select {
	case <-fired:
		t.Fatal("released watcher hard-exited without a signal")
	default:
	}
}
