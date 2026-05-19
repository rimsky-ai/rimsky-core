// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Server is the in-process MCP handler. Mounted at POST /mcp inside
// control-api's chi router by registerMCPRoute. Dispatches
// initialize / tools/list / tools/call by calling back into the
// control-api router for tool invocations.
type Server struct {
	Tools ToolCatalog
}

// ToolCatalog is the dependency Server uses to render the tools/list
// response and dispatch tools/call.
type ToolCatalog interface {
	// Filtered returns the subset of the catalog that the requesting
	// identity is allowed to see, based on the per-request identity
	// already attached to r.Context() by the auth middleware.
	Filtered(r *http.Request) []Tool

	// Invoke runs the named tool by dispatching to its underlying
	// HTTP route. Returns the result (JSON-marshalable) or an
	// *Error.
	Invoke(r *http.Request, name string, args json.RawMessage) (any, *Error)
}

// ServeHTTP handles a single POST /mcp JSON-RPC request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeRPCError(w, nil, CodeParseError, "read body: "+err.Error())
		return
	}
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, CodeParseError, "invalid JSON-RPC: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, CodeInvalidRequest, "jsonrpc must be 2.0")
		return
	}
	switch req.Method {
	case "initialize":
		s.handleInitialize(w, req)
	case "tools/list":
		s.handleToolsList(w, r, req)
	case "tools/call":
		s.handleToolsCall(w, r, req)
	default:
		writeRPCError(w, req.ID, CodeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) handleInitialize(w http.ResponseWriter, req Request) {
	writeRPCResult(w, req.ID, map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "rimsky-control-api",
			"version": "v1",
		},
	})
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request, req Request) {
	writeRPCResult(w, req.ID, map[string]any{
		"tools": s.Tools.Filtered(r),
	})
}

func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request, req Request) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, CodeInvalidParams, "invalid params: "+err.Error())
		return
	}
	result, rpcErr := s.Tools.Invoke(r, p.Name, p.Arguments)
	if rpcErr != nil {
		writeRPCErrorObj(w, req.ID, rpcErr)
		return
	}
	bs, err := json.Marshal(result)
	if err != nil {
		writeRPCError(w, req.ID, CodeInternalError, "marshal result: "+err.Error())
		return
	}
	writeRPCResult(w, req.ID, map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": string(bs)},
		},
	})
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	resp := Response{JSONRPC: "2.0", ID: id, Result: result}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		_, _ = fmt.Fprintf(w, `{"error":"encode: %s"}`, err.Error())
	}
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeRPCErrorObj(w, id, &Error{Code: code, Message: msg})
}

func writeRPCErrorObj(w http.ResponseWriter, id json.RawMessage, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	resp := Response{JSONRPC: "2.0", ID: id, Error: e}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		_, _ = fmt.Fprintf(w, `{"error":"encode: %s"}`, err.Error())
	}
}
