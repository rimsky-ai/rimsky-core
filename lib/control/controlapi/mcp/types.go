// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package mcp is the in-control-api MCP protocol skin. Tools-only V1
// (no resources, prompts, or subscriptions); HTTP transport only;
// shared TCP port with the HTTP+JSON skin. The package re-uses the
// JSON-RPC envelope and tool-catalog shapes that the pre-spec
// standalone mcp-servers/control-api/ module curated by hand.
//
// @concept: control-api
package mcp

import "encoding/json"

// Request is one JSON-RPC 2.0 request envelope.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is one JSON-RPC 2.0 response envelope.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is one JSON-RPC 2.0 error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool is one MCP tool descriptor returned by tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Registry is the dependency the catalog uses to enumerate tools and
// resolve a tool to its action + route. Implemented by controlapi/
// (see actionRegistryAdapter); shaped here so the mcp package
// doesn't import controlapi.
type Registry interface {
	AllTools() []string
	EntryForTool(name string) (RegistryEntry, bool)
}

// RegistryEntry mirrors controlapi.ActionEntry but lives in the mcp
// package so the package compiles without a back-import.
type RegistryEntry struct {
	Action      string
	IsWrite     bool
	Routes      []RegistryRoute
	Description string
}

// RegistryRoute mirrors controlapi.Route.
type RegistryRoute struct {
	Method string
	Path   string
}
