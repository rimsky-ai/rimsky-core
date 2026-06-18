// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	rimskyscratch "github.com/rimsky-ai/rimsky-core/lib/graph/scratch"
)

type callbackAckBody struct {
	AckStatus         string  `json:"ack_status"`
	CurrentDispatchID *string `json:"current_dispatch_id,omitempty"`
}

const (
	ackStatusAccepted            = "accepted"
	ackStatusRejectedRunTerminal = "rejected_run_terminal"
	ackStatusRejectedRunStale    = "rejected_run_stale"
	ackStatusRejectedRunParked   = "rejected_run_parked"
	ackStatusRejectedUnknown     = "rejected_unknown"
)

type ackOutcomeRecord struct {
	Status string
	Phase  string
}

// @concept: async-callback-persistence
type CallbackRegistry struct {
	mu      sync.RWMutex
	pending map[string]AsyncContext
}

func NewCallbackRegistry() *CallbackRegistry {
	return &CallbackRegistry{pending: map[string]AsyncContext{}}
}

func (r *CallbackRegistry) Register(ackID string, ctx AsyncContext) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[ackID] = ctx
}

func (r *CallbackRegistry) Pop(ackID string) (AsyncContext, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.pending[ackID]
	if ok {
		delete(r.pending, ackID)
	}
	return c, ok
}

type CallbackServer struct {
	Registry                         *CallbackRegistry
	Persist                          persistence.Tables
	Queue                            persistence.Queue
	AdvisoryLocker                   persistence.AdvisoryLocker
	ClaimHandles                     persistence.ClaimHandleTable
	Clock                            shared.Clock
	Logger                           shared.Logger
	SupervisorID                     string
	ResumeGrace                      time.Duration
	Blob                             persistence.BlobBackend
	BlobSpillThreshold               int
	MaxRetriesWithoutProgressDefault int
	ExpectedAttributesSchemaFor      func(executorName string) (schema []byte, ok bool)
	Metrics                          MetricsHook
	LifecycleSubs                    *locks.LifecycleRegistry
	LifecyclePeersForSpec            func(tplSpec node.TemplateSpec) []string
	// @concept: data-processing
	DataProcessors DataProcessingRegistry
	addr           string
	srv            *http.Server
	serveErr       chan error
	ackMu          sync.Mutex
	ackOutcomes    map[shared.UUID]ackOutcomeRecord
}

func (c *CallbackServer) recordAckOutcome(dispatchID shared.UUID, status, phase string, _ bool) {
	c.ackMu.Lock()
	defer c.ackMu.Unlock()
	if c.ackOutcomes == nil {
		c.ackOutcomes = make(map[shared.UUID]ackOutcomeRecord)
	}
	c.ackOutcomes[dispatchID] = ackOutcomeRecord{Status: status, Phase: phase}
}

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

func (c *CallbackServer) Start(host string, port int) (string, error) {
	if c.Logger == nil {
		c.Logger = shared.SilentLogger{}
	}
	r := chi.NewRouter()
	r.Post("/v1/callback/{async_ack_id}", c.handleCallback)
	if c.Persist != nil {
		r.Method(http.MethodPost, "/v1/runs/{run_id}/attributes", rimskyattrs.Handler(rimskyattrs.HandlerDeps{
			Store: attributesStoreAdapter{
				store: c.Persist,
				queue: c.Queue,
				clock: c.Clock,
			},
			Auth:   c.attributesAuth,
			Logger: c.Logger,
		}))
	}
	if c.Persist != nil && c.Queue != nil {
		r.Method(http.MethodPost, "/v1/runs/{run_id}/scratch", rimskyscratch.Handler(rimskyscratch.HandlerDeps{
			Writer: scratchStoreAdapter{
				persist:        c.Persist,
				queue:          c.Queue,
				blob:           c.Blob,
				spillThreshold: c.BlobSpillThreshold,
				logger:         c.Logger,
			},
			Auth:   c.attributesAuth,
			Logger: c.Logger,
		}))
	}
	if c.Persist != nil && c.Queue != nil {
		r.Post("/v1/runs/{run_id}/keepalive", c.handleKeepalive)
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
		err := c.srv.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.serveErr <- err
		}
		close(c.serveErr)
	}()
	return c.addr, nil
}

func (c *CallbackServer) Addr() string { return c.addr }

func (c *CallbackServer) ServeErr() <-chan error { return c.serveErr }

func (c *CallbackServer) Close(ctx context.Context) error {
	if c.srv == nil {
		return nil
	}
	return c.srv.Shutdown(ctx)
}

type asyncCallbackBody struct {
	Success *asyncCallbackSuccess `json:"success,omitempty"`
	Error   *asyncCallbackError   `json:"error,omitempty"`
	Park    *asyncCallbackPark    `json:"park,omitempty"`
}

type asyncCallbackSuccess struct {
	Changed         bool           `json:"changed,omitempty"`
	ChangeSummary   string         `json:"change_summary,omitempty"`
	AttributesDelta map[string]any `json:"attributes_delta,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	// @concept: executor
	Scratch []byte `json:"scratch,omitempty"`
}

type asyncCallbackError struct {
	ErrorClass      string         `json:"error_class,omitempty"`
	Payload         any            `json:"payload,omitempty"`
	AttributesDelta map[string]any `json:"attributes_delta,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	// @concept: executor
	Scratch []byte `json:"scratch,omitempty"`
}

type asyncCallbackPark struct {
	Reason          string         `json:"reason,omitempty"`
	ReasonNote      string         `json:"reason_note,omitempty"`
	ReasonLabel     string         `json:"reason_label,omitempty"`
	ResumeAt        string         `json:"resume_at,omitempty"`
	AttributesDelta map[string]any `json:"attributes_delta,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	// @concept: executor
	Scratch []byte `json:"scratch,omitempty"`
}

func (c *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	ackID := chi.URLParam(r, "async_ack_id")
	if ackID == "" {
		http.Error(w, `{"error":"missing async_ack_id"}`, http.StatusBadRequest)
		return
	}
	asyncCtx, ok := c.Registry.Pop(ackID)
	if !ok {
		row, err := c.lookupAsyncCtxByAck(r.Context(), ackID)
		if err != nil {
			c.Logger.Warn("callback: persistent lookup failed", "async_ack_id", ackID, "error", err.Error())
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		if row == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"unknown_async_ack_id"}`))
			return
		}
		asyncCtx = *row
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	t, parseErr := parseAsyncCallback(bodyBytes)
	if parseErr != nil {
		c.Registry.Register(ackID, asyncCtx)
		http.Error(w, `{"error":"`+parseErr.Error()+`"}`, http.StatusBadRequest)
		return
	}

	if err := c.driveTerminal(r.Context(), asyncCtx, t); err != nil {
		c.Registry.Register(ackID, asyncCtx)
		c.Logger.Warn("callback: driveTerminal failed",
			"node_id", asyncCtx.NodeID.String(), "error", err.Error())
		_ = c.consumeAckOutcome(asyncCtx.DispatchID)
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	outcome := c.consumeAckOutcome(asyncCtx.DispatchID)
	body := callbackAckBody{AckStatus: outcome.Status}
	if outcome.Status != ackStatusAccepted && outcome.Status != ackStatusRejectedUnknown {
		if successor := c.findCanonicalSuccessor(r.Context(), asyncCtx); successor != nil {
			s := successor.String()
			body.CurrentDispatchID = &s
		}
	}
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

// @concept: async-callback-persistence
func (c *CallbackServer) lookupAsyncCtxByAck(ctx context.Context, ackID string) (*AsyncContext, error) {
	if c.Persist == nil || c.Queue == nil {
		return nil, nil
	}
	var row *persistence.DispatchRow
	if err := c.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := c.Queue.LookupRunByAsyncAckID(ctx, tx, ackID)
		row = r
		return err
	}); err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	supervisorID := c.SupervisorID
	if row.ClaimedBy != nil {
		supervisorID = *row.ClaimedBy
	}
	var instanceID shared.UUID
	if err := c.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		n, err := c.Persist.Nodes().Get(ctx, row.NodeID, tx)
		if err != nil {
			return err
		}
		if n == nil {
			return fmt.Errorf("lookupAsyncCtxByAck: node %s missing for dispatch %s", row.NodeID, row.ID)
		}
		instanceID = n.InstanceID
		return nil
	}); err != nil {
		return nil, fmt.Errorf("lookupAsyncCtxByAck: resolve instance: %w", err)
	}
	return &AsyncContext{
		NodeID:       row.NodeID,
		InstanceID:   instanceID,
		DispatchID:   row.ID,
		SupervisorID: supervisorID,
		FrameID:      row.FrameID,
	}, nil
}

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
		if nextID == ac.DispatchID {
			return nil
		}
		successor = &nextID
		return nil
	})
	return successor
}

func parseAsyncCallback(raw []byte) (terminalEvent, error) {
	var body asyncCallbackBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return terminalEvent{}, errors.New("invalid json")
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
		return terminalEvent{}, fmt.Errorf("expected AsyncCallbackBody; outcome oneof must be set (success | error | park); got %d outcomes", outcomeCount)
	}
	switch {
	case body.Success != nil:
		return terminalEvent{
			Kind:          terminalKindComplete,
			Changed:       body.Success.Changed,
			ChangeSummary: body.Success.ChangeSummary,
			AttributesDel: body.Success.AttributesDelta,
			Tags:          dedupTagsRT(body.Success.Tags),
			Scratch:       body.Success.Scratch,
		}, nil
	case body.Error != nil:
		return terminalEvent{
			Kind:          terminalKindErrored,
			ErrorClass:    body.Error.ErrorClass,
			Payload:       map[string]any{"payload": body.Error.Payload},
			AttributesDel: body.Error.AttributesDelta,
			Tags:          dedupTagsRT(body.Error.Tags),
			Scratch:       body.Error.Scratch,
		}, nil
	case body.Park != nil:
		t := terminalEvent{
			Kind:            terminalKindPark,
			ParkReason:      parkReasonFromStorageForm(body.Park.Reason),
			ParkReasonNote:  body.Park.ReasonNote,
			ParkReasonLabel: body.Park.ReasonLabel,
			AttributesDel:   body.Park.AttributesDelta,
			Tags:            dedupTagsRT(body.Park.Tags),
			Scratch:         body.Park.Scratch,
		}
		if body.Park.ResumeAt != "" {
			if pt, err := time.Parse(time.RFC3339, body.Park.ResumeAt); err == nil {
				t.ParkResumeAt = pt
			}
		}
		return t, nil
	}
	return terminalEvent{}, errors.New("unreachable")
}

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
		MaxRetriesWithoutProgressDefault: c.MaxRetriesWithoutProgressDefault,
		ExpectedAttributesSchemaFor:      c.ExpectedAttributesSchemaFor,
		Metrics:                          c.Metrics,
		LifecycleSubs:                    c.LifecycleSubs,
		LifecyclePeersForSpec:            c.LifecyclePeersForSpec,
		DataProcessors:                   c.DataProcessors,
	}
	acq := &acquisition{
		DispatchID:     ac.DispatchID,
		NodeID:         ac.NodeID,
		InstanceID:     ac.InstanceID,
		NodeType:       ac.NodeType,
		Executor:       ac.Executor,
		GraphName:      "",
		FrameID:        ac.FrameID,
		Locks:          ac.AcquiredLocks,
		NodeDef:        ac.NodeDef,
		InstanceParams: nil,
	}
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
		return err
	}

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
	c.recordAckOutcome(ac.DispatchID, ackStatus, phase, rejected)
	return nil
}

func ackStatusForPhase(phase string) string {
	switch phase {
	case "stale":
		return ackStatusRejectedRunStale
	case "parked":
		return ackStatusRejectedRunParked
	default:
		return ackStatusRejectedRunTerminal
	}
}

func (c *CallbackServer) attributesAuth(token string, runID shared.UUID) error {
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
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
		c.Logger.Warn("attributesAuth: supervisor id mismatch",
			"run_id", runID.String(),
			"token_supervisor", truncForLog(tokSupervisor, 64),
			"token_supervisor_len", len(tokSupervisor),
			"server_supervisor", c.SupervisorID)
		return rimskyattrs.ErrUnauthorizedCallback
	}
	dispatchID, err := uuid.Parse(tokDispatch)
	if err != nil {
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

func truncForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

type attributesStoreAdapter struct {
	store persistence.Tables
	queue persistence.Queue
	clock shared.Clock
}

func (a attributesStoreAdapter) now() time.Time {
	if a.clock != nil {
		return a.clock.Now().UTC()
	}
	return time.Now().UTC()
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
		if err := a.store.NodeAttributes().Upsert(ctx, runID, nodeID, data, tx); err != nil {
			return err
		}
		if a.queue != nil {
			if _, err := a.queue.BumpLastProgressAt(ctx, tx, runID, a.now()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a attributesStoreAdapter) MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any) error {
	return a.store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := a.store.NodeAttributes().MergeDelta(ctx, runID, delta, tx); err != nil {
			return err
		}
		if a.queue != nil {
			if _, err := a.queue.BumpLastProgressAt(ctx, tx, runID, a.now()); err != nil {
				return err
			}
		}
		return nil
	})
}

// @concept: executor
type scratchStoreAdapter struct {
	persist        persistence.Tables
	queue          persistence.Queue
	blob           persistence.BlobBackend
	spillThreshold int
	logger         shared.Logger
}

func (a scratchStoreAdapter) Write(ctx context.Context, runID shared.UUID, b []byte) error {
	var (
		inline        []byte
		handle        string
		handleBackend string
	)
	if a.blob != nil && persistence.ShouldSpillBlob(a.blob, a.spillThreshold, len(b)) {
		var nodeID shared.UUID
		if id, _, err := a.queue.GetDispatchNode(ctx, runID); err == nil {
			nodeID = id
		} else if a.logger != nil {
			a.logger.Warn("scratchStoreAdapter: node id lookup failed; spill key has empty NodeID hint",
				"dispatch_id", runID.String(), "error", err.Error())
		}
		key := persistence.BlobKey{NodeID: nodeID.String(), Hint: "scratch"}
		h, err := a.blob.Write(ctx, key, b)
		if err != nil {
			if a.logger != nil {
				a.logger.Warn("scratchStoreAdapter: blob spill failed; falling back to inline",
					"dispatch_id", runID.String(), "error", err.Error())
			}
			inline = b
		} else {
			handle = string(h)
			handleBackend = a.blob.Name()
		}
	} else {
		inline = b
	}
	if err := a.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := a.queue.WriteScratchInTx(ctx, tx, runID, inline, handle, handleBackend); err != nil {
			return err
		}
		if _, err := a.queue.BumpLastProgressAt(ctx, tx, runID, time.Now().UTC()); err != nil {
			return err
		}
		return nil
	}); err != nil {
		// @story: opaque-executor-scratch
		if errors.Is(err, persistence.ErrRunRowMissing) {
			return rimskyscratch.ErrRunRowMissing
		}
		return err
	}
	return nil
}
