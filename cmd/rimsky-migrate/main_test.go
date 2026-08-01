// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestRun_HonorsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	cfgPath := filepath.Join(dir, "rimsky.yml")
	cfgYAML := "persistence:\n  driver: sqlite\n  sqlite:\n    path: " + dbPath + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RIMSKY_CONFIG", cfgPath)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	err := run(ctx, logger)
	if err == nil {
		t.Fatal("run() with an already-canceled context should fail, not silently migrate")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run() error = %v, want it to wrap context.Canceled", err)
	}
}

func TestRun_MissingConfigSurfacesLoadError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "does-not-exist.yml")
	t.Setenv("RIMSKY_CONFIG", cfgPath)

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	err := run(context.Background(), logger)
	if err == nil {
		t.Fatal("run() with a missing config path should fail, not silently migrate")
	}
}
