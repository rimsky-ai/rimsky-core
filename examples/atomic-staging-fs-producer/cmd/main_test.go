// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMustParseDurationEnv_EmptyReturnsZero(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DURATION", "")
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if got := mustParseDurationEnv(logger, "RIMSKY_TEST_DURATION"); got != 0 {
		t.Fatalf("mustParseDurationEnv(empty) = %v, want 0", got)
	}
}

func TestMustParseDurationEnv_ValidValue(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DURATION", "45s")
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if got := mustParseDurationEnv(logger, "RIMSKY_TEST_DURATION"); got != 45*time.Second {
		t.Fatalf("mustParseDurationEnv(45s) = %v, want 45s", got)
	}
}

func TestMustParseDurationEnv_InvalidValueExitsNonZero(t *testing.T) {
	if os.Getenv("RIMSKY_TEST_MUST_PARSE_DURATION_INVALID") == "1" {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		mustParseDurationEnv(logger, "RIMSKY_TEST_DURATION")
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run", "TestMustParseDurationEnv_InvalidValueExitsNonZero")
	cmd.Env = append(os.Environ(),
		"RIMSKY_TEST_MUST_PARSE_DURATION_INVALID=1",
		"RIMSKY_TEST_DURATION=1 hour",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("mustParseDurationEnv with an unparseable value should exit non-zero; output:\n%s", out)
	}
	if !strings.Contains(string(out), "RIMSKY_TEST_DURATION") {
		t.Fatalf("error output should name the offending env var; output:\n%s", out)
	}
}
