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
// initialize / tools/list / tools/call / resources/list / resources/read
// by calling back into the control-api router (for tool invocations) or
// directly into persistence (for resources). Push semantics
// (resources/subscribe + notifications/resources/updated) are out of v1
// scope per spec .ok-planner/specs/2026-05-24-instance-debugger-design.md §6.
type Server struct {
	Tools     ToolCatalog
	Resources ResourceCatalog
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

// ResourceCatalog is the dependency Server uses to render
// resources/list and resources/read responses. It mirrors
// ToolCatalog's identity-and-permission-aware shape. Per spec
// .ok-planner/specs/2026-05-24-instance-debugger-design.md §6, v1
// exposes only the polling-shaped subset (list + read); subscribe and
// server-pushed notifications require an MCP transport upgrade and are
// deferred to a future spec.
type ResourceCatalog interface {
	// List returns the resources the requesting identity is allowed
	// to see, based on the identity attached to r.Context() by the
	// auth middleware.
	List(r *http.Request) ([]Resource, error)

	// Read fetches the contents of one resource by URI, gated by
	// permission (the implementation gates against breakpoint:read
	// for breakpoint-hits URIs). Returns the response body shape
	// per spec §6.4. The returned *Error, when non-nil, carries the
	// JSON-RPC code (CodeInvalidParams for bad URIs, an internal
	// code for permission denials shaped as a -32603 "not found" so
	// existence isn't leaked, etc.).
	Read(r *http.Request, uri string) (*ResourceContents, *Error)
}

// Resource is one resources/list entry.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	MimeType    string `json:"mimeType"`
	Description string `json:"description,omitempty"`
}

// ResourceContents is one resources/read response entry. Text is the
// JSON-encoded body per spec §6.4 (a string, not nested JSON, per the
// MCP resource contents wire shape).
type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
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
	case "resources/list":
		s.handleResourcesList(w, r, req)
	case "resources/read":
		s.handleResourcesRead(w, r, req)
	default:
		writeRPCError(w, req.ID, CodeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) handleInitialize(w http.ResponseWriter, req Request) {
	writeRPCResult(w, req.ID, map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{"subscribe": false, "listChanged": false},
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

// handleResourcesList dispatches the resources/list call. Returns an
// empty list when no Resources catalog is wired (so tools-only
// deployments stay backward-compatible).
func (s *Server) handleResourcesList(w http.ResponseWriter, r *http.Request, req Request) {
	if s.Resources == nil {
		writeRPCResult(w, req.ID, map[string]any{"resources": []Resource{}})
		return
	}
	res, err := s.Resources.List(r)
	if err != nil {
		writeRPCError(w, req.ID, CodeInternalError, "resources/list: "+err.Error())
		return
	}
	if res == nil {
		res = []Resource{}
	}
	writeRPCResult(w, req.ID, map[string]any{"resources": res})
}

// handleResourcesRead dispatches the resources/read call. Per MCP
// spec the params carry `{"uri": "<uri>"}`; the resolver maps the URI
// to a persistence read and returns a contents envelope.
func (s *Server) handleResourcesRead(w http.ResponseWriter, r *http.Request, req Request) {
	if s.Resources == nil {
		writeRPCError(w, req.ID, CodeMethodNotFound, "resources/read: no resource catalog wired")
		return
	}
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, CodeInvalidParams, "invalid params: "+err.Error())
		return
	}
	if p.URI == "" {
		writeRPCError(w, req.ID, CodeInvalidParams, "uri is required")
		return
	}
	contents, rpcErr := s.Resources.Read(r, p.URI)
	if rpcErr != nil {
		writeRPCErrorObj(w, req.ID, rpcErr)
		return
	}
	if contents == nil {
		// Defensive: ResourceCatalog implementations shouldn't return
		// (nil, nil), but if they do, surface as an internal error so
		// the agent isn't left holding a malformed response.
		writeRPCError(w, req.ID, CodeInternalError, "resources/read: empty contents from catalog")
		return
	}
	writeRPCResult(w, req.ID, map[string]any{
		"contents": []ResourceContents{*contents},
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
