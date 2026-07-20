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
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type Server struct {
	Tools     ToolCatalog
	Resources ResourceCatalog

	mu       sync.Mutex
	sessions map[string]time.Time
}

type ToolCatalog interface {
	Filtered(r *http.Request) []Tool

	Invoke(r *http.Request, name string, args json.RawMessage) (result any, isError bool, err *Error)
}

type ResourceCatalog interface {
	List(r *http.Request) ([]Resource, error)

	Read(r *http.Request, uri string) (*ResourceContents, *Error)
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	MimeType    string `json:"mimeType"`
	Description string `json:"description,omitempty"`
}

type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.serveStream(w, r)
	case http.MethodPost:
		s.servePost(w, r)
	case http.MethodDelete:
		s.serveDelete(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

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
	if req.Method != "initialize" {
		sid := r.Header.Get(sessionHeader)
		if sid == "" {
			writeRPCErrorStatus(w, http.StatusBadRequest, req.ID, CodeSessionRequired, "missing "+sessionHeader+" header: call initialize first")
			return
		}
		if !s.touchSession(sid) {
			writeRPCErrorStatus(w, http.StatusNotFound, req.ID, CodeSessionNotFound, "unknown or terminated session: "+sid)
			return
		}
	}
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

func (s *Server) serveDelete(w http.ResponseWriter, r *http.Request) {
	if sid := r.Header.Get(sessionHeader); sid != "" {
		s.closeSession(sid)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) serveStream(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get(sessionHeader)
	if sid == "" {
		http.Error(w, "missing "+sessionHeader+" header: call initialize first", http.StatusBadRequest)
		return
	}
	if !s.touchSession(sid) {
		http.Error(w, "unknown or terminated session: "+sid, http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set(sessionHeader, sid)
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
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

const sessionHeader = "Mcp-Session-Id"

const streamKeepAlive = 25 * time.Second

const sessionIdleTimeout = 30 * time.Minute

func (s *Server) openSession() string {
	id := newSessionID()
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]time.Time)
	}
	for existing, last := range s.sessions {
		if now.Sub(last) > sessionIdleTimeout {
			delete(s.sessions, existing)
		}
	}
	s.sessions[id] = now
	return id
}

func (s *Server) touchSession(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.sessions[id]
	if !ok {
		return false
	}
	if time.Since(last) > sessionIdleTimeout {
		delete(s.sessions, id)
		return false
	}
	s.sessions[id] = time.Now()
	return true
}

func (s *Server) closeSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func isNotification(id json.RawMessage) bool {
	trimmed := bytes.TrimSpace(id)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

const defaultProtocolVersion = "2025-06-18"

var supportedProtocolVersions = []string{defaultProtocolVersion}

func negotiateProtocolVersion(requested string) string {
	if requested == "" {
		return defaultProtocolVersion
	}
	for _, v := range supportedProtocolVersions {
		if v == requested {
			return requested
		}
	}
	return defaultProtocolVersion
}

func (s *Server) handleInitialize(w http.ResponseWriter, req Request) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}
	version := negotiateProtocolVersion(params.ProtocolVersion)
	sid := s.openSession()
	w.Header().Set(sessionHeader, sid)
	writeRPCResult(w, req.ID, map[string]any{
		"protocolVersion": version,
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
	result, isError, rpcErr := s.Tools.Invoke(r, p.Name, p.Arguments)
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
		"isError": isError,
	})
}

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
		writeRPCError(w, req.ID, CodeInternalError, "resources/read: empty contents from catalog")
		return
	}
	writeRPCResult(w, req.ID, map[string]any{
		"contents": []ResourceContents{*contents},
	})
}

func normalizeID(id json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(id)) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	resp := Response{JSONRPC: "2.0", ID: normalizeID(id), Result: result}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Default().Error("mcp.write_response_failed", "error", err.Error())
	}
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeRPCErrorObj(w, id, &Error{Code: code, Message: msg})
}

func writeRPCErrorObj(w http.ResponseWriter, id json.RawMessage, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	resp := Response{JSONRPC: "2.0", ID: normalizeID(id), Error: e}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Default().Error("mcp.write_response_failed", "error", err.Error())
	}
}

func writeRPCErrorStatus(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := Response{JSONRPC: "2.0", ID: normalizeID(id), Error: &Error{Code: code, Message: msg}}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Default().Error("mcp.write_response_failed", "error", err.Error())
	}
}
