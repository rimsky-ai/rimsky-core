// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
