// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"log/slog"
	"os"

	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
)

const moduleWitnessSpecifier = "scenario:per-node-module-witness"

func moduleWitness() map[string]claudeagent.ModuleMcpFactory {
	return map[string]claudeagent.ModuleMcpFactory{
		moduleWitnessSpecifier: func() *claudeagent.ModuleMcpServer {
			return &claudeagent.ModuleMcpServer{
				Name: "module-witness",
				Tools: []claudeagent.ModuleMcpTool{{
					Definition: claudeagent.ToolDefinition{
						Name:        "witness",
						Description: "scenario proof witness for the module MCP transport",
						InputSchema: map[string]any{"type": "object"},
					},
					Handler: func(args map[string]any) (string, bool, error) {
						return "module-transport-reached", false, nil
					},
				}},
			}
		},
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	opts, err := claudeagent.LoadOptsFromEnv()
	if err != nil {
		slog.Error("CLAUDEAGENT.CONFIG.INVALID", "error", err.Error())
		os.Exit(1)
	}
	opts.McpModules = moduleWitness()
	if err := claudeagent.Serve(opts); err != nil {
		slog.Error("CLAUDEAGENT.PROCESS.FAILED", "error", err.Error())
		os.Exit(1)
	}
}
