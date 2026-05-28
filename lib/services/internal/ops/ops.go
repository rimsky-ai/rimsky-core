// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package ops bundles the small slog-setup helper that every bundled-service
// binary in lib/services calls during startup.
package ops

import (
	"log/slog"
	"os"
)

// Setup configures slog to write JSON to stderr at the given level,
// installs the result as the process default via slog.SetDefault (so
// package-global slog.Info/Warn/Error inherit it), and returns the
// logger. Matches the conventions every bundled-service binary in
// lib/services uses: JSON output, stderr destination, stdlib default
// time format.
func Setup(level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
