// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"log/slog"
	"os"

	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

const moduleWitnessSpecifier = "scenario:per-node-module-witness"

func registerModuleWitness() {
	claudeagent.RegisterMcpModule(moduleWitnessSpecifier, func() *claudeagent.ModuleMcpServer {
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
	})
}

func main() {
	registerModuleWitness()
	ops.Setup(slog.LevelInfo)
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
