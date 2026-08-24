// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// @decision: logging-slog-only
const LogLevelEnv = "RIMSKY_LOG_LEVEL"

const LogLevelsAccepted = "debug, info, warn, error"

func ParseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info", "":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

// @decision: logging-slog-only
func NewJSONLoggerForLevel(raw string) *slog.Logger {
	return newJSONLoggerTo(os.Stderr, raw)
}

func newJSONLoggerTo(w io.Writer, raw string) *slog.Logger {
	level, known := ParseLogLevel(raw)
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
	if !known {
		logger.Warn("SERVERKIT.LOGLEVEL.UNRECOGNIZED", "detail", "using the default level",
			"variable", LogLevelEnv,
			"value", raw,
			"accepted", LogLevelsAccepted,
			"using", level.String())
	}
	return logger
}

// @decision: logging-slog-only
// @decision: operator-env-namespaced-per-service
func NewJSONLogger() *slog.Logger {
	return NewJSONLoggerForLevel(os.Getenv(LogLevelEnv))
}
