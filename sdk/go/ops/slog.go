// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package ops bundles the small operational helpers every rimsky-
// implementing service needs: stdlib slog setup, an /health HTTP
// handler, and a DSN env-var reader.
//
// These exist as small free functions rather than a framework: each
// helper is a 10-line shape that was duplicated across every
// sensor / executor / store-service binary. The SDK exports them so
// service authors get the same operator-facing conventions
// (JSON log output, GET /health → 200, consistent DSN env-var
// error shape) without re-implementing each surface.
package ops

import (
	"log/slog"
	"os"
)

// Setup configures slog to write JSON to stderr and returns the
// resulting *slog.Logger. The logger is also installed as the
// process default via slog.SetDefault so packages using the package-
// global slog.Info/Warn/Error functions inherit it.
//
// This matches the conventions every bundled rimsky service used
// before the SDK extraction: JSON output, stderr destination, no
// time-format override (stdlib default RFC3339Nano).
func Setup(level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
