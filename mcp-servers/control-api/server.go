// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package controlapimcp implements a JSON-RPC 2.0 over HTTP MCP server
// that wraps the rimsky control-API HTTP surface as a tool catalog.
//
// Per plan K2: implements `initialize`, `tools/list`, `tools/call` over
// POST /mcp using stdlib encoding/json + go-chi/chi. No third-party MCP
// SDK; the wire surface is small enough that a direct implementation is
// cleaner.
//
// Auth: forwards a configured operator token (Authorization: Bearer
// <CONTROL_API_TOKEN>) to the underlying control-API. The shim does
// not enforce its own auth; the wrapper relies on operators isolating
// the shim port.
package controlapimcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
)

// Config bundles the operator-supplied settings.
type Config struct {
	// ControlAPIURL is the absolute base URL of the rimsky control-API
	// (e.g. "http://127.0.0.1:8080"). No trailing slash.
	ControlAPIURL string
	// ControlAPIToken is the bearer token forwarded as
	// `Authorization: Bearer ...` on every wrapped call. Empty → no
	// Authorization header is sent.
	ControlAPIToken string
}

// Validate rejects a malformed config.
func (c Config) Validate() error {
	if c.ControlAPIURL == "" {
		return errors.New("control_api_url is required")
	}
	return nil
}

// Server is the per-process MCP shim. Holds the configured client and
// the tool catalog.
type Server struct {
	cfg     Config
	client  *http.Client
	mu      sync.RWMutex
	tools   []Tool
	handler map[string]ToolHandler
}

// NewServer constructs the shim. Returns an error when cfg is invalid.
func NewServer(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	s := &Server{
		cfg:     cfg,
		client:  &http.Client{},
		handler: map[string]ToolHandler{},
	}
	s.registerCoreTools()
	return s, nil
}

// Routes returns a chi.Router mounting the MCP endpoint.
func (s *Server) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/mcp", s.handleMCP)
	return r
}

// rpcRequest is a JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcResponse is a JSON-RPC 2.0 response envelope.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// handleMCP dispatches POST /mcp by method.
func (s *Server) handleMCP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var rpc rpcRequest
	if err := json.Unmarshal(body, &rpc); err != nil {
		s.writeRPC(w, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
		})
		return
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: rpc.ID}
	switch rpc.Method {
	case "initialize":
		resp.Result = s.handleInitialize()
	case "tools/list":
		resp.Result = s.handleToolsList()
	case "tools/call":
		result, rerr := s.handleToolsCall(req.Context(), rpc.Params)
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + rpc.Method}
	}
	s.writeRPC(w, resp)
}

func (s *Server) writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Last-ditch logging via the stdlib stderr (the shim is
		// process-local; no slog logger is wired by default).
		_, _ = fmt.Fprintf(w, `{"error":"encode response: %s"}`, err.Error())
	}
}

// handleInitialize returns the MCP protocol "hello" payload.
func (s *Server) handleInitialize() any {
	return map[string]any{
		"protocolVersion": "2025-06-18",
		"serverInfo": map[string]string{
			"name":    "rimsky-control-api",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
	}
}

// handleToolsList returns the registered tool catalog.
func (s *Server) handleToolsList() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{"tools": s.tools}
}

// toolCallParams is the params shape for tools/call.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// handleToolsCall dispatches by tool name.
func (s *Server) handleToolsCall(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params toolCallParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	s.mu.RLock()
	h, ok := s.handler[params.Name]
	s.mu.RUnlock()
	if !ok {
		return nil, &rpcError{Code: -32601, Message: "unknown tool: " + params.Name}
	}
	out, err := h(ctx, params.Arguments)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	// MCP tool-result format: { content: [{ type: "text", text: <json> }] }
	bs, err := json.Marshal(out)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: "marshal result: " + err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(bs)},
		},
	}, nil
}

// Tool describes one MCP tool.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolHandler is the function signature for each tool's handler.
type ToolHandler func(ctx context.Context, args json.RawMessage) (any, error)

// RegisterTool adds a tool to the catalog.
func (s *Server) RegisterTool(t Tool, h ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append(s.tools, t)
	s.handler[t.Name] = h
}
