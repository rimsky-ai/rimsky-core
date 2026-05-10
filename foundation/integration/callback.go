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
// the parent's frame_id (see foundation/integration/runner_terminal.go and
// docs/history/2026-04-26-frame-resolution-design.md §9).
package integration

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
	Persist        persistence.Store
	Queue          persistence.Queue
	AdvisoryLocker persistence.AdvisoryLocker
	ClaimHandles   persistence.ClaimHandlesStore
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

// asyncCallbackBody mirrors the new AsyncCallbackBody shape from
// protocols/proto/v1/executor.proto (plan A5). Events processed in
// arrival order before the terminal verdict is applied.
//
// The supervisor's parser tries this shape first; on parse failure it
// falls back to the legacy {type: "complete"|"blocked"|"errored", ...}
// shape (callbackBody above). Both shapes remain accepted indefinitely.
type asyncCallbackBody struct {
	Events []asyncCallbackNamedEvent `json:"events,omitempty"`
	// Exactly one of complete | blocked | errored | park_requested
	// MUST be set when the new shape is used.
	Complete      *asyncCallbackComplete `json:"complete,omitempty"`
	Blocked       *asyncCallbackBlocked  `json:"blocked,omitempty"`
	Errored       *asyncCallbackErrored  `json:"errored,omitempty"`
	ParkRequested *asyncCallbackPark     `json:"park_requested,omitempty"`
}

// asyncCallbackNamedEvent mirrors the proto NamedEvent message.
//
// Payload is base64-encoded on the wire (proto-JSON rule for `bytes`).
type asyncCallbackNamedEvent struct {
	Name    string `json:"name"`
	Payload []byte `json:"payload,omitempty"`
}

type asyncCallbackComplete struct {
	Changed         bool           `json:"changed,omitempty"`
	ChangeSummary   string         `json:"change_summary,omitempty"`
	AttributesDelta map[string]any `json:"attributes_delta,omitempty"`
}

type asyncCallbackBlocked struct {
	Reason  string `json:"reason,omitempty"`
	Context any    `json:"context,omitempty"`
}

type asyncCallbackErrored struct {
	ErrorClass string `json:"error_class,omitempty"`
	Payload    any    `json:"payload,omitempty"`
}

type asyncCallbackPark struct {
	Reason       string `json:"reason,omitempty"`
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
	// Try the new AsyncCallbackBody shape first (plan A5/H1). Fall back
	// to the legacy callbackBody shape on indeterminate shape (no terminal
	// fields and no `type`). Both shapes remain accepted indefinitely.
	//
	// Parser surfaces two distinct error shapes:
	//   - "invalid json": body is not parseable as JSON at all.
	//   - "async callback body must include exactly one terminal field":
	//     the new-shape body has zero terminals or more than one.
	t, namedEvents, parseErr, ok := tryParseAsyncCallback(bodyBytes)
	if parseErr != nil {
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"`+parseErr.Error()+`"}`, http.StatusBadRequest)
		return
	}
	if !ok {
		var legacy callbackBody
		if err := json.Unmarshal(bodyBytes, &legacy); err != nil {
			c.Registry.Register(ackID, asyncCtx)
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		var legacyOK bool
		t, legacyOK = classifyCallbackBody(legacy)
		if !legacyOK {
			c.Registry.Register(ackID, asyncCtx)
			http.Error(w, `{"error":"async callback body must include exactly one terminal field (complete | blocked | errored | park_requested) or a legacy type discriminator"}`, http.StatusBadRequest)
			return
		}
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
	// synchronous-runner path in supervisor.go that calls Queue.Complete
	// after a non-async run.
	_ = c.Queue.Complete(r.Context(), asyncCtx.DispatchID, asyncCtx.SupervisorID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}

// tryParseAsyncCallback attempts to parse the new AsyncCallbackBody
// shape (plan A5).
//
// Return shape:
//
//	terminalEvent — populated on the success path.
//	[]namedEventRecord — events from `events: [...]`.
//	error — non-nil for an *explicit* validation failure on a body that
//	  parsed as the new shape but is malformed (e.g. zero or >1 terminal
//	  fields, or events present without a terminal). The caller surfaces
//	  this as HTTP 400 directly rather than falling back to the legacy
//	  parser.
//	bool — ok=true on success; ok=false signals the caller should attempt
//	  the legacy callbackBody parser (used for "indeterminate" bodies that
//	  might be legacy-shape).
//
// "Indeterminate" = JSON parses fine, but neither a terminal field nor an
// events array is present. Legacy bodies look like that (they carry a
// `type` discriminator) so we hand off to the legacy parser.
func tryParseAsyncCallback(raw []byte) (terminalEvent, []namedEventRecord, error, bool) {
	var body asyncCallbackBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return terminalEvent{}, nil, nil, false
	}
	// Count terminal fields set. Exactly one must be set in the new shape.
	terminalCount := 0
	if body.Complete != nil {
		terminalCount++
	}
	if body.Blocked != nil {
		terminalCount++
	}
	if body.Errored != nil {
		terminalCount++
	}
	if body.ParkRequested != nil {
		terminalCount++
	}
	if terminalCount > 1 {
		// Two or more terminals → reject explicitly. The legacy parser
		// can't represent this so falling back would silently drop one.
		return terminalEvent{}, nil, fmt.Errorf("async callback body must include exactly one terminal field; got %d", terminalCount), false
	}
	if terminalCount == 0 {
		// No terminal fields. If `events` is present, the body intends the
		// new shape but is missing its terminal — surface a clear error.
		// Without `events` the body could be legacy → fall through to
		// indeterminate so the caller tries the legacy parser.
		if len(body.Events) > 0 {
			return terminalEvent{}, nil, errors.New("async callback body must include exactly one terminal field (complete | blocked | errored | park_requested) when events are present"), false
		}
		return terminalEvent{}, nil, nil, false
	}
	events := make([]namedEventRecord, 0, len(body.Events))
	for _, e := range body.Events {
		events = append(events, namedEventRecord{
			Name:          e.Name,
			PayloadInline: e.Payload,
		})
	}
	switch {
	case body.Complete != nil:
		return terminalEvent{
			Kind:          terminalKindComplete,
			Changed:       body.Complete.Changed,
			ChangeSummary: body.Complete.ChangeSummary,
			AttributesDel: body.Complete.AttributesDelta,
		}, events, nil, true
	case body.Blocked != nil:
		return terminalEvent{
			Kind:       terminalKindBlocked,
			ErrorClass: "executor_blocked",
			Payload: map[string]any{
				"reason":  body.Blocked.Reason,
				"context": body.Blocked.Context,
			},
		}, events, nil, true
	case body.Errored != nil:
		return terminalEvent{
			Kind:       terminalKindErrored,
			ErrorClass: body.Errored.ErrorClass,
			Payload:    map[string]any{"payload": body.Errored.Payload},
		}, events, nil, true
	case body.ParkRequested != nil:
		t := terminalEvent{
			Kind:             terminalKindPark,
			ParkReason:       body.ParkRequested.Reason,
			ParkPayload:      body.ParkRequested.Payload,
			ParkSessionToken: body.ParkRequested.SessionToken,
		}
		if body.ParkRequested.ResumeAt != "" {
			if pt, err := time.Parse(time.RFC3339, body.ParkRequested.ResumeAt); err == nil {
				t.ParkResumeAt = pt
			}
		}
		return t, events, nil, true
	}
	return terminalEvent{}, nil, nil, false
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
// `modeling/attribute/callback.go` semantics).
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

// attributesStoreAdapter bridges the persistence.NodeAttributesStore to
// the local `attributes.NodeAttributesStore` (returns `*attributes.Row`)
// the callback handler depends on. The two row shapes carry the same
// fields; the adapter copies between them.
//
// The split exists because `modeling/attribute` cannot import
// `foundation/persistence` without a cycle.
type attributesStoreAdapter struct {
	store persistence.Store
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
