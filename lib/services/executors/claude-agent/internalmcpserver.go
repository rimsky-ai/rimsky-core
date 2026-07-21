// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

const mcpProtocolVersionFallback = "2025-03-26"

type mcpToolProvider interface {
	serverName() string
	listTools() []ToolDefinition
	callTool(name string, args json.RawMessage) (map[string]any, *jsonRPCError)
}

type mcpHTTPServer struct {
	Host string
	Port int
	URL  string

	server   *http.Server
	sessions *mcpSessionTable
	sweeper  *time.Ticker
	stop     chan struct{}
	stopOnce sync.Once
}

type mcpSessionTable struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

func newMcpSessionTable() *mcpSessionTable {
	return &mcpSessionTable{sessions: make(map[string]time.Time)}
}

func (t *mcpSessionTable) open(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessions[id] = time.Now()
}

func (t *mcpSessionTable) touch(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.sessions[id]; !ok {
		return false
	}
	t.sessions[id] = time.Now()
	return true
}

func (t *mcpSessionTable) evict(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, id)
}

func (t *mcpSessionTable) evictIdle(cutoff time.Time) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var evicted []string
	for id, last := range t.sessions {
		if last.Before(cutoff) {
			delete(t.sessions, id)
			evicted = append(evicted, id)
		}
	}
	return evicted
}

type mcpHTTPServerOpts struct {
	host        string
	port        int
	logger      *slog.Logger
	sessionIdle time.Duration
	bearerToken string
}

// @decision: internal-mcp-server-net-http
func startMcpHTTPServer(provider mcpToolProvider, opts mcpHTTPServerOpts) (*mcpHTTPServer, error) {
	host := opts.host
	if host == "" {
		host = "127.0.0.1"
	}
	log := opts.logger
	if log == nil {
		log = slog.Default()
	}
	sessionIdle := opts.sessionIdle
	if sessionIdle == 0 {
		sessionIdle = 10 * time.Minute
	}

	sessions := newMcpSessionTable()

	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(opts.port)))
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port

	srv := &mcpHTTPServer{
		Host:     host,
		Port:     port,
		URL:      fmt.Sprintf("http://%s:%d/mcp", host, port),
		sessions: sessions,
		stop:     make(chan struct{}),
	}

	bearerToken := opts.bearerToken
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if bearerToken != "" && r.Header.Get("Authorization") != "Bearer "+bearerToken {
			log.Warn("mcp.unauthorized", "reason", "missing_or_wrong_bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		serveMcpRequest(w, r, provider, sessions, log)
	})
	srv.server = &http.Server{Handler: mux}

	sweepInterval := sessionIdle / 4
	if sweepInterval < 30*time.Second {
		sweepInterval = 30 * time.Second
	}
	srv.sweeper = time.NewTicker(sweepInterval)
	go func() {
		for {
			select {
			case <-srv.stop:
				return
			case <-srv.sweeper.C:
				for _, id := range sessions.evictIdle(time.Now().Add(-sessionIdle)) {
					log.Info("mcp.session_closed", "session_id", id, "reason", "idle_timeout")
				}
			}
		}
	}()

	go func() {
		if err := srv.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error("mcp.http_server_error", "error", err.Error())
		}
	}()

	return srv, nil
}

func (s *mcpHTTPServer) Close() error {
	var err error
	s.stopOnce.Do(func() {
		close(s.stop)
		s.sweeper.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err = s.server.Shutdown(ctx); err != nil {
			err = s.server.Close()
		}
	})
	return err
}

type CallbackServerHandle struct {
	Host     string
	Port     int
	URL      string
	Registry *TokenRegistry

	server *mcpHTTPServer
}

type InternalMcpOpts struct {
	Host        string
	Port        int
	Logger      *slog.Logger
	SessionIdle time.Duration
}

func StartInternalMcpServer(opts InternalMcpOpts) (*CallbackServerHandle, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	log := logger.With("component", "internal-mcp")
	registry := NewTokenRegistry()
	srv, err := startMcpHTTPServer(
		&callbackToolProvider{registry: registry, log: log},
		mcpHTTPServerOpts{host: opts.Host, port: opts.Port, logger: log, sessionIdle: opts.SessionIdle},
	)
	if err != nil {
		return nil, err
	}
	return &CallbackServerHandle{
		Host:     srv.Host,
		Port:     srv.Port,
		URL:      srv.URL,
		Registry: registry,
		server:   srv,
	}, nil
}

func (h *CallbackServerHandle) Close() error {
	return h.server.Close()
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

func writeJSONRPC(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func serveMcpRequest(
	w http.ResponseWriter,
	r *http.Request,
	provider mcpToolProvider,
	sessions *mcpSessionTable,
	log *slog.Logger,
) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
			sessions.evict(sid)
			log.Info("mcp.session_closed", "session_id", sid, "reason", "client_delete")
		}
		w.WriteHeader(http.StatusOK)
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPC(w, http.StatusBadRequest, jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &jsonRPCError{Code: -32700, Message: "Parse error"},
		})
		return
	}

	sid := r.Header.Get("Mcp-Session-Id")
	if sid != "" {
		if !sessions.touch(sid) {
			log.Warn("mcp.unknown_session", "session_id", sid)
			writeJSONRPC(w, http.StatusNotFound, jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32001, Message: "Session not found"},
			})
			return
		}
	} else if req.Method != "initialize" {
		writeJSONRPC(w, http.StatusBadRequest, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32000, Message: "Bad Request: server not initialized"},
		})
		return
	}

	if req.ID == nil || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		if sid != "" {
			sessions.evict(sid)
			log.Info("mcp.session_reinitialized", "old_session_id", sid)
		}
		newSid := uuid.NewString()
		sessions.open(newSid)
		log.Info("mcp.session_opened", "session_id", newSid)
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = mcpProtocolVersionFallback
		}
		w.Header().Set("Mcp-Session-Id", newSid)
		writeJSONRPC(w, http.StatusOK, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{
					"name":    provider.serverName(),
					"version": "1.0.0",
				},
			},
		})
	case "ping":
		writeJSONRPC(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		defs := provider.listTools()
		tools := make([]map[string]any, 0, len(defs))
		for _, d := range defs {
			tools = append(tools, map[string]any{
				"name":        d.Name,
				"description": d.Description,
				"inputSchema": d.InputSchema,
			})
		}
		writeJSONRPC(w, http.StatusOK, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": tools},
		})
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &call); err != nil {
			writeJSONRPC(w, http.StatusOK, jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "Invalid params"},
			})
			return
		}
		result, rpcErr := provider.callTool(call.Name, call.Arguments)
		if rpcErr != nil {
			writeJSONRPC(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr})
			return
		}
		writeJSONRPC(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
	default:
		writeJSONRPC(w, http.StatusOK, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: "Method not found"},
		})
	}
}

func toolResultText(text string, isError bool) map[string]any {
	result := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
	if isError {
		result["isError"] = true
	}
	return result
}

type callbackToolProvider struct {
	registry *TokenRegistry
	log      *slog.Logger
}

func (p *callbackToolProvider) serverName() string { return CallbackMCPServerName }

func (p *callbackToolProvider) listTools() []ToolDefinition { return toolDefinitions() }

func (p *callbackToolProvider) callTool(name string, arguments json.RawMessage) (map[string]any, *jsonRPCError) {
	log := p.log
	registry := p.registry

	deferTeardown := func(td func() error) {
		go func() {
			if err := td(); err != nil {
				log.Warn("internal-mcp teardown failed", "error", err.Error())
			}
		}()
	}

	lookup := func(token string, tool string) (*TokenEntry, map[string]any) {
		entry, ok := registry.Lookup(token)
		if !ok {
			log.Warn("mcp.unknown_token", "tool", tool)
			return nil, toolResultText("unknown_token", true)
		}
		log.Info("mcp.tool_called", "tool", tool, "run_id", entry.SessionID)
		return entry, nil
	}

	invalidParams := func(detail string) (map[string]any, *jsonRPCError) {
		return nil, &jsonRPCError{Code: -32602, Message: "Invalid params: " + detail}
	}

	switch name {
	case "report_complete":
		var args struct {
			Token           string         `json:"token"`
			AttributesDelta map[string]any `json:"attributes_delta"`
			Changed         *bool          `json:"changed"`
			ChangeSummary   *string        `json:"change_summary"`
			Signoffs        []string       `json:"signoffs"`
		}
		if err := json.Unmarshal(arguments, &args); err != nil {
			return invalidParams(err.Error())
		}
		if args.Token == "" || args.Changed == nil {
			return invalidParams("token and changed are required")
		}
		entry, errResult := lookup(args.Token, "report_complete")
		if errResult != nil {
			return errResult, nil
		}
		outcome, err := entry.OnComplete(args.AttributesDelta, *args.Changed, args.ChangeSummary, args.Signoffs, deferTeardown)
		if err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
		}
		raw, err := json.Marshal(outcome)
		if err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
		}
		return toolResultText(string(raw), false), nil
	case "report_blocked":
		var args struct {
			Token   string  `json:"token"`
			Reason  *string `json:"reason"`
			Context any     `json:"context"`
		}
		if err := json.Unmarshal(arguments, &args); err != nil {
			return invalidParams(err.Error())
		}
		if args.Token == "" || args.Reason == nil {
			return invalidParams("token and reason are required")
		}
		entry, errResult := lookup(args.Token, "report_blocked")
		if errResult != nil {
			return errResult, nil
		}
		if err := entry.OnBlocked(*args.Reason, args.Context, deferTeardown); err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
		}
		return toolResultText(`{"status":"accepted"}`, false), nil
	case "report_error":
		var args struct {
			Token      string `json:"token"`
			ErrorClass string `json:"error_class"`
			Payload    any    `json:"payload"`
		}
		if err := json.Unmarshal(arguments, &args); err != nil {
			return invalidParams(err.Error())
		}
		if args.Token == "" || args.ErrorClass == "" {
			return invalidParams("token and error_class are required")
		}
		if !errorClassDeclared(args.ErrorClass) {
			return invalidParams(fmt.Sprintf(
				"error_class %q is not in this executor's declared vocabulary", args.ErrorClass))
		}
		entry, errResult := lookup(args.Token, "report_error")
		if errResult != nil {
			return errResult, nil
		}
		if err := entry.OnError(args.ErrorClass, args.Payload, deferTeardown); err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
		}
		return toolResultText(`{"status":"accepted"}`, false), nil
	case "report_park":
		var args struct {
			Token    string `json:"token"`
			ResumeAt string `json:"resume_at"`
		}
		if err := json.Unmarshal(arguments, &args); err != nil {
			return invalidParams(err.Error())
		}
		if args.Token == "" || args.ResumeAt == "" {
			return invalidParams("token and resume_at are required")
		}
		entry, errResult := lookup(args.Token, "report_park")
		if errResult != nil {
			return errResult, nil
		}
		if entry.OnPark == nil {
			return toolResultText("park_not_supported", true), nil
		}
		if err := entry.OnPark(args.ResumeAt, deferTeardown); err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
		}
		return toolResultText(`{"status":"accepted"}`, false), nil
	case "dispatch_context_read":
		var args struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(arguments, &args); err != nil || args.Token == "" {
			return invalidParams("token is required")
		}
		entry, errResult := lookup(args.Token, "dispatch_context_read")
		if errResult != nil {
			return errResult, nil
		}
		raw, err := json.Marshal(entry.DispatchContext)
		if err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
		}
		return toolResultText(string(raw), false), nil
	case "attributes_read":
		var args struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(arguments, &args); err != nil || args.Token == "" {
			return invalidParams("token is required")
		}
		entry, errResult := lookup(args.Token, "attributes_read")
		if errResult != nil {
			return errResult, nil
		}
		raw, err := json.Marshal(entry.AttributesAtSpawn)
		if err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: err.Error()}
		}
		return toolResultText(string(raw), false), nil
	default:
		return nil, &jsonRPCError{Code: -32602, Message: "Unknown tool: " + name}
	}
}
