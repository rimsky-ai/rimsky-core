// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"sort"
	"testing"
)

var routedActionsDeliberatelyWithoutAnMCPTool = map[string]string{
	"mcp:read": "the MCP transport cannot be a tool on itself; an MCP caller is already speaking it",
}

// @decision: mcp-http-parity
// @story: mcp-transport
func TestEveryRoutedActionHasAnMCPCounterpart(t *testing.T) {
	reg := BuildV1Registry()
	schemas := builtinSchemas()

	var gaps []string
	for _, action := range reg.AllActions() {
		entry, ok := reg.Entry(action)
		if !ok {
			t.Fatalf("registry reported action %q it cannot resolve", action)
		}
		if len(entry.Routes) == 0 {
			continue
		}
		if _, exempt := routedActionsDeliberatelyWithoutAnMCPTool[action]; exempt {
			if len(entry.MCPTools) > 0 {
				t.Errorf("action %q is listed as deliberately tool-free but registers MCP tools %v", action, entry.MCPTools)
			}
			continue
		}
		tools := append([]string{}, entry.MCPTools...)
		for _, r := range entry.Routes {
			if r.Tool != "" {
				tools = append(tools, r.Tool)
			}
		}
		if len(tools) == 0 {
			gaps = append(gaps, action)
			continue
		}
		for _, tool := range tools {
			if len(schemas[tool]) == 0 {
				t.Errorf("MCP tool %q (action %q) has no input schema in builtinSchemas; an agent cannot call a tool whose arguments are undeclared", tool, action)
			}
		}
	}
	sort.Strings(gaps)
	for _, action := range gaps {
		t.Errorf("action %q is reachable over HTTP but has no MCP tool; the MCP surface is a full-parity projection of the HTTP surface, so a new action ships with its tool or is named in routedActionsDeliberatelyWithoutAnMCPTool", action)
	}
}

// @decision: mcp-http-parity
func TestEveryMCPToolResolvesBackToItsAction(t *testing.T) {
	reg := BuildV1Registry()
	for _, tool := range reg.AllTools() {
		entry, ok := reg.EntryForTool(tool)
		if !ok {
			t.Errorf("MCP tool %q does not resolve to an action", tool)
			continue
		}
		if len(entry.Routes) == 0 {
			t.Errorf("MCP tool %q resolves to action %q, which has no HTTP route to re-dispatch through", tool, entry.Action)
		}
	}
}

// @decision: mcp-http-parity
func TestBuiltinSchemasDeclareNoToolTheRegistryDoesNotKnow(t *testing.T) {
	reg := BuildV1Registry()
	known := map[string]bool{}
	for _, tool := range reg.AllTools() {
		known[tool] = true
	}
	for tool := range builtinSchemas() {
		if !known[tool] {
			t.Errorf("builtinSchemas declares %q, which no action registers; a schema with no tool is dead weight an agent never sees", tool)
		}
	}
}
