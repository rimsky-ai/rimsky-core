// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package mcp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Server is the in-process MCP handler. Mounted at GET+POST /mcp inside
// control-api's chi router by registerMCPRoute. Speaks enough of the MCP
// Streamable HTTP transport for the default `type: http` client to
// connect and control:
//
//   - POST /mcp dispatches initialize / tools/list / tools/call /
//     resources/list / resources/read (calling back into the control-api
//     router for tool invocations, or directly into persistence for
//     resources). initialize issues a fresh Mcp-Session-Id response
//     header; any JSON-RPC notification (an id-less request, e.g.
//     notifications/initialized) is consumed with a 202/empty body and
//     never a JSON-RPC reply.
//   - GET /mcp opens a valid (idle, keep-alive) text/event-stream so the
//     client's server-to-client stream probe succeeds instead of 405.
//
// Server-initiated push (resources/subscribe +
// notifications/resources/updated, live event streaming) is out of v1
// (#7: connect-and-control only; live push is V2). The GET stream
// therefore stays idle — it exists so the probe succeeds, and pushes
// nothing in v1.
type Server struct {
	Tools     ToolCatalog
	Resources ResourceCatalog
}

// ToolCatalog is the dependency Server uses to render the tools/list
// response and dispatch tools/call.
type ToolCatalog interface {
	// @constraint: filtered returns the subset of the catalog that the requesting
	// identity is allowed to see, based on the per-request identity
	// already attached to r.Context() by the auth middleware.
	Filtered(r *http.Request) []Tool

	// @constraint: invoke runs the named tool by dispatching to its underlying
	// HTTP route. Returns the result (JSON-marshalable) or an
	// *Error.
	Invoke(r *http.Request, name string, args json.RawMessage) (any, *Error)
}

// ResourceCatalog is the dependency Server uses to render
// resources/list and resources/read responses. It mirrors
// ToolCatalog's identity-and-permission-aware shape. Per spec
// exposes only the polling-shaped subset (list + read); subscribe and
// server-pushed notifications require an MCP transport upgrade and are
// deferred to a future spec.
type ResourceCatalog interface {
	// @constraint: list returns the resources the requesting identity is allowed
	// to see, based on the identity attached to r.Context() by the
	// auth middleware.
	List(r *http.Request) ([]Resource, error)

	// @constraint: read fetches the contents of one resource by URI, gated by
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

// ServeHTTP routes a /mcp request by HTTP method. GET opens the
// server-to-client SSE stream (idle in v1); POST carries one JSON-RPC
// message (request or notification). Any other method is 405.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.serveStream(w, r)
	case http.MethodPost:
		s.servePost(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// servePost handles a single POST /mcp JSON-RPC message. A notification
// (an id-less JSON-RPC request, e.g. notifications/initialized) is
// consumed with a 202/empty body and never receives a JSON-RPC reply —
// replying to a notification is a JSON-RPC 2.0 violation and the default
// `type: http` client treats the spurious reply as a handshake failure.
func (s *Server) servePost(w http.ResponseWriter, r *http.Request) {
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
	// @constraint: A JSON-RPC notification carries no `id` (absent or JSON null). It
	// must be consumed with no response body — 202 Accepted, empty. This
	// covers notifications/initialized (the post-initialize handshake
	// step) and any other notifications/* the client emits. We branch on
	// the absence of an id rather than a method-name allowlist so an
	// unknown notification is still silently consumed, never answered.
	if isNotification(req.ID) {
		w.WriteHeader(http.StatusAccepted)
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

// serveStream answers the client's GET /mcp server-to-client stream probe
// with a valid text/event-stream. v1 has no server-initiated messages, so
// the stream stays idle: it flushes the 200 + headers immediately (so the
// client's probe succeeds), emits periodic SSE keep-alive comments to keep
// intermediaries from reaping the idle connection, and returns when the
// client disconnects (request context cancelled). No domain push is
// performed — connect-and-control only; live push is V2.
func (s *Server) serveStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if sid := r.Header.Get(sessionHeader); sid != "" {
		// @constraint: echo the session the client bound the stream to.
		w.Header().Set(sessionHeader, sid)
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(streamKeepAlive)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// @constraint: SSE comment line — a no-op keep-alive that carries no MCP
			// message (v1 pushes nothing). If the write fails the peer is
			// gone; ctx.Done will also fire, so just return.
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// sessionHeader is the MCP Streamable HTTP session-id header. initialize
// issues one; the client echoes it on every subsequent request.
const sessionHeader = "Mcp-Session-Id"

// streamKeepAlive is the idle GET-stream keep-alive interval.
const streamKeepAlive = 25 * time.Second

// isNotification reports whether a JSON-RPC envelope is a notification
// (no `id`). An absent id is empty RawMessage; an explicit `null` id also
// denotes a notification per JSON-RPC 2.0.
func isNotification(id json.RawMessage) bool {
	trimmed := bytes.TrimSpace(id)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// newSessionID mints a fresh opaque MCP session id. v1 is stateless
// beyond connect (there is no server-push state bound to a session), so
// the id only needs to be unique and opaque, not persisted.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// @constraint: crypto/rand failure is fatal-grade; fall back to a time-seed so
		// the handshake still completes rather than handing the client an
		// empty session id (which it would treat as no-session).
		return fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (s *Server) handleInitialize(w http.ResponseWriter, req Request) {
	// @constraint: issue a session id so the client can run as a session-aware
	// Streamable-HTTP peer (echoing the header on subsequent requests and
	// binding its GET stream to it). v1 holds no per-session state, so the
	// id is opaque and unvalidated beyond connect.
	w.Header().Set(sessionHeader, newSessionID())
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
		// @constraint: defensive: ResourceCatalog implementations shouldn't return
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
