// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"errors"
	"fmt"
	"testing"
)

func testModules(specifier string, factory ModuleMcpFactory) map[string]ModuleMcpFactory {
	return map[string]ModuleMcpFactory{specifier: factory}
}

func TestStandUpModuleLoopback_UnregisteredModuleErrors(t *testing.T) {
	_, _, _, err := standUpModuleLoopback("srv", "not-a-registered-module", nil, nil)
	if err == nil {
		t.Fatal("expected an error for an unregistered module specifier, got nil")
	}
	var cfgErr *CliConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected a *CliConfigError, got %T: %v", err, err)
	}
}

func TestStandUpModuleLoopback_ServesRegisteredModuleToolsOverHTTP(t *testing.T) {
	modules := testModules("test-module-echo", func() *ModuleMcpServer {
		return &ModuleMcpServer{
			Name: "echo-server",
			Tools: []ModuleMcpTool{{
				Definition: ToolDefinition{Name: "echo", Description: "echoes back the message arg"},
				Handler: func(args map[string]any) (string, bool, error) {
					msg, _ := args["message"].(string)
					return "echo:" + msg, false, nil
				},
			}},
		}
	})

	url, token, teardown, err := standUpModuleLoopback("my-mcp", "test-module-echo", modules, nil)
	if err != nil {
		t.Fatalf("standUpModuleLoopback: %v", err)
	}
	t.Cleanup(func() { _ = teardown() })

	client := &mcpTestClient{t: t, url: url, authHeader: "Bearer " + token}
	client.initialize()
	if client.serverName != "echo-server" {
		t.Fatalf("serverInfo.name = %q, want %q", client.serverName, "echo-server")
	}

	result, rpcErr := client.callTool("echo", `{"message":"hi"}`)
	if rpcErr != nil {
		t.Fatalf("callTool(echo): %+v", rpcErr)
	}
	if got := client.firstText(result); got != "echo:hi" {
		t.Fatalf("callTool(echo) text = %q, want %q", got, "echo:hi")
	}
}

func TestStandUpModuleLoopback_UnknownToolIsJSONRPCError(t *testing.T) {
	modules := testModules("test-module-empty", func() *ModuleMcpServer {
		return &ModuleMcpServer{Name: "empty-server"}
	})

	url, token, teardown, err := standUpModuleLoopback("my-mcp", "test-module-empty", modules, nil)
	if err != nil {
		t.Fatalf("standUpModuleLoopback: %v", err)
	}
	t.Cleanup(func() { _ = teardown() })

	client := &mcpTestClient{t: t, url: url, authHeader: "Bearer " + token}
	client.initialize()

	_, rpcErr := client.callTool("nonexistent", `{}`)
	if rpcErr == nil {
		t.Fatal("callTool(nonexistent): expected a JSON-RPC error, got nil")
	}
}

func TestStandUpModuleLoopback_ToolHandlerErrorMapsToJSONRPCError(t *testing.T) {
	modules := testModules("test-module-failing", func() *ModuleMcpServer {
		return &ModuleMcpServer{
			Name: "failing-server",
			Tools: []ModuleMcpTool{{
				Definition: ToolDefinition{Name: "boom"},
				Handler: func(map[string]any) (string, bool, error) {
					return "", false, fmt.Errorf("boom: internal failure")
				},
			}},
		}
	})

	url, token, teardown, err := standUpModuleLoopback("my-mcp", "test-module-failing", modules, nil)
	if err != nil {
		t.Fatalf("standUpModuleLoopback: %v", err)
	}
	t.Cleanup(func() { _ = teardown() })

	client := &mcpTestClient{t: t, url: url, authHeader: "Bearer " + token}
	client.initialize()

	_, rpcErr := client.callTool("boom", `{}`)
	if rpcErr == nil {
		t.Fatal("callTool(boom): expected a JSON-RPC error when the handler returns an error, got nil")
	}
}

func TestStandUpModuleLoopback_RejectsUnauthenticatedRequests(t *testing.T) {
	modules := testModules("test-module-auth", func() *ModuleMcpServer {
		return &ModuleMcpServer{Name: "auth-server"}
	})

	url, _, teardown, err := standUpModuleLoopback("my-mcp", "test-module-auth", modules, nil)
	if err != nil {
		t.Fatalf("standUpModuleLoopback: %v", err)
	}
	t.Cleanup(func() { _ = teardown() })

	client := &mcpTestClient{t: t, url: url}
	resp, _ := client.post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`, "")
	if resp.StatusCode == 200 {
		t.Fatal("expected a non-200 status for a request with no bearer token")
	}
}
