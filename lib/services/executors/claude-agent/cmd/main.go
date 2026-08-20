// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
)

func main() {
	slog.SetDefault(serverkit.NewJSONLogger())
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
