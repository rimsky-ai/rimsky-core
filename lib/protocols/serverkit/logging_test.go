// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLogLevelAcceptsEveryCaseAndReportsAnUnknownToken(t *testing.T) {
	known := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"DEBUG": slog.LevelDebug,
		" Warn": slog.LevelWarn,
		"ERROR": slog.LevelError,
		"info":  slog.LevelInfo,
		"":      slog.LevelInfo,
	}
	for input, want := range known {
		got, ok := ParseLogLevel(input)
		if !ok {
			t.Errorf("ParseLogLevel(%q) reported the token as unknown", input)
		}
		if got != want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", input, got, want)
		}
	}

	for _, input := range []string{"verbose", "trace", "wrn"} {
		got, ok := ParseLogLevel(input)
		if ok {
			t.Errorf("ParseLogLevel(%q) accepted a token rimsky does not define", input)
		}
		if got != slog.LevelInfo {
			t.Errorf("ParseLogLevel(%q) fell back to %v, want info", input, got)
		}
	}
}

// @decision: logging-slog-only
func TestJSONLoggerTakesItsLevelFromTheSharedVariable(t *testing.T) {
	t.Setenv(LogLevelEnv, "debug")
	if !NewJSONLogger().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug level from the shared variable did not reach the handler")
	}

	t.Setenv(LogLevelEnv, "error")
	if NewJSONLogger().Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("error level from the shared variable did not raise the handler's threshold")
	}
}

// @decision: logging-slog-only
func TestJSONLoggerReportsAnUnrecognizedLevelInsteadOfSilentlyUsingInfo(t *testing.T) {
	var captured bytes.Buffer
	logger := newJSONLoggerTo(&captured, "verbose")

	if !logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("an unrecognized level must still leave a usable logger at info")
	}
	if !strings.Contains(captured.String(), "unrecognized log level") {
		t.Fatalf("an unrecognized level emitted no event: %q", captured.String())
	}
	if !strings.Contains(captured.String(), "verbose") {
		t.Fatalf("the event must name the value the operator set: %q", captured.String())
	}
}
