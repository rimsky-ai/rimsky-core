// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"log/slog"
	"os"

	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	opts, err := claudeagent.LoadOptsFromEnv()
	if err != nil {
		slog.Error("claude-agent config", "error", err.Error())
		os.Exit(1)
	}
	if err := claudeagent.Serve(opts); err != nil {
		slog.Error("claude-agent", "error", err.Error())
		os.Exit(1)
	}
}
