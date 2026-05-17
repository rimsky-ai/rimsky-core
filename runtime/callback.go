// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Spec §12.4 — async-handoff terminal callback. Executors that
// returned AwaitAsyncCallback POST an AsyncCallbackBody JSON body to
// `POST {callback_url}/v1/callback/{async_ack_id}`. The CallbackRegistry
// maps the ack id back to the per-run AsyncContext the runner registered
// at handoff time; this file's HTTP handler classifies the body, builds
// a `terminalEvent`, and drives the same `applyTerminal*` flow that the
// synchronous executor-RPC path runs in `runner_terminal.go`.
//
// Body shape mirrors the gRPC StreamClose outcome oneof (spec §12.3 —
// HTTP+JSON bridge): the body carries exactly one of
// `success` / `error` / `park`, plus an optional `events` array of
// NamedEvent records replayed before the outcome verdict. The legacy
// `{type: "complete"|"blocked"|"errored"}` discriminator is rejected
// with HTTP 400. The chi route param is `{async_ack_id}` (spec §12.4);
// the internal handler variable is named `ackID` for brevity.
//
// The dispatch row's frame_id is preserved across async handoff; the
// callback resolution path commits cascade message-passes that inherit
// the parent's frame_id (see foundation/integration/runner_terminal.go and
// docs/history/2026-04-26-frame-resolution-design.md §9).
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	rimskyattrs "github.com/fallguy/rimsky/graph/attribute"
)

// CallbackRegistry tracks pending async executions. Runners register an
// AsyncContext (defined in runner.go) when an executor returns
// AwaitAsyncCallback; the HTTP endpoint resolves ackID to the context on callback.
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
// Persist, Queue, AdvisoryLocker, ClaimHandles, and ResumeGrace are required
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
	Persist        persistence.Tables
	Queue          persistence.Queue
	AdvisoryLocker persistence.AdvisoryLocker
	ClaimHandles   persistence.ClaimHandleTable
	Clock          shared.Clock
	Logger         shared.Logger
	SupervisorID   string
	// ResumeGrace is forwarded as `RunArgs.ResumeGrace` when driving the
	// terminal flow. Zero falls back to the runner's 30-minute default
	// (see `releaseLocksInTx`).
	ResumeGrace time.Duration
	// Blob, BlobSpillThreshold, InvalidateHandler, and
	// MaxRetriesWithoutProgressDefault are threaded into RunArgs at
	// driveTerminal time so the async-callback path takes the same
	// spill / unified-invalidate / retry-cap behaviors that the sync
	// path takes. Without these wired, applyTerminalPark cannot spill
	// (large parked payloads end up inline), processNamedEvents cannot
	// spill, and on_event handler invalidates fall through to bare
	// InvalidateNode and cannot wake parked targets via the H2 unified
	// path. Zero values mean "use the runtime defaults" (no spill, no
	// unified invalidate handler, built-in 100-retry cap).
	Blob                             persistence.BlobBackend
	BlobSpillThreshold               int
	InvalidateHandler                func(ctx context.Context, args InvalidateArgs) error
	MaxRetriesWithoutProgressDefault int
	// UserdataValidator is the dispatch-time userdata schema validator
	// (plan F7), threaded into the async-callback RunArgs so an
	// async-callback-driven re-dispatch (resume after park, retry) hits
	// the same validator the synchronous path runs. Nil → skipped.
	UserdataValidator func(executorName string, merged map[string]any) error
	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook (plan I1/I2/I3). Threaded into RunArgs at driveTerminal time
	// so async-callback-driven terminals contribute to the same
	// `rimsky_terminal_verdicts_total` / `rimsky_invalidates_total`
	// counters as the sync path. Optional; nil → no-op everywhere.
	Metrics MetricsHook
	addr    string
	srv     *http.Server
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
			Store:  attributesStoreAdapter{store: c.Persist},
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

// asyncCallbackBody mirrors the AsyncCallbackBody shape from
// protocols/proto/v1/executor.proto (post 2026-05-12 nomenclature
// resolution). Events processed in arrival order before the outcome
// verdict is applied.
//
// Exactly one of success | error | park MUST be set; the legacy
// `{type: ...}` discriminator shape is no longer accepted (pre-v1; no
// consumer pin).
type asyncCallbackBody struct {
	Events  []asyncCallbackNamedEvent `json:"events,omitempty"`
	Success *asyncCallbackSuccess     `json:"success,omitempty"`
	Error   *asyncCallbackError       `json:"error,omitempty"`
	Park    *asyncCallbackPark        `json:"park,omitempty"`
}

// asyncCallbackNamedEvent mirrors the proto NamedEvent message.
//
// Payload is base64-encoded on the wire (proto-JSON rule for `bytes`).
type asyncCallbackNamedEvent struct {
	Name    string `json:"name"`
	Payload []byte `json:"payload,omitempty"`
}

type asyncCallbackSuccess struct {
	Changed         bool           `json:"changed,omitempty"`
	ChangeSummary   string         `json:"change_summary,omitempty"`
	AttributesDelta map[string]any `json:"attributes_delta,omitempty"`
}

type asyncCallbackError struct {
	ErrorClass string `json:"error_class,omitempty"`
	Payload    any    `json:"payload,omitempty"`
}

type asyncCallbackPark struct {
	// Reason is the snake_case ParkReason form (matching the proto
	// enum's lower_snake_case projection; see parkReasonStorageForm).
	Reason       string `json:"reason,omitempty"`
	ReasonNote   string `json:"reason_note,omitempty"`
	ReasonLabel  string `json:"reason_label,omitempty"` // required when Reason == "other" per spec E12
	Payload      []byte `json:"payload,omitempty"`
	ResumeAt     string `json:"resume_at,omitempty"` // RFC3339; empty = absent
	SessionToken string `json:"session_token,omitempty"`
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
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	// Parse the AsyncCallbackBody shape. The legacy `{type: ...}`
	// discriminator shape is no longer accepted (pre-v1; no consumer
	// pin). Errors surface as HTTP 400 with a precise message.
	t, namedEvents, parseErr := parseAsyncCallback(bodyBytes)
	if parseErr != nil {
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"`+parseErr.Error()+`"}`, http.StatusBadRequest)
		return
	}
	t.NamedEvents = namedEvents

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
	// synchronous-runner path in supervisor.go that calls
	// Queue.Complete after a non-async run.
	_ = c.Queue.Complete(r.Context(), asyncCtx.DispatchID, asyncCtx.SupervisorID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}

// parseAsyncCallback parses the AsyncCallbackBody shape. Exactly one
// outcome oneof variant must be set; the legacy `{type: ...}`
// discriminator shape is no longer accepted (pre-v1; no consumer pin).
//
// Returns:
//
//	terminalEvent — populated on success.
//	[]namedEventRecord — events from `events: [...]`.
//	error — non-nil on malformed body. The caller surfaces this as HTTP 400.
func parseAsyncCallback(raw []byte) (terminalEvent, []namedEventRecord, error) {
	var body asyncCallbackBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return terminalEvent{}, nil, errors.New("invalid json")
	}
	outcomeCount := 0
	if body.Success != nil {
		outcomeCount++
	}
	if body.Error != nil {
		outcomeCount++
	}
	if body.Park != nil {
		outcomeCount++
	}
	if outcomeCount != 1 {
		return terminalEvent{}, nil, fmt.Errorf("expected AsyncCallbackBody; outcome oneof must be set (success | error | park); got %d outcomes", outcomeCount)
	}
	events := make([]namedEventRecord, 0, len(body.Events))
	for _, e := range body.Events {
		events = append(events, namedEventRecord{
			Name:          e.Name,
			PayloadInline: e.Payload,
		})
	}
	switch {
	case body.Success != nil:
		return terminalEvent{
			Kind:          terminalKindComplete,
			Changed:       body.Success.Changed,
			ChangeSummary: body.Success.ChangeSummary,
			AttributesDel: body.Success.AttributesDelta,
		}, events, nil
	case body.Error != nil:
		return terminalEvent{
			Kind:       terminalKindErrored,
			ErrorClass: body.Error.ErrorClass,
			Payload:    map[string]any{"payload": body.Error.Payload},
		}, events, nil
	case body.Park != nil:
		t := terminalEvent{
			Kind:             terminalKindPark,
			ParkReason:       parkReasonFromStorageForm(body.Park.Reason),
			ParkReasonNote:   body.Park.ReasonNote,
			ParkReasonLabel:  body.Park.ReasonLabel,
			ParkPayload:      body.Park.Payload,
			ParkSessionToken: body.Park.SessionToken,
		}
		if body.Park.ResumeAt != "" {
			if pt, err := time.Parse(time.RFC3339, body.Park.ResumeAt); err == nil {
				t.ParkResumeAt = pt
			}
		}
		return t, events, nil
	}
	// Unreachable — outcomeCount==1 enforced above.
	return terminalEvent{}, nil, errors.New("unreachable")
}

// driveTerminal reconstructs the runner's `RunArgs` + `acquisition` shape
// from the AsyncContext and the CallbackServer's startup-time deps, then
// dispatches to the same applyTerminal* family the synchronous path runs
// in `runner_terminal.go`. Keeps the per-lock release tx, §5.6.4
// resolution, state→fresh / stale / failed transitions, dispatch
// re-enqueue, and event audit trail in one place.
func (c *CallbackServer) driveTerminal(ctx context.Context, ac AsyncContext, t terminalEvent) error {
	args := RunArgs{
		Persist:                          c.Persist,
		Queue:                            c.Queue,
		AdvisoryLocker:                   c.AdvisoryLocker,
		ClaimHandles:                     c.ClaimHandles,
		StoreRegistry:                    ac.StoreRegistry,
		Clock:                            c.Clock,
		Logger:                           c.Logger,
		SupervisorID:                     ac.SupervisorID,
		ResumeGrace:                      c.ResumeGrace,
		Blob:                             c.Blob,
		BlobSpillThreshold:               c.BlobSpillThreshold,
		InvalidateHandler:                c.InvalidateHandler,
		MaxRetriesWithoutProgressDefault: c.MaxRetriesWithoutProgressDefault,
		UserdataValidator:                c.UserdataValidator,
		Metrics:                          c.Metrics,
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
// `graph/attribute/callback.go` semantics).
func (c *CallbackServer) attributesAuth(token string, nodeID shared.UUID) error {
	// `c.Logger` is defaulted to SilentLogger{} in Start() before this
	// handler is mounted, so it is never nil here — same convention as
	// `handleCallback` which calls `c.Logger.Warn` directly.
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		// Raw token bytes are user-supplied and may be arbitrarily long
		// or non-printable. Log the length only; the failure mode (no
		// ':' separator) is self-explanatory.
		c.Logger.Warn("attributesAuth: token has no ':' separator",
			"node_id", nodeID.String(),
			"token_len", len(token))
		return rimskyattrs.ErrUnauthorizedCallback
	}
	tokSupervisor, tokDispatch := parts[0], parts[1]
	if tokSupervisor == "" || tokDispatch == "" {
		c.Logger.Warn("attributesAuth: empty supervisor or dispatch segment",
			"node_id", nodeID.String(),
			"token_supervisor_len", len(tokSupervisor),
			"token_dispatch_len", len(tokDispatch))
		return rimskyattrs.ErrUnauthorizedCallback
	}
	if c.SupervisorID != "" && tokSupervisor != c.SupervisorID {
		// Supervisor-mismatch is the most useful branch for diagnostics;
		// log a bounded prefix of the token's supervisor segment so a
		// misconfigured caller is identifiable without flooding logs
		// with arbitrary-length user-supplied bytes.
		c.Logger.Warn("attributesAuth: supervisor id mismatch",
			"node_id", nodeID.String(),
			"token_supervisor", truncForLog(tokSupervisor, 64),
			"token_supervisor_len", len(tokSupervisor),
			"server_supervisor", c.SupervisorID)
		return rimskyattrs.ErrUnauthorizedCallback
	}
	dispatchID, err := uuid.Parse(tokDispatch)
	if err != nil {
		// Parse failure mode is self-explanatory; log only the length
		// of the dispatch segment, not its raw bytes.
		c.Logger.Warn("attributesAuth: dispatch id parse failed",
			"node_id", nodeID.String(),
			"token_dispatch_len", len(tokDispatch),
			"error", err.Error())
		return rimskyattrs.ErrUnauthorizedCallback
	}
	// Single round-trip: dispatch must exist, be claimed by us, and
	// target the URL's node_id.
	gotNodeID, ownership, err := c.Queue.GetDispatchNode(context.Background(), dispatchID)
	if err != nil {
		c.Logger.Warn("attributesAuth: GetDispatchNode failed",
			"node_id", nodeID.String(),
			"dispatch_id", dispatchID.String(),
			"error", err.Error())
		return rimskyattrs.ErrUnauthorizedCallback
	}
	if ownership.Kind != "claimed_by" || ownership.SupervisorID != tokSupervisor {
		c.Logger.Warn("attributesAuth: ownership mismatch",
			"node_id", nodeID.String(),
			"dispatch_id", dispatchID.String(),
			"ownership_kind", ownership.Kind,
			"ownership_supervisor", ownership.SupervisorID,
			"token_supervisor", truncForLog(tokSupervisor, 64))
		return rimskyattrs.ErrUnauthorizedCallback
	}
	if gotNodeID != nodeID {
		c.Logger.Warn("attributesAuth: node id mismatch",
			"url_node_id", nodeID.String(),
			"dispatch_node_id", gotNodeID.String(),
			"dispatch_id", dispatchID.String())
		return rimskyattrs.ErrUnauthorizedCallback
	}
	return nil
}

// truncForLog returns s capped to max bytes (with a trailing ellipsis when
// truncation occurred) so user-supplied token bytes don't bloat logs.
func truncForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// attributesStoreAdapter bridges the persistence.NodeAttributeTable to
// the local `attributes.NodeAttributeTable` (returns `*attributes.Row`)
// the callback handler depends on. The two row shapes carry the same
// fields; the adapter copies between them.
//
// The split exists because `graph/attribute` cannot import
// `foundation/persistence` without a cycle.
type attributesStoreAdapter struct {
	store persistence.Tables
}

func (a attributesStoreAdapter) Get(ctx context.Context, nodeID shared.UUID) (*rimskyattrs.Row, error) {
	var row *persistence.NodeAttributesRow
	if err := a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := a.store.NodeAttributes().Get(ctx, nodeID, tx)
		row = r
		return err
	}); err != nil {
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
	return a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return a.store.NodeAttributes().Upsert(ctx, nodeID, runAttempt, data, tx)
	})
}

func (a attributesStoreAdapter) MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any) error {
	return a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return a.store.NodeAttributes().MergeDelta(ctx, nodeID, delta, tx)
	})
}
