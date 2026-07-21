// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"encoding/json"
	"log/slog"
	"sync"

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

var (
	moduleRegistryMu sync.RWMutex
	moduleRegistry   = map[string]ModuleMcpFactory{}
)

func RegisterMcpModule(specifier string, factory ModuleMcpFactory) {
	moduleRegistryMu.Lock()
	defer moduleRegistryMu.Unlock()
	moduleRegistry[specifier] = factory
}

func lookupMcpModule(specifier string) (ModuleMcpFactory, bool) {
	moduleRegistryMu.RLock()
	defer moduleRegistryMu.RUnlock()
	factory, ok := moduleRegistry[specifier]
	return factory, ok
}

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

func standUpModuleLoopback(serverName string, specifier string, logger *slog.Logger) (url string, bearerToken string, teardown func() error, err error) {
	factory, ok := lookupMcpModule(specifier)
	if !ok {
		return "", "", nil, &CliConfigError{Message: "mcp server \"" + serverName + "\" module \"" + specifier +
			"\" is not a registered MCP module (register it via RegisterMcpModule)"}
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
