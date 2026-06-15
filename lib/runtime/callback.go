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
// the parent's frame_id (see runtime/runner_terminal.go and
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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	rimskyattrs "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// callbackAckBody is the structured response the supervisor writes for
// every callback per spec §"HTTP callback ack body: structured response".
// HTTP status stays 200 for both accepted and rejected — ack_status is
// the authoritative signal. CurrentDispatchID is set on rejection when
// the supervisor can compute the canonical successor via the run row's
// RunScope (an in-flight run for the same node in the same RunScope
// supersedes the rejected dispatch).
//
// @blessed-invariant: callback-determinism — Callback determinism.
type callbackAckBody struct {
	AckStatus         string  `json:"ack_status"`
	CurrentDispatchID *string `json:"current_dispatch_id,omitempty"`
}

// @constraint: ack_status enum values for callbackAckBody. Per spec
// §"HTTP callback ack body: structured response": closed enum, no
// UNSPECIFIED.
const (
	ackStatusAccepted            = "accepted"
	ackStatusRejectedRunTerminal = "rejected_run_terminal"
	ackStatusRejectedRunStale    = "rejected_run_stale"
	ackStatusRejectedRunParked   = "rejected_run_parked"
	ackStatusRejectedUnknown     = "rejected_unknown"
)

// ackOutcomeRecord is the per-dispatch ack state captured in
// driveTerminal's phase-check tx and surfaced to handleCallback for the
// HTTP response body. The phase string (for rejected outcomes) drives
// the optional `current_dispatch_id` lookup at response time.
type ackOutcomeRecord struct {
	Status string
	Phase  string
}

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
	// ExpectedAttributesSchemaFor is the dispatch-time hook that returns
	// the named executor's advertised expected_attributes_schema bytes,
	// threaded into the async-callback RunArgs so an async-callback-
	// driven re-dispatch (resume after park, retry) computes the same
	// effective attribute schema the synchronous path computes. Nil →
	// no executor schema in the merge.
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook (plan I1/I2/I3). Threaded into RunArgs at driveTerminal time
	// so async-callback-driven terminals contribute to the same
	// `rimsky_terminal_verdicts_total` / `rimsky_invalidates_total`
	// counters as the sync path. Optional; nil → no-op everywhere.
	Metrics MetricsHook
	// LifecycleSubs and LifecyclePeersForSpec are threaded into RunArgs at
	// driveTerminal time so an async-callback-driven sub-graph / fanout-
	// partition terminal fires OnRunScopeTerminal at the close site, same
	// as the synchronous RunNode path. Nil → run-scope fan-out is a no-op.
	//
	// Per spec 2026-05-24-host-agent-and-proxy-design.md.
	LifecycleSubs         *locks.LifecycleRegistry
	LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string
	// DataProcessors is threaded into RunArgs at driveTerminal time so an
	// async-callback-driven leaf terminal can Commit/Abandon the
	// per-sub-claim candidate against the producer's DataProcessing
	// surface, same as the synchronous RunNode path. Nil → candidate
	// Commit/Abandon is a no-op (degrades to the pre-DataProcessing
	// posture). @concept: data-processing
	DataProcessors DataProcessingRegistry
	addr           string
	srv            *http.Server
	// serveErr surfaces a fatal post-start death of the callback HTTP
	// serve loop (anything other than a graceful Close). At most one
	// error is ever sent; the channel is closed when the serve loop
	// exits, so a clean shutdown delivers a close with no error. The
	// supervisor's launch wiring forwards this onto its role fail
	// channel — a supervisor whose callback listener has died must
	// restart, not run degraded with async callbacks black-holed.
	serveErr chan error
	// @constraint: ackOutcomes records the per-dispatch ack status
	// produced by driveTerminal's phase-check tx so handleCallback can
	// write the structured response body. Keyed by dispatch_id; entries
	// are removed by handleCallback once consumed (single-shot per
	// callback). Guarded by ackMu.
	ackMu       sync.Mutex
	ackOutcomes map[shared.UUID]ackOutcomeRecord
}

// recordAckOutcome stores the per-dispatch ack status produced inside
// driveTerminal's phase-check tx. handleCallback consumes it after
// driveTerminal returns to write the structured ack body.
func (c *CallbackServer) recordAckOutcome(dispatchID shared.UUID, status, phase string, _ bool) {
	c.ackMu.Lock()
	defer c.ackMu.Unlock()
	if c.ackOutcomes == nil {
		c.ackOutcomes = make(map[shared.UUID]ackOutcomeRecord)
	}
	c.ackOutcomes[dispatchID] = ackOutcomeRecord{Status: status, Phase: phase}
}

// consumeAckOutcome returns and removes the recorded ack outcome for
// dispatchID, or a default (accepted, no current_dispatch_id) when no
// record exists. The fallback handles paths that bypass the phase-check
// tx (defensive; should not occur in steady state).
func (c *CallbackServer) consumeAckOutcome(dispatchID shared.UUID) ackOutcomeRecord {
	c.ackMu.Lock()
	defer c.ackMu.Unlock()
	if c.ackOutcomes == nil {
		return ackOutcomeRecord{Status: ackStatusAccepted}
	}
	rec, ok := c.ackOutcomes[dispatchID]
	if !ok {
		return ackOutcomeRecord{Status: ackStatusAccepted}
	}
	delete(c.ackOutcomes, dispatchID)
	return rec
}

// Start listens on host:port (port=0 for OS-assigned). Safe to call before
// any callbacks are registered. Returns the bound address.
func (c *CallbackServer) Start(host string, port int) (string, error) {
	if c.Logger == nil {
		c.Logger = shared.SilentLogger{}
	}
	r := chi.NewRouter()
	// @constraint: spec §12.4 — the chi-route param is `{async_ack_id}`.
	// The internal handler reads it as `ackID` for brevity.
	r.Post("/v1/callback/{async_ack_id}", c.handleCallback)
	// @constraint: spec §12.5 — incremental attributes writeback.
	// Mounted on the same listener as the async terminal callback so
	// executors can reach both at the supervisor's advertised callback
	// URL.
	if c.Persist != nil {
		r.Method(http.MethodPost, "/v1/runs/{run_id}/attributes", rimskyattrs.Handler(rimskyattrs.HandlerDeps{
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
	c.serveErr = make(chan error, 1)
	go func() {
		// @constraint: a serve-loop death other than the graceful-Close
		// http.ErrServerClosed is a fatal post-start failure: report it
		// on serveErr so the supervising process can exit instead of
		// running degraded. The buffered send precedes the close in the
		// same goroutine, so consumers see the error (if any) and then
		// the close — no send-after-close race.
		err := c.srv.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.serveErr <- err
		}
		close(c.serveErr)
	}()
	return c.addr, nil
}

// Addr returns the bound address of the running server (empty before Start).
func (c *CallbackServer) Addr() string { return c.addr }

// ServeErr returns the channel surfacing a fatal post-start death of the
// callback serve loop. At most one error is sent; the channel closes when
// the serve loop exits (clean shutdown closes with no error). Nil before
// Start.
func (c *CallbackServer) ServeErr() <-chan error { return c.serveErr }

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
	Reason     string `json:"reason,omitempty"`
	ReasonNote string `json:"reason_note,omitempty"`
	// @constraint: ReasonLabel required when Reason == "other" per spec E12.
	ReasonLabel string `json:"reason_label,omitempty"`
	Payload     []byte `json:"payload,omitempty"`
	// @constraint: ResumeAt is RFC3339; empty = absent.
	ResumeAt     string `json:"resume_at,omitempty"`
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
	// @constraint: parse the AsyncCallbackBody shape; the legacy
	// `{type: ...}` discriminator shape is no longer accepted (pre-v1;
	// no consumer pin). Errors surface as HTTP 400 with a precise
	// message.
	t, namedEvents, parseErr := parseAsyncCallback(bodyBytes)
	if parseErr != nil {
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"`+parseErr.Error()+`"}`, http.StatusBadRequest)
		return
	}
	t.NamedEvents = namedEvents

	if err := c.driveTerminal(r.Context(), asyncCtx, t); err != nil {
		// @constraint: re-register so the executor can retry. If we
		// didn't, a transient failure would leave the node stuck in
		// `running` forever — the callback would never correlate on
		// retry.
		c.Registry.Register(ackID, asyncCtx)
		c.Logger.Warn("callback: driveTerminal failed",
			"node_id", asyncCtx.NodeID.String(), "error", err.Error())
		// @constraint: driveTerminal records the ack outcome via
		// recordAckOutcome BEFORE applyTerminal runs (so the phase-check
		// tx's verdict is captured even if applyTerminal later fails).
		// On the error path we won't ship a structured ack body — but
		// we MUST consume here to evict the map entry, otherwise it
		// leaks for the lifetime of the CallbackServer when the
		// executor never retries (or retries to a different ack id).
		_ = c.consumeAckOutcome(asyncCtx.DispatchID)
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	// @constraint: read the ack outcome the phase-check tx recorded in
	// driveTerminal. Per spec §"HTTP callback ack body: structured
	// response": HTTP 200 for both accepted and rejected; ack_status
	// discriminates, and current_dispatch_id (optional) surfaces the
	// canonical successor.
	outcome := c.consumeAckOutcome(asyncCtx.DispatchID)
	body := callbackAckBody{AckStatus: outcome.Status}
	if outcome.Status != ackStatusAccepted && outcome.Status != ackStatusRejectedUnknown {
		// @constraint: look up the canonical successor by walking from
		// the rejected run's RunScope to the current in-flight run for
		// the same node in the same RunScope. The node + scope id are
		// fetched once; failures are tolerated (current_dispatch_id
		// stays unset).
		if successor := c.findCanonicalSuccessor(r.Context(), asyncCtx); successor != nil {
			s := successor.String()
			body.CurrentDispatchID = &s
		}
	}
	// @constraint: cleanup runs only for accepted callbacks. Rejected
	// callbacks left no state mutation; there is no dispatch row to
	// Complete (or the row belongs to a successor dispatch that the
	// rejecting callback must not touch).
	//
	// @deliberate: post-2026-05-21 lifecycle reorder, every apply*
	// terminal function flips the dispatch row to a terminal phase
	// inside its own tx (RemoveForNodeInTx in
	// Complete/Pass/Errored/InfraError; ParkActiveInTx in Park). This
	// Queue.Complete call is therefore a WHERE-clause-guarded no-op on
	// every known happy path; it survives as belt-and-suspenders
	// cleanup against any future terminal path that forgets to flip
	// in-tx.
	if outcome.Status == ackStatusAccepted {
		if err := c.Queue.Complete(r.Context(), asyncCtx.DispatchID, asyncCtx.SupervisorID); err != nil {
			c.Logger.Error("callback: queue.Complete failed after applied terminal",
				"node_id", asyncCtx.NodeID.String(),
				"dispatch_id", asyncCtx.DispatchID.String(),
				"supervisor_id", asyncCtx.SupervisorID,
				"error", err.Error())
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		c.Logger.Warn("callback: encode ack body failed",
			"dispatch_id", asyncCtx.DispatchID.String(),
			"error", err.Error())
	}
}

// findCanonicalSuccessor walks from the rejected dispatch's run row to
// its RunScope and returns the in-flight run id for the same node in the
// same RunScope, when one exists. The result is the canonical successor
// the executor should target on retry / handoff.
//
// Failures (no run row, no in-flight successor, DB error) return nil
// without surfacing — `current_dispatch_id` stays unset on the ack body
// and the executor falls back to no-handoff semantics. The supervisor's
// log of the original rejection (callback.late_or_stale_run) is the
// authoritative diagnostic trail; this is a best-effort enrichment.
func (c *CallbackServer) findCanonicalSuccessor(ctx context.Context, ac AsyncContext) *shared.UUID {
	var successor *shared.UUID
	_ = c.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := c.Persist.Nodes().GetRunByDispatchIDForUpdate(ctx, ac.DispatchID, tx)
		if err != nil || row == nil {
			return nil
		}
		nextID, ok, err := c.Queue.GetInFlightRunForNode(ctx, tx, row.NodeID, row.RunScopeID)
		if err != nil || !ok {
			return nil
		}
		// @constraint: a successor must be a row DISTINCT from the
		// rejected one; same-id means the queue has not yet advanced
		// past the rejected dispatch.
		if nextID == ac.DispatchID {
			return nil
		}
		successor = &nextID
		return nil
	})
	return successor
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
	// @deliberate: unreachable — outcomeCount==1 enforced above.
	return terminalEvent{}, nil, errors.New("unreachable")
}

// driveTerminal reconstructs the runner's `RunArgs` + `acquisition` shape
// from the AsyncContext and the CallbackServer's startup-time deps, then
// dispatches to the same applyTerminal* family the synchronous path runs
// in `runner_terminal.go`. Keeps the per-lock release tx, §5.6.4
// resolution, state→fresh / stale / failed transitions, dispatch
// re-enqueue, and event audit trail in one place.
//
// @blessed-invariant: callback-honored-iff — A callback for a run is honored if and only if
// the run's phase ∈ {active, held} at acceptance, checked atomically
// inside the same tx as the state mutation. Otherwise: HTTP 200
// ack-but-noop with a structured log event. Per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Callback determinism". The invariant is structurally enforced:
// the phase-check FOR-UPDATE read and applyTerminal's state-mutation
// writes share one tx via runApplyTerminal's outer Transaction block.
// Refactor history: pre-fan-out-safety the phase-check tx committed
// before applyTerminal opened its own state-mutation tx — the TOCTOU
// window between commits closed when applyTerminal was rewritten to
// accept the outer tx.
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
		ExpectedAttributesSchemaFor:      c.ExpectedAttributesSchemaFor,
		Metrics:                          c.Metrics,
		LifecycleSubs:                    c.LifecycleSubs,
		LifecyclePeersForSpec:            c.LifecyclePeersForSpec,
		DataProcessors:                   c.DataProcessors,
	}
	acq := &acquisition{
		DispatchID: ac.DispatchID,
		NodeID:     ac.NodeID,
		InstanceID: ac.InstanceID,
		NodeType:   ac.NodeType,
		Executor:   ac.Executor,
		// @constraint: resume-callback path doesn't run applyAttributeOverrides.
		GraphName: "",
		// @constraint: partition key (formerly inline `ChildKey`) is
		// derived from the RunScope on demand via `resolveAcqScope`;
		// the resume path leaves it implicit. The phase-check tx below
		// also populates RunScopeID directly from the run row.
		FrameID:        ac.FrameID,
		Locks:          ac.AcquiredLocks,
		NodeDef:        ac.NodeDef,
		InstanceParams: nil,
	}
	// @constraint: callback determinism rule (per spec §"Callback
	// determinism") — open a single tx that (1) SELECTs the run FOR
	// UPDATE and checks phase ∈ {active, held}, then (2) runs
	// applyTerminal's state mutation in the same tx. ack-but-noop on
	// rejection.
	//
	// @blessed-invariant: callback-determinism — Callback determinism — phase-check + state
	// mutation share one tx; structurally enforced here.
	// @concept: run-scope
	var ackStatus string
	var phase string
	rejected := false
	setup := func(ctx context.Context, tx persistence.Tx) (bool, error) {
		row, err := c.Persist.Nodes().GetRunByDispatchIDForUpdate(ctx, ac.DispatchID, tx)
		if err != nil {
			return false, fmt.Errorf("driveTerminal: GetRunByDispatchIDForUpdate: %w", err)
		}
		if row == nil {
			rejected = true
			ackStatus = ackStatusRejectedUnknown
			c.Logger.Warn("callback.late_or_stale_run",
				"dispatch_id", ac.DispatchID.String(),
				"reason", "run_not_found")
			return true, nil
		}
		if row.Phase != "active" && row.Phase != "held" {
			rejected = true
			phase = row.Phase
			ackStatus = ackStatusForPhase(row.Phase)
			c.Logger.Warn("callback.late_or_stale_run",
				"dispatch_id", ac.DispatchID.String(),
				"current_phase", row.Phase,
				"expected_phase", "active|held")
			return true, nil
		}
		// @constraint: accepted path populates the acquisition's
		// RunScopeID directly from the run row (no separate
		// RunTree.GetByID needed under RunScope-first; the row carries
		// run_scope_id), and populates the instance-row-driven lineage
		// fields (template_hash, instance params) inside the same tx —
		// the instance row is immutable for the run's lifetime, so
		// reading it under the determinism tx is a single round-trip
		// rather than two.
		acq.RunScopeID = row.RunScopeID
		ackStatus = ackStatusAccepted
		if inst, err := c.Persist.Instances().Get(ctx, acq.InstanceID, tx); err == nil && inst != nil {
			acq.TemplateHash = inst.TemplateHash
			acq.InstanceParams = inst.Params
		} else if err != nil && c.Logger != nil {
			c.Logger.Warn("driveTerminal: instances.Get failed; lineage will omit template_hash and params",
				"node_id", acq.NodeID.String(),
				"instance_id", acq.InstanceID.String(),
				"error", err.Error())
		}
		return false, nil
	}
	if err := runApplyTerminal(ctx, args, acq, ac.ResolvedAttributes, ac.AttributesSchema, t, setup); err != nil {
		// @constraint: defer recording the ack outcome — handler
		// treats this as a retryable failure (re-registers the ack
		// id), so we must NOT leave a stale rejected entry in the
		// ack-outcome map.
		return err
	}

	// @constraint: breakpoint checkpoint after_terminal. Runs AFTER
	// runApplyTerminal returns (its tx is committed) and BEFORE the
	// callback handler records the ack outcome. EvaluateBreakpoints
	// opens its own short txns; pause-mode hits block on waitForResume
	// (per-iteration short txns; no tx held across the wait). Return
	// value is discarded — after-terminal overlays don't mutate further
	// dispatch because the dispatch is already complete. Notify-only
	// breakpoints just observe. Failures are best-effort: Warn-log and
	// continue so debugger problems don't fail the callback.
	// AsyncContext does NOT carry GraphName / scope.PartitionKey (the
	// callback path skips L5 attribute overrides per the comment at acq
	// construction), so the matcher's graph / child_key keys evaluate
	// against empty strings — callers writing breakpoints intended to
	// fire on async-callback terminals should leave those keys absent
	// (wildcard) per spec §4.4.
	scope := resolveAcqScope(ctx, args, acq)
	terminalSig := signalForTerminal(t)
	if _, err := EvaluateBreakpoints(ctx, args, CheckpointContext{
		InstanceID:       acq.InstanceID,
		NodeID:           acq.NodeID,
		DispatchID:       acq.DispatchID,
		FrameID:          acq.FrameID,
		Executor:         acq.Executor,
		NodeType:         acq.NodeType,
		Graph:            acq.GraphName,
		ChildKey:         scope.PartitionKey,
		MergedAttributes: ac.ResolvedAttributes,
		Checkpoint:       persistence.CheckpointAfterTerminal,
		TerminalSignal:   &terminalSig,
		NodeRunSnapshot:  nodeRunSnapshotForBreakpoint(acq),
		HeldClaims:       heldClaimsSummaryForBreakpoint(acq),
		OpenWaitSet:      openWaitSetSummaryForBreakpoint(ctx, args, acq),
	}); err != nil && c.Logger != nil {
		c.Logger.Warn("breakpoint: after_terminal eval failed; continuing",
			"dispatch_id", acq.DispatchID.String(),
			"error", err.Error())
	}
	// @constraint: attach the structured ack-body fields to the
	// AsyncContext so the HTTP handler can serialize the response after
	// driveTerminal returns. The handler reads these via the per-call
	// ackOutcome map keyed by dispatch id (set below). Per spec §"HTTP
	// callback ack body: structured response".
	c.recordAckOutcome(ac.DispatchID, ackStatus, phase, rejected)
	return nil
}

// ackStatusForPhase maps a non-{active,held} run phase to the
// corresponding rejected ack_status enum per spec §"HTTP callback ack
// body".
func ackStatusForPhase(phase string) string {
	switch phase {
	case "stale":
		return ackStatusRejectedRunStale
	case "parked":
		return ackStatusRejectedRunParked
	default:
		// @constraint: terminal phases (fresh/failed) and any new
		// phases default to terminal — the run is no longer eligible
		// for callback.
		return ackStatusRejectedRunTerminal
	}
}

// attributesAuth validates the §12.5 incremental-writeback callback's
// `Authorization` header. The token is the supervisor-issued
// `cancel_token` of the form `<supervisorID>:<dispatchID>`.
//
// Under per-run attribute keying (2026-05-20), the URL's path param is
// `run_id` (= dispatchID). Auth passes when:
//
//  1. the token's supervisor segment matches this CallbackServer's
//     SupervisorID (the only supervisor entitled to mint tokens);
//  2. the token's dispatch segment parses as a UUID and equals the
//     URL's run_id (closes the URL-spoof attack against a holder of
//     someone else's token).
//
// The pre-rekeying flow looked up `dispatchID → nodeID` via
// `Queue.GetDispatchNode` because the URL was keyed by `node_id`. Under
// per-run keying the URL is keyed by the same thing the token encodes,
// so the resolution step is unnecessary.
//
// Token shape mirrors `runner_dispatch.go`'s `cancelToken` builder. Any
// shape, supervisor-mismatch, parse-failure, or run-id-mismatch returns
// ErrUnauthorizedCallback so the handler maps to HTTP 401 (per
// `graph/attribute/callback.go` semantics).
func (c *CallbackServer) attributesAuth(token string, runID shared.UUID) error {
	// @constraint: `c.Logger` is defaulted to SilentLogger{} in Start()
	// before this handler is mounted, so it is never nil here — same
	// convention as `handleCallback` which calls `c.Logger.Warn`
	// directly.
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		// @constraint: raw token bytes are user-supplied and may be
		// arbitrarily long or non-printable. Log the length only; the
		// failure mode (no ':' separator) is self-explanatory.
		c.Logger.Warn("attributesAuth: token has no ':' separator",
			"run_id", runID.String(),
			"token_len", len(token))
		return rimskyattrs.ErrUnauthorizedCallback
	}
	tokSupervisor, tokDispatch := parts[0], parts[1]
	if tokSupervisor == "" || tokDispatch == "" {
		c.Logger.Warn("attributesAuth: empty supervisor or dispatch segment",
			"run_id", runID.String(),
			"token_supervisor_len", len(tokSupervisor),
			"token_dispatch_len", len(tokDispatch))
		return rimskyattrs.ErrUnauthorizedCallback
	}
	if c.SupervisorID != "" && tokSupervisor != c.SupervisorID {
		// @constraint: supervisor-mismatch is the most useful branch
		// for diagnostics; log a bounded prefix of the token's
		// supervisor segment so a misconfigured caller is identifiable
		// without flooding logs with arbitrary-length user-supplied
		// bytes.
		c.Logger.Warn("attributesAuth: supervisor id mismatch",
			"run_id", runID.String(),
			"token_supervisor", truncForLog(tokSupervisor, 64),
			"token_supervisor_len", len(tokSupervisor),
			"server_supervisor", c.SupervisorID)
		return rimskyattrs.ErrUnauthorizedCallback
	}
	dispatchID, err := uuid.Parse(tokDispatch)
	if err != nil {
		// @constraint: parse failure mode is self-explanatory; log only
		// the length of the dispatch segment, not its raw bytes.
		c.Logger.Warn("attributesAuth: dispatch id parse failed",
			"run_id", runID.String(),
			"token_dispatch_len", len(tokDispatch),
			"error", err.Error())
		return rimskyattrs.ErrUnauthorizedCallback
	}
	if dispatchID != runID {
		c.Logger.Warn("attributesAuth: run id mismatch",
			"url_run_id", runID.String(),
			"token_dispatch_id", dispatchID.String())
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

func (a attributesStoreAdapter) GetByRun(ctx context.Context, runID shared.UUID) (*rimskyattrs.Row, error) {
	var row *persistence.NodeAttributesRow
	if err := a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := a.store.NodeAttributes().GetByRun(ctx, runID, tx)
		row = r
		return err
	}); err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return &rimskyattrs.Row{
		RunID:     row.NodeRunID,
		NodeID:    row.NodeID,
		Data:      row.Data,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

func (a attributesStoreAdapter) Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any) error {
	return a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return a.store.NodeAttributes().Upsert(ctx, runID, nodeID, data, tx)
	})
}

func (a attributesStoreAdapter) MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any) error {
	return a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return a.store.NodeAttributes().MergeDelta(ctx, runID, delta, tx)
	})
}
