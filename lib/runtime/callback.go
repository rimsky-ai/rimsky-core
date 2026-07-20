// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

var errUnauthorizedCallback = errors.New("callback: unauthorized")

type callbackAckBody struct {
	AckStatus        string  `json:"ack_status"`
	CurrentNodeRunID *string `json:"current_dispatch_id,omitempty"`
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

// @decision: async-callback-persistent-registry
type CallbackRegistry struct {
	mu      sync.RWMutex
	pending map[string]AsyncContext
}

func NewCallbackRegistry() *CallbackRegistry {
	return &CallbackRegistry{pending: map[string]AsyncContext{}}
}

func (r *CallbackRegistry) Register(ackID string, ctx AsyncContext) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.pending[ackID]; exists {
		return false
	}
	r.pending[ackID] = ctx
	return true
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
	Registry                    *CallbackRegistry
	Persist                     persistence.Tables
	Queue                       persistence.Queue
	AdvisoryLocker              persistence.AdvisoryLocker
	ClaimHandles                persistence.ClaimHandleTable
	StoreRegistry               *locks.Registry
	Clock                       shared.Clock
	Logger                      shared.Logger
	SupervisorID                string
	LivenessInterval            time.Duration
	ResumeGrace                 time.Duration
	Blob                        persistence.BlobBackend
	BlobSpillThreshold          int
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	DeclaredTagsFor             func(executorName string) (tags []string, ok bool)
	Metrics                     MetricsHook
	LifecycleSubs               *locks.LifecycleRegistry
	LifecyclePeersForSpec       func(tplSpec node.TemplateSpec) []string
	// @concept: data-processing
	DataProcessors   DataProcessingRegistry
	ProducerVerbKick func()
	PeerAuth         string
	ServerIdentity   *peer.IdentityHolder
	ClientCAs        *x509.CertPool
	addr             string
	srv              *http.Server
	serveErr         chan error
}

func (c *CallbackServer) authorizePeer(r *http.Request) error {
	if c.PeerAuth != peer.PeerAuthMTLS {
		return nil
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return errUnauthorizedCallback
	}
	if _, err := pki.PrincipalFromVerifiedChains(r.TLS); err != nil {
		return fmt.Errorf("%w: %v", errUnauthorizedCallback, err)
	}
	return nil
}

func (c *CallbackServer) enforceCallbackPrincipal(r *http.Request, asyncCtx AsyncContext) error {
	if c.PeerAuth != peer.PeerAuthMTLS || asyncCtx.AsyncAckPrincipal == "" {
		return nil
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return errUnauthorizedCallback
	}
	got, err := pki.PrincipalFromVerifiedChains(r.TLS)
	if err != nil {
		return fmt.Errorf("%w: %v", errUnauthorizedCallback, err)
	}
	if got != asyncCtx.AsyncAckPrincipal {
		return fmt.Errorf("%w: callback principal %q does not match dispatched executor principal %q", errUnauthorizedCallback, got, asyncCtx.AsyncAckPrincipal)
	}
	return nil
}

func (c *CallbackServer) Start(host string, port int) (string, error) {
	if c.Logger == nil {
		c.Logger = shared.SilentLogger{}
	}
	r := chi.NewRouter()
	r.Post("/v1/callback/{async_ack_id}", c.handleCallback)
	if c.Persist != nil && c.Queue != nil {
		r.Post("/v1/runs/{run_id}/keepalive", c.handleKeepalive)
		r.Post("/v1/runs/{run_id}/attributes", c.handleAttributeWriteback)
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
	if c.PeerAuth == peer.PeerAuthMTLS {
		if c.ServerIdentity == nil {
			_ = listener.Close()
			return "", fmt.Errorf("callback: peer_auth mtls requires a server identity")
		}
		if c.ClientCAs == nil {
			_ = listener.Close()
			return "", fmt.Errorf("callback: peer_auth mtls requires a client CA pool")
		}
		c.srv.TLSConfig = peer.TLSServerConfig(peer.PeerAuthMTLS, c.ServerIdentity, c.ClientCAs)
	}
	c.serveErr = make(chan error, 1)
	go func() {
		var serveErr error
		if c.PeerAuth == peer.PeerAuthMTLS {
			serveErr = c.srv.ServeTLS(listener, "", "")
		} else {
			serveErr = c.srv.Serve(listener)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			c.serveErr <- serveErr
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
	err := c.srv.Shutdown(ctx)
	<-c.serveErr
	return err
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
	ResumeAt string   `json:"resume_at"`
	Tags     []string `json:"tags,omitempty"`
	// @concept: executor
	Scratch []byte `json:"scratch,omitempty"`
}

func (c *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	if err := c.authorizePeer(r); err != nil {
		c.Logger.Warn("callback: unauthorized peer", "error", err.Error())
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
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
	if err := c.enforceCallbackPrincipal(r, asyncCtx); err != nil {
		c.Registry.Register(ackID, asyncCtx)
		c.Logger.Warn("callback: principal binding rejected", "async_ack_id", ackID, "error", err.Error())
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
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

	outcome, err := c.driveTerminal(r.Context(), asyncCtx, t)
	if err != nil {
		c.Registry.Register(ackID, asyncCtx)
		c.Logger.Warn("callback: driveTerminal failed",
			"node_id", asyncCtx.NodeID.String(), "error", err.Error())
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	body := callbackAckBody{AckStatus: outcome.Status}
	if outcome.Status != ackStatusAccepted && outcome.Status != ackStatusRejectedUnknown {
		if successor := c.findCanonicalSuccessor(r.Context(), asyncCtx); successor != nil {
			s := successor.String()
			body.CurrentNodeRunID = &s
		}
	}
	if outcome.Status == ackStatusAccepted {
		if err := c.Queue.Complete(r.Context(), asyncCtx.NodeRunID, asyncCtx.SupervisorID); err != nil {
			c.Logger.Error("callback: queue.Complete failed after applied terminal",
				"node_id", asyncCtx.NodeID.String(),
				"dispatch_id", asyncCtx.NodeRunID.String(),
				"supervisor_id", asyncCtx.SupervisorID,
				"error", err.Error())
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		c.Logger.Warn("callback: encode ack body failed",
			"dispatch_id", asyncCtx.NodeRunID.String(),
			"error", err.Error())
	}
}

// @decision: async-callback-persistent-registry
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
	args := c.runArgs(supervisorID, c.StoreRegistry)
	acq := acquisition{
		NodeRunID: row.ID,
		NodeID:    row.NodeID,
		FrameID:   row.FrameID,
	}
	if err := c.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return c.reconstructAcquisition(ctx, args, tx, row, &acq)
	}); err != nil {
		return nil, fmt.Errorf("lookupAsyncCtxByAck: reconstruct acquisition: %w", err)
	}
	resolvedAttrs, schema, err := recoverResolvedAttributes(ctx, args, &acq)
	if err != nil {
		c.Logger.Warn("callback: attribute reconstruction failed; settling with base bag only",
			"async_ack_id", ackID,
			"dispatch_id", row.ID.String(),
			"error", err.Error())
	}
	expectedPrincipal := ""
	if row.AsyncAckPrincipal != nil {
		expectedPrincipal = *row.AsyncAckPrincipal
	}
	return &AsyncContext{
		NodeID:             acq.NodeID,
		InstanceID:         acq.InstanceID,
		NodeRunID:          acq.NodeRunID,
		SupervisorID:       supervisorID,
		StoreRegistry:      c.StoreRegistry,
		FrameID:            acq.FrameID,
		AcquiredLocks:      acq.Locks,
		NodeType:           acq.NodeType,
		Executor:           acq.Executor,
		NodeDef:            acq.NodeDef,
		GraphName:          acq.GraphName,
		ResolvedAttributes: resolvedAttrs,
		AttributesSchema:   schema,
		AsyncAckPrincipal:  expectedPrincipal,
	}, nil
}

// @decision: async-callback-persistent-registry
func (c *CallbackServer) reconstructAcquisition(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row *persistence.DispatchRow, acq *acquisition,
) error {
	nd, err := c.Persist.Nodes().Get(ctx, row.NodeID, tx)
	if err != nil {
		return err
	}
	if nd == nil {
		return fmt.Errorf("node %s missing for dispatch %s", row.NodeID, row.ID)
	}
	acq.InstanceID = nd.InstanceID
	acq.NodeType = nd.NodeType
	acq.Executor = nd.Executor
	inst, err := c.Persist.Instances().Get(ctx, nd.InstanceID, tx)
	if err != nil {
		return err
	}
	tmpl := lookupTemplate(ctx, args, tx, inst)
	acq.NodeDef = lookupNodeDef(tmpl, nd.NodeType)
	acq.TemplateAttributeDefaults = templateAttributeDefaultsFor(tmpl, nd.Executor)
	acq.GraphName = spec.MainGraphName
	if tmpl != nil {
		acq.GraphName = graphContainingNodeType(tmpl.Graphs, nd.NodeType)
	}
	if inst != nil {
		acq.InstanceParams = inst.Params
		acq.InstanceAttributeOverrides = inst.AttributeOverrides
		acq.TemplateHash = inst.TemplateHash
	}
	if rt := c.Persist.NodeRunTree(); rt != nil {
		treeRow, err := rt.GetByID(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		if treeRow != nil {
			acq.RunScopeID = treeRow.RunScopeID
		}
	}
	acqLocks, err := reconstructAcquiredLocks(ctx, args, tx, row.ID, acq.NodeDef)
	if err != nil {
		return err
	}
	acq.Locks = acqLocks
	held, err := loadInheritedClaimsForNode(ctx, args, tx, nd, acq.FrameID)
	if err != nil {
		return fmt.Errorf("reconstructAcquisition: load inherited claims: %w", err)
	}
	if len(held) > 0 {
		acq.HeldClaims = held
	}
	return nil
}

// @decision: async-callback-persistent-registry
func reconstructAcquiredLocks(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	nodeRunID shared.UUID, nodeDef *node.TemplateNodeDef,
) ([]AcquiredLock, error) {
	if args.ClaimHandles == nil {
		return nil, nil
	}
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, nodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("reconstructAcquiredLocks: ListByNodeRun: %w", err)
	}
	aliasByProducer := map[string]string{}
	if nodeDef != nil {
		for _, ref := range nodeDef.ClaimProducers {
			aliasByProducer[ref.Name] = ref.AliasOf()
		}
	}
	var out []AcquiredLock
	for i := range rows {
		row := rows[i]
		if row.ParentClaimHandleID != nil || row.State != spec.ClaimHandleStateActive {
			continue
		}
		switch row.LockKind {
		case persistence.LockKindNamed:
			name := ""
			if row.LockName != nil {
				name = *row.LockName
			}
			out = append(out, AcquiredLock{
				Spec:          locks.NamedLockSpec{Name: name},
				ClaimHandleID: row.ID,
				IsHeld:        row.IsHeld,
			})
		case persistence.LockKindScope:
			producerName := ""
			if row.ProducerName != nil {
				producerName = *row.ProducerName
			}
			var producer locks.ClaimProducer
			if args.StoreRegistry != nil {
				if p, ok := args.StoreRegistry.Get(producerName); ok {
					producer = p
				}
			}
			alias := aliasByProducer[producerName]
			out = append(out, AcquiredLock{
				Spec: claimproducer.ClaimSpec{
					ProducerName: producerName,
					Alias:        alias,
					Lifetime:     string(row.Lifetime),
				},
				ClaimHandleID: row.ID,
				ClaimResult: claimproducer.ClaimResult{
					Address:    row.Address,
					Payload:    row.Payload,
					ClaimScope: row.ClaimScopeData,
				},
				Producer:                producer,
				Alias:                   alias,
				IsHeld:                  row.IsHeld,
				ProducerCandidateHandle: row.ProducerCandidateHandle,
			})
		}
	}
	return out, nil
}

// @decision: async-callback-persistent-registry
func recoverResolvedAttributes(ctx context.Context, args RunArgs, acq *acquisition) (map[string]any, map[string]any, error) {
	if acq.Executor != "" && acq.NodeDef != nil && acq.NodeDef.Attributes != nil {
		schema, _, execSchemaVisible := computeEffectiveAttributeSchema(args, acq)
		if !execSchemaVisible {
			return nil, schema, &executorSchemaUnavailableError{Executor: acq.Executor}
		}
	}
	merged, schema, _, _, err := resolveAttributesCore(ctx, args, acq)
	if err != nil {
		return nil, schema, err
	}
	acq.MergedAttributes = merged
	return merged, schema, nil
}

func (c *CallbackServer) runArgs(supervisorID string, storeRegistry *locks.Registry) RunArgs {
	return RunArgs{
		Persist:                     c.Persist,
		Queue:                       c.Queue,
		AdvisoryLocker:              c.AdvisoryLocker,
		ClaimHandles:                c.ClaimHandles,
		StoreRegistry:               storeRegistry,
		Clock:                       c.Clock,
		Logger:                      c.Logger,
		SupervisorID:                supervisorID,
		ResumeGrace:                 c.ResumeGrace,
		Blob:                        c.Blob,
		BlobSpillThreshold:          c.BlobSpillThreshold,
		ExpectedAttributesSchemaFor: c.ExpectedAttributesSchemaFor,
		DeclaredTagsFor:             c.DeclaredTagsFor,
		Metrics:                     c.Metrics,
		LifecycleSubs:               c.LifecycleSubs,
		LifecyclePeersForSpec:       c.LifecyclePeersForSpec,
		DataProcessors:              c.DataProcessors,
		ProducerVerbKick:            c.ProducerVerbKick,
	}
}

func (c *CallbackServer) findCanonicalSuccessor(ctx context.Context, ac AsyncContext) *shared.UUID {
	var successor *shared.UUID
	_ = c.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := c.Persist.Nodes().GetRunByDispatchIDForUpdate(ctx, ac.NodeRunID, tx)
		if err != nil || row == nil {
			return nil
		}
		nextID, ok, err := c.Queue.GetInFlightRunForNode(ctx, tx, row.NodeID, row.RunScopeID)
		if err != nil || !ok {
			return nil
		}
		if nextID == ac.NodeRunID {
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
		if body.Park.ResumeAt == "" {
			return terminalEvent{}, errors.New("park requires resume_at")
		}
		pt, err := time.Parse(time.RFC3339, body.Park.ResumeAt)
		if err != nil {
			return terminalEvent{}, fmt.Errorf("park resume_at: invalid RFC3339 timestamp %q", body.Park.ResumeAt)
		}
		return terminalEvent{
			Kind:         terminalKindPark,
			ParkResumeAt: pt,
			Tags:         dedupTagsRT(body.Park.Tags),
			Scratch:      body.Park.Scratch,
		}, nil
	}
	return terminalEvent{}, errors.New("unreachable")
}

func (c *CallbackServer) driveTerminal(ctx context.Context, ac AsyncContext, t terminalEvent) (ackOutcomeRecord, error) {
	args := c.runArgs(ac.SupervisorID, ac.StoreRegistry)
	acq := &acquisition{
		NodeRunID:      ac.NodeRunID,
		NodeID:         ac.NodeID,
		InstanceID:     ac.InstanceID,
		NodeType:       ac.NodeType,
		Executor:       ac.Executor,
		GraphName:      ac.GraphName,
		FrameID:        ac.FrameID,
		Locks:          ac.AcquiredLocks,
		NodeDef:        ac.NodeDef,
		InstanceParams: nil,
	}
	var ackStatus string
	var phase string
	rejected := false
	setup := func(ctx context.Context, tx persistence.Tx) (bool, error) {
		row, err := c.Persist.Nodes().GetRunByDispatchIDForUpdate(ctx, ac.NodeRunID, tx)
		if err != nil {
			return false, fmt.Errorf("driveTerminal: GetRunByDispatchIDForUpdate: %w", err)
		}
		if row == nil {
			rejected = true
			ackStatus = ackStatusRejectedUnknown
			c.Logger.Warn("callback.late_or_stale_run",
				"dispatch_id", ac.NodeRunID.String(),
				"reason", "run_not_found")
			return true, nil
		}
		if row.State != cascade.NodeStateRunning && row.State != cascade.NodeStateHeld {
			rejected = true
			phase = string(row.State)
			ackStatus = ackStatusForState(row.State)
			c.Logger.Warn("callback.late_or_stale_run",
				"dispatch_id", ac.NodeRunID.String(),
				"current_state", string(row.State),
				"expected_state", "running|held")
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
	t = validateTags(ctx, dispatchContext{Args: args, Acquired: acq}, t)
	if err := runApplyTerminal(ctx, args, acq, ac.ResolvedAttributes, ac.AttributesSchema, t, setup); err != nil {
		return ackOutcomeRecord{}, err
	}

	outcome := ackOutcomeRecord{Status: ackStatus, Phase: phase}
	if rejected {
		return outcome, nil
	}

	scope := resolveAcqScope(ctx, args, acq)
	terminalSig := signalForTerminal(args, t)
	if _, err := EvaluateBreakpoints(ctx, args, CheckpointContext{
		InstanceID:       acq.InstanceID,
		NodeID:           acq.NodeID,
		NodeRunID:        acq.NodeRunID,
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
			"dispatch_id", acq.NodeRunID.String(),
			"error", err.Error())
	}
	return outcome, nil
}

func ackStatusForState(s cascade.NodeState) string {
	switch s {
	case cascade.NodeStateStale, cascade.NodeStatePending:
		return ackStatusRejectedRunStale
	case cascade.NodeStateParked:
		return ackStatusRejectedRunParked
	default:
		return ackStatusRejectedRunTerminal
	}
}
