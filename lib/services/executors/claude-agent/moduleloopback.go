// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
)

type ModuleMcpTool struct {
	Definition ToolDefinition
	Handler    func(args map[string]any) (text string, isError bool, err error)
}

type ModuleMcpServer struct {
	Name  string
	Tools []ModuleMcpTool
}

type ModuleMcpFactory func() *ModuleMcpServer

type moduleToolProvider struct {
	module *ModuleMcpServer
}

func (p *moduleToolProvider) serverName() string { return p.module.Name }

func (p *moduleToolProvider) listTools() []ToolDefinition {
	defs := make([]ToolDefinition, 0, len(p.module.Tools))
	for _, t := range p.module.Tools {
		defs = append(defs, t.Definition)
	}
	return defs
}

func (p *moduleToolProvider) callTool(name string, arguments json.RawMessage) (map[string]any, *jsonRPCError) {
	for _, t := range p.module.Tools {
		if t.Definition.Name != name {
			continue
		}
		var args map[string]any
		if len(arguments) > 0 {
			if err := json.Unmarshal(arguments, &args); err != nil {
				return nil, &jsonRPCError{Code: -32602, Message: "Invalid params: " + err.Error()}
			}
		}
		text, isError, err := t.Handler(args)
		if err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
		}
		return toolResultText(text, isError), nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "Unknown tool: " + name}
}

func standUpModuleLoopback(serverName string, specifier string, modules map[string]ModuleMcpFactory, logger *slog.Logger) (url string, bearerToken string, teardown func() error, err error) {
	factory, ok := modules[specifier]
	if !ok {
		return "", "", nil, &CliConfigError{Message: "mcp server \"" + serverName + "\" module \"" + specifier +
			"\" is not a declared MCP module (declare it in Opts.McpModules)"}
	}
	module := factory()
	token := uuid.NewString()
	if logger == nil {
		logger = slog.Default()
	}
	srv, err := startMcpHTTPServer(
		&moduleToolProvider{module: module},
		mcpHTTPServerOpts{logger: logger.With("component", "mcp-module", "module_server", serverName), bearerToken: token},
	)
	if err != nil {
		return "", "", nil, err
	}
	return srv.URL, token, srv.Close, nil
}
