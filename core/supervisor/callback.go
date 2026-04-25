// Port of rimsky/src/supervisor/callback.ts (Plan A Task 10.5). HTTP
// callback endpoint that async-handoff executors POST to with their final
// outcome. See spec §7.2 "async-handoff" path.
//
// Runners register an AsyncContext when an executor returns AsyncAccepted;
// the endpoint resolves ackID → context, classifies the body as a
// TerminalOutcome, applies it via ApplyTerminalOutcome, and then clears the
// dispatch row.
package supervisor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// CallbackRegistry tracks pending async executions. Runners register an
// AsyncContext (defined in runner.go) when an executor returns
// AsyncAccepted; the HTTP endpoint resolves ackID to the context on callback.
type CallbackRegistry struct {
	mu      sync.RWMutex
	pending map[string]AsyncContext
}

// NewCallbackRegistry returns an empty registry.
func NewCallbackRegistry() *CallbackRegistry {
	return &CallbackRegistry{pending: map[string]AsyncContext{}}
}

// Register records an AsyncContext under the given ackID.
func (r *CallbackRegistry) Register(ackID string, ctx AsyncContext) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[ackID] = ctx
}

// Pop returns and removes the AsyncContext for ackID. The bool is false when
// ackID is unknown.
func (r *CallbackRegistry) Pop(ackID string) (AsyncContext, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.pending[ackID]
	if ok {
		delete(r.pending, ackID)
	}
	return c, ok
}

// CallbackServer is the supervisor's HTTP endpoint for async executor callbacks.
type CallbackServer struct {
	Registry *CallbackRegistry
	Storage  storage.StorageBackend
	Queue    queue.DispatchQueue
	Clock    shared.Clock
	Logger   shared.Logger
	addr     string
	srv      *http.Server
}

// Start listens on host:port (port=0 for OS-assigned). Safe to call before
// any callbacks are registered. Returns the bound address.
func (c *CallbackServer) Start(host string, port int) (string, error) {
	if c.Logger == nil {
		c.Logger = shared.SilentLogger{}
	}
	r := chi.NewRouter()
	r.Post("/v1/callback/{ackID}", c.handleCallback)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	listener, err := net.Listen("tcp", net.JoinHostPort(host, portToStr(port)))
	if err != nil {
		return "", err
	}
	c.addr = listener.Addr().String()
	c.srv = &http.Server{Handler: r}
	go func() { _ = c.srv.Serve(listener) }()
	return c.addr, nil
}

// Addr returns the bound address of the running server (empty before Start).
func (c *CallbackServer) Addr() string { return c.addr }

// Close shuts down the server, honoring ctx for deadline.
func (c *CallbackServer) Close(ctx context.Context) error {
	if c.srv == nil {
		return nil
	}
	return c.srv.Shutdown(ctx)
}

type callbackBody struct {
	Type string `json:"type"` // "complete" | "blocked" | "errored"
	// Complete fields:
	Result        any    `json:"result,omitempty"`
	Changed       bool   `json:"changed,omitempty"`
	ChangeSummary string `json:"change_summary,omitempty"`
	// Blocked fields:
	Reason  string `json:"reason,omitempty"`
	Context any    `json:"context,omitempty"`
	// Errored fields:
	ErrorClass string `json:"error_class,omitempty"`
	Payload    any    `json:"payload,omitempty"`
}

func (c *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	ackID := chi.URLParam(r, "ackID")
	if ackID == "" {
		http.Error(w, `{"error":"missing ackID"}`, http.StatusBadRequest)
		return
	}
	asyncCtx, ok := c.Registry.Pop(ackID)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unknown_async_ack_id"}`))
		return
	}
	var body callbackBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// Re-register the async context since we didn't actually apply the callback.
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	var outcome TerminalOutcome
	switch strings.ToLower(body.Type) {
	case "complete":
		outcome = TerminalOutcome{
			Kind:          TerminalRunSucceeded,
			Result:        body.Result,
			Changed:       body.Changed,
			ChangeSummary: body.ChangeSummary,
		}
	case "blocked":
		outcome = TerminalOutcome{
			Kind:       TerminalAppError,
			ErrorClass: "executor_blocked",
			Payload:    map[string]any{"reason": body.Reason, "context": body.Context},
		}
	case "errored":
		outcome = TerminalOutcome{
			Kind:       TerminalAppError,
			ErrorClass: body.ErrorClass,
			Payload:    map[string]any{"payload": body.Payload},
		}
	default:
		// Re-register the async context since we didn't actually apply the callback.
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"unknown callback type"}`, http.StatusBadRequest)
		return
	}
	// Apply outcome.
	if err := ApplyTerminalOutcome(r.Context(), ApplyTerminalArgs{
		Storage: c.Storage, Queue: c.Queue, Clock: c.Clock, Logger: c.Logger,
		NodeID: asyncCtx.NodeID, InstanceID: asyncCtx.InstanceID,
		SupervisorID: asyncCtx.SupervisorID,
		GetResource:  asyncCtx.GetResource,
		Outcome:      outcome,
	}); err != nil {
		// Re-register so the executor can retry. If we didn't, a transient
		// failure would leave the node stuck in `running` forever — the
		// callback would never correlate on retry.
		c.Registry.Register(ackID, asyncCtx)
		c.Logger.Warn("callback: ApplyTerminalOutcome failed",
			"node_id", asyncCtx.NodeID.String(), "error", err.Error())
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	// After outcome applies, clean up the dispatch row via complete.
	_ = c.Queue.Complete(r.Context(), asyncCtx.DispatchID, asyncCtx.SupervisorID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}
