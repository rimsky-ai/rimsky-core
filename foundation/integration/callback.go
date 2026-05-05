// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Spec §12.4 — async-handoff terminal callback. Executors that returned
// AsyncAccepted POST a TerminalEvent JSON body to
// `POST {callback_url}/v1/callback/{async_ack_id}`. The CallbackRegistry
// maps the ack id back to the per-run AsyncContext the runner registered
// at handoff time; this file's HTTP handler classifies the body, builds
// a `terminalEvent`, and drives the same `applyTerminal*` flow that the
// synchronous executor-RPC path runs in `runner_terminal.go`.
//
// Body shape mirrors the gRPC `TerminalEvent` (spec §12.3 — HTTP+JSON
// bridge): top-level `type` keys the discriminator (Complete / Blocked /
// Errored), body carries the per-kind fields. The chi route param is
// `{async_ack_id}` (spec §12.4); the internal handler variable is named
// `ackID` for brevity.
//
// The dispatch row's frame_id is preserved across async handoff; the
// callback resolution path commits cascade message-passes that inherit
// the parent's frame_id (see core/supervisor/runner_terminal.go and
// docs/history/2026-04-26-frame-resolution-design.md §9).
package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	rimskyattrs "github.com/fallguy/rimsky/modeling/attribute"
	"github.com/fallguy/rimsky/modeling/shared"
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
//
// Persist, Queue, AdvisoryLocker, LockHolders, and ResumeGrace are required
// for driving the terminal-handling tx in `runner_terminal.go::applyTerminal*`
// (per spec §7.6 / §7.3 step 6c). They are populated by the supervisor at
// startup and threaded through here so the callback handler can run
// the exact same flow the synchronous executor-RPC path runs.
//
// SupervisorID is the running supervisor's ID. Used by the §12.5
// attributes-callback auth path to verify that an inbound `cancel_token`
// matches the dispatch row's `claimed_by` (i.e. this supervisor still owns
// the running window).
type CallbackServer struct {
	Registry       *CallbackRegistry
	Persist        persistence.Store
	Queue          persistence.Queue
	AdvisoryLocker persistence.AdvisoryLocker
	LockHolders    persistence.LockHoldersStore
	Clock          shared.Clock
	Logger         shared.Logger
	SupervisorID   string
	// ResumeGrace is forwarded as `RunArgs.ResumeGrace` when driving the
	// terminal flow. Zero falls back to the runner's 30-minute default
	// (see `releaseLocksInTx`).
	ResumeGrace time.Duration
	addr        string
	srv         *http.Server
}

// Start listens on host:port (port=0 for OS-assigned). Safe to call before
// any callbacks are registered. Returns the bound address.
func (c *CallbackServer) Start(host string, port int) (string, error) {
	if c.Logger == nil {
		c.Logger = shared.SilentLogger{}
	}
	r := chi.NewRouter()
	// Spec §12.4: the chi-route param is `{async_ack_id}`. The internal
	// handler reads it as `ackID` for brevity.
	r.Post("/v1/callback/{async_ack_id}", c.handleCallback)
	// Spec §12.5: incremental attributes writeback. Mounted on the same
	// listener as the async terminal callback so executors can reach both
	// at the supervisor's advertised callback URL.
	if c.Persist != nil {
		r.Method(http.MethodPost, "/v1/attributes/{node_id}", rimskyattrs.Handler(rimskyattrs.HandlerDeps{
			Store:  attributesStoreAdapter{inner: c.Persist.NodeAttributes()},
			Auth:   c.attributesAuth,
			Logger: c.Logger,
		}))
	}
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
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

// callbackBody mirrors the §12.3 HTTP+JSON shape for `TerminalEvent`.
// The discriminator key is `type` (preserved from the existing chi
// convention). The Complete branch carries `attributes_delta` per spec
// §12.2 (the legacy `result` field is retired).
type callbackBody struct {
	Type string `json:"type"` // "complete" | "blocked" | "errored"
	// Complete fields:
	AttributesDelta map[string]any `json:"attributes_delta,omitempty"`
	Changed         bool           `json:"changed,omitempty"`
	ChangeSummary   string         `json:"change_summary,omitempty"`
	// Blocked fields:
	Reason  string `json:"reason,omitempty"`
	Context any    `json:"context,omitempty"`
	// Errored fields:
	ErrorClass string `json:"error_class,omitempty"`
	Payload    any    `json:"payload,omitempty"`
}

func (c *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	ackID := chi.URLParam(r, "async_ack_id")
	if ackID == "" {
		http.Error(w, `{"error":"missing async_ack_id"}`, http.StatusBadRequest)
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
	t, ok := classifyCallbackBody(body)
	if !ok {
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"unknown callback type"}`, http.StatusBadRequest)
		return
	}

	if err := c.driveTerminal(r.Context(), asyncCtx, t); err != nil {
		// Re-register so the executor can retry. If we didn't, a transient
		// failure would leave the node stuck in `running` forever — the
		// callback would never correlate on retry.
		c.Registry.Register(ackID, asyncCtx)
		c.Logger.Warn("callback: driveTerminal failed",
			"node_id", asyncCtx.NodeID.String(), "error", err.Error())
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	// After outcome applies, clean up the dispatch row. Mirror the
	// synchronous-runner path in supervisor.go that calls Queue.Complete
	// after a non-async run.
	_ = c.Queue.Complete(r.Context(), asyncCtx.DispatchID, asyncCtx.SupervisorID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}

// classifyCallbackBody folds the §12.3 callback body into the
// runner-internal `terminalEvent`. Returns ok=false on an unknown
// `type` discriminator. Blocked is mapped to error class
// `executor_blocked` per spec §12.2 (the supervisor routes the policy
// chain on that class, defaulting to give_up).
func classifyCallbackBody(body callbackBody) (terminalEvent, bool) {
	switch strings.ToLower(body.Type) {
	case "complete":
		return terminalEvent{
			Kind:          terminalKindComplete,
			Changed:       body.Changed,
			ChangeSummary: body.ChangeSummary,
			AttributesDel: body.AttributesDelta,
		}, true
	case "blocked":
		return terminalEvent{
			Kind:       terminalKindBlocked,
			ErrorClass: "executor_blocked",
			Payload:    map[string]any{"reason": body.Reason, "context": body.Context},
		}, true
	case "errored":
		return terminalEvent{
			Kind:       terminalKindErrored,
			ErrorClass: body.ErrorClass,
			Payload:    map[string]any{"payload": body.Payload},
		}, true
	}
	return terminalEvent{}, false
}

// driveTerminal reconstructs the runner's `RunArgs` + `acquisition` shape
// from the AsyncContext and the CallbackServer's startup-time deps, then
// dispatches to the same applyTerminal* family the synchronous path runs
// in `runner_terminal.go`. Keeps the per-lock release tx, §5.6.4
// resolution, state→fresh / stale / failed transitions, dispatch
// re-enqueue, and event audit trail in one place.
func (c *CallbackServer) driveTerminal(ctx context.Context, ac AsyncContext, t terminalEvent) error {
	args := RunArgs{
		Persist:        c.Persist,
		Queue:          c.Queue,
		AdvisoryLocker: c.AdvisoryLocker,
		LockHolders:    c.LockHolders,
		StoreRegistry:  ac.StoreRegistry,
		Clock:          c.Clock,
		Logger:         c.Logger,
		SupervisorID:   ac.SupervisorID,
		ResumeGrace:    c.ResumeGrace,
	}
	acq := &acquisition{
		DispatchID:     ac.DispatchID,
		NodeID:         ac.NodeID,
		InstanceID:     ac.InstanceID,
		NodeType:       ac.NodeType,
		Executor:       ac.Executor,
		FrameID:        ac.FrameID,
		Locks:          ac.AcquiredLocks,
		NodeDef:        ac.NodeDef,
		InstanceParams: nil,
	}
	return applyTerminal(ctx, args, acq, ac.ResolvedAttributes, ac.AttributesSchema, t)
}

// attributesAuth validates the §12.5 incremental-writeback callback's
// `Authorization` header. The token is the supervisor-issued
// `cancel_token` of the form `<supervisorID>:<dispatchID>`. Auth passes
// when:
//
//  1. the token's supervisor segment matches this CallbackServer's
//     SupervisorID (the only supervisor entitled to mint tokens);
//  2. the dispatch row is still claimed by this supervisor (i.e. the
//     running window is open);
//  3. the dispatch row's node_id matches the URL-supplied node_id.
//
// Token shape mirrors `runner_dispatch.go`'s `cancelToken` builder. Any
// shape, supervisor-mismatch, ownership-mismatch, or node-mismatch
// returns ErrUnauthorizedCallback so the handler maps to HTTP 401 (per
// `core/attributes/callback.go` semantics).
func (c *CallbackServer) attributesAuth(token string, nodeID shared.UUID) error {
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		return rimskyattrs.ErrUnauthorizedCallback
	}
	tokSupervisor, tokDispatch := parts[0], parts[1]
	if tokSupervisor == "" || tokDispatch == "" {
		return rimskyattrs.ErrUnauthorizedCallback
	}
	if c.SupervisorID != "" && tokSupervisor != c.SupervisorID {
		return rimskyattrs.ErrUnauthorizedCallback
	}
	dispatchID, err := uuid.Parse(tokDispatch)
	if err != nil {
		return rimskyattrs.ErrUnauthorizedCallback
	}
	// Single round-trip: dispatch must exist, be claimed by us, and
	// target the URL's node_id.
	gotNodeID, ownership, err := c.Queue.GetDispatchNode(context.Background(), dispatchID)
	if err != nil {
		return rimskyattrs.ErrUnauthorizedCallback
	}
	if ownership.Kind != "claimed_by" || ownership.SupervisorID != tokSupervisor {
		return rimskyattrs.ErrUnauthorizedCallback
	}
	if gotNodeID != nodeID {
		return rimskyattrs.ErrUnauthorizedCallback
	}
	return nil
}

// attributesStoreAdapter bridges the persistence.NodeAttributesStore to
// the local `attributes.NodeAttributesStore` (returns `*attributes.Row`)
// the callback handler depends on. The two row shapes carry the same
// fields; the adapter copies between them.
//
// The split exists because `core/attributes` cannot import
// `core/persistence` without a cycle.
type attributesStoreAdapter struct {
	inner persistence.NodeAttributesStore
}

func (a attributesStoreAdapter) Get(ctx context.Context, nodeID shared.UUID) (*rimskyattrs.Row, error) {
	row, err := a.inner.Get(ctx, nodeID, nil)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return &rimskyattrs.Row{
		NodeID:     row.NodeID,
		RunAttempt: row.RunAttempt,
		Data:       row.Data,
		UpdatedAt:  row.UpdatedAt,
	}, nil
}

func (a attributesStoreAdapter) Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any) error {
	return a.inner.Upsert(ctx, nodeID, runAttempt, data, nil)
}

func (a attributesStoreAdapter) MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any) error {
	return a.inner.MergeDelta(ctx, nodeID, delta, nil)
}
