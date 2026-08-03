// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package shared

import (
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLogLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"info":  slog.LevelInfo,
		"":      slog.LevelInfo,
		"bogus": slog.LevelInfo,
	}
	for input, want := range cases {
		require.Equalf(t, want, ParseLogLevel(input), "input %q", input)
	}
}

func TestWaitForSignalOrFailureReturnsOnSignal(t *testing.T) {
	log := NewCapturingLogger()
	sigCh := make(chan os.Signal, 1)
	failCh := make(chan error, 1)
	sigCh <- os.Interrupt

	err := WaitForSignalOrFailure(log, sigCh, failCh)
	require.NoError(t, err)

	records := log.Records()
	require.Len(t, records, 1)
	require.Equal(t, "info", records[0].Level)
}

// @decision: graceful-shutdown
func TestInstallSecondSignalHardExitFiresOnTheNextSignal(t *testing.T) {
	log := NewCapturingLogger()
	sigCh := make(chan os.Signal, 1)
	drained := make(chan struct{})
	defer close(drained)

	fired := make(chan struct{})
	InstallSecondSignalHardExit(sigCh, drained, log, func() { close(fired) })

	sigCh <- os.Interrupt
	<-fired

	records := log.Records()
	require.Len(t, records, 1)
	require.Equal(t, "warn", records[0].Level)
}

func TestWaitForSignalOrFailureReturnsOnFailure(t *testing.T) {
	log := NewCapturingLogger()
	sigCh := make(chan os.Signal, 1)
	failCh := make(chan error, 1)
	wantErr := errors.New("role crashed")
	failCh <- wantErr

	err := WaitForSignalOrFailure(log, sigCh, failCh)
	require.ErrorIs(t, err, wantErr)

	records := log.Records()
	require.Len(t, records, 1)
	require.Equal(t, "error", records[0].Level)
}
