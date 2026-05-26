// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// rimsky-host-agent is the long-running dev-machine daemon that dials the
// rimsky-host-agent-proxy outbound, receives Spawn/Dispatch/Reap frames,
// exec()s local binaries as rimsky services, tunnels their gRPC streams and
// local HTTP callbacks back through the bidi stream, and reaps them on
// signal. It is bundled into the `rimsky` CLI as `rimsky agent start`; this
// standalone binary calls the same hostagent.Run main loop.
//
// @concept: host-agent
//
// Environment variables (see runtime/hostagent.LoadConfigFromEnv):
//
//	RIMSKY_URL                  required; proxy agent-facing endpoint (host:port).
//	RIMSKY_API_KEY              required; api-key presented in Register.
//	RIMSKY_AGENT_LISTEN         optional; local HTTP listener addr.
//	RIMSKY_AGENT_LABEL          optional; defaults to "<hostname>-<pid>".
//	RIMSKY_LOG_LEVEL            optional; debug|info|warn|error (default info).
//	RIMSKY_AGENT_HEARTBEAT_SEC  optional; heartbeat cadence seconds (default 10).
//	RIMSKY_AGENT_REAP_GRACE_SEC optional; reap grace seconds (default 30).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/fallguyconsulting/rimsky/runtime/hostagent"
)

func main() {
	cfg := hostagent.LoadConfigFromEnv()

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(cfg.LogLevel)})
	slog.SetDefault(slog.New(handler))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := hostagent.Run(ctx, cfg); err != nil {
		slog.Error("hostagent.Run", "error", err)
		os.Exit(1)
	}
}

// parseLogLevel maps a textual level to slog.Level (mirrors the other
// entrypoints' helper of the same name).
func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
