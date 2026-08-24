// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
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
	ClaimProducerRegistry       *locks.Registry
	Clock                       shared.Clock
	Logger                      shared.Logger
	SupervisorID                string
	LivenessInterval            time.Duration
	ResumeGrace                 time.Duration
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	DeclaredTagsFor             func(executorName string) (tags []string, ok bool)
	Metrics                     MetricsHook
	LateBindServiceProxies      map[string]string
	// @decision: lifecycle-drain-per-role
	LifecycleKick func()
	// @concept: data-processing
	DataProcessors   DataProcessingRegistry
	ProducerVerbKick func()
	ServiceAuth      string
	ServerIdentity   *service.IdentityHolder
	ClientCAs        *x509.CertPool
	addr             string
	srv              *http.Server
	serveErr         chan error
}

func (c *CallbackServer) authorizeService(r *http.Request) error {
	if c.ServiceAuth != service.ServiceAuthMTLS {
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
	if c.ServiceAuth != service.ServiceAuthMTLS || asyncCtx.AsyncAckPrincipal == "" {
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

// @decision: protocol-version-v1-namespaced
func (c *CallbackServer) Routes() chi.Router {
	r := chi.NewRouter()
	// @decision: async-callback-post-json
	r.Post("/v1/callback/{async_ack_id}", c.handleCallback)
	if c.Persist != nil && c.Queue != nil {
		r.Post("/v1/runs/{run_id}/keepalive", c.handleKeepalive)
		r.Post("/v1/runs/{run_id}/attributes", c.handleAttributeWriteback)
	}
	r.Get("/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return r
}

func (c *CallbackServer) Start(host string, port int) (string, error) {
	if c.Logger == nil {
		c.Logger = shared.SilentLogger{}
	}
	r := c.Routes()

	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return "", err
	}
	c.addr = listener.Addr().String()
	c.srv = &http.Server{Handler: r}
	if c.ServiceAuth == service.ServiceAuthMTLS {
		if c.ServerIdentity == nil {
			_ = listener.Close()
			return "", fmt.Errorf("callback: service_auth mtls requires a server identity")
		}
		if c.ClientCAs == nil {
			_ = listener.Close()
			return "", fmt.Errorf("callback: service_auth mtls requires a client CA pool")
		}
		c.srv.TLSConfig = service.TLSServerConfig(service.ServiceAuthMTLS, c.ServerIdentity, c.ClientCAs)
	}
	c.serveErr = make(chan error, 1)
	go func() {
		var serveErr error
		if c.ServiceAuth == service.ServiceAuthMTLS {
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

// @decision: run-token-swept
func (c *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	if err := c.authorizeService(r); err != nil {
		c.Logger.Warn("CALLBACK.SERVICE.UNAUTHORIZED", "error", err.Error())
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
			c.Logger.Warn("CALLBACK.ASYNCACK.LOOKUPFAILED", "async_ack_id", ackID, "error", err.Error())
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
		c.Logger.Warn("CALLBACK.PRINCIPALBINDING.REJECTED", "async_ack_id", ackID, "error", err.Error())
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
		c.Logger.Warn("CALLBACK.DRIVETERMINAL.FAILED",
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
		if err := c.Queue.Complete(context.WithoutCancel(r.Context()), asyncCtx.NodeRunID, asyncCtx.SupervisorID); err != nil {
			c.Logger.Error("CALLBACK.QUEUECOMPLETE.FAILED", "detail", "the terminal was already applied",
				"node_id", asyncCtx.NodeID.String(),
				"dispatch_id", asyncCtx.NodeRunID.String(),
				"supervisor_id", asyncCtx.SupervisorID,
				"error", err.Error())
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		c.Logger.Warn("CALLBACK.ACKBODY.ENCODEFAILED",
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
		r, err := c.Queue.LookupRunByAsyncAckID(ctx, ackID, tx)
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
	args := c.runArgs(supervisorID, c.ClaimProducerRegistry)
	acq := acquisition{
		NodeRunID: row.ID,
		NodeID:    row.NodeID,
		FrameID:   row.FrameID,
	}
	if err := c.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return c.reconstructAcquisition(ctx, args, row, &acq, tx)
	}); err != nil {
		return nil, fmt.Errorf("lookupAsyncCtxByAck: reconstruct acquisition: %w", err)
	}
	resolvedAttrs, schema, err := recoverResolvedAttributes(ctx, args, &acq)
	if err != nil {
		c.Logger.Warn("CALLBACK.ATTRIBUTERECONSTRUCTION.FAILED", "detail", "settling with the base bag only",
			"async_ack_id", ackID,
			"dispatch_id", row.ID.String(),
			"error", err.Error())
	}
	expectedPrincipal := ""
	if row.AsyncAckPrincipal != nil {
		expectedPrincipal = *row.AsyncAckPrincipal
	}
	return &AsyncContext{
		NodeID:                acq.NodeID,
		InstanceID:            acq.InstanceID,
		NodeRunID:             acq.NodeRunID,
		SupervisorID:          supervisorID,
		ClaimProducerRegistry: c.ClaimProducerRegistry,
		FrameID:               acq.FrameID,
		AcquiredLocks:         acq.Locks,
		NodeType:              acq.NodeType,
		Executor:              acq.Executor,
		NodeDef:               acq.NodeDef,
		GraphName:             acq.GraphName,
		ResolvedAttributes:    resolvedAttrs,
		AttributesSchema:      schema,
		AsyncAckPrincipal:     expectedPrincipal,
	}, nil
}

// @decision: async-callback-persistent-registry
func (c *CallbackServer) reconstructAcquisition(
	ctx context.Context, args RunArgs, row *persistence.DispatchRow, acq *acquisition, tx persistence.Tx,
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
	tmpl := lookupTemplate(ctx, args, inst, tx)
	acq.NodeDef = LookupNodeDef(tmpl, nd.NodeType)
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
		treeRow, err := rt.GetByID(ctx, row.ID, tx)
		if err != nil {
			return err
		}
		if treeRow != nil {
			acq.RunScopeID = treeRow.RunScopeID
		}
	}
	acqLocks, err := reconstructAcquiredLocks(ctx, args, row.ID, acq.NodeDef, tx)
	if err != nil {
		return err
	}
	acq.Locks = acqLocks
	held, err := loadInheritedClaimsForNode(ctx, args, nd.InstanceID, tmpl, acq.NodeDef, acq.FrameID, tx)
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
	ctx context.Context, args RunArgs, nodeRunID shared.UUID, nodeDef *node.TemplateNodeDef, tx persistence.Tx,
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
			if args.ClaimProducerRegistry != nil {
				p, ok, resolveErr := args.ClaimProducerRegistry.ResolveWithContext(ctx, producerName, "", tx)
				if resolveErr != nil {
					return nil, fmt.Errorf("reconstructAcquiredLocks: resolving producer %q: %w", producerName, resolveErr)
				}
				if ok {
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

func (c *CallbackServer) runArgs(supervisorID string, claimProducerRegistry *locks.Registry) RunArgs {
	return RunArgs{
		Persist:                     c.Persist,
		Queue:                       c.Queue,
		AdvisoryLocker:              c.AdvisoryLocker,
		ClaimHandles:                c.ClaimHandles,
		ClaimProducerRegistry:       claimProducerRegistry,
		Clock:                       c.Clock,
		Logger:                      c.Logger,
		SupervisorID:                supervisorID,
		ResumeGrace:                 c.ResumeGrace,
		ExpectedAttributesSchemaFor: c.ExpectedAttributesSchemaFor,
		DeclaredTagsFor:             c.DeclaredTagsFor,
		Metrics:                     c.Metrics,
		LateBindServiceProxies:      c.LateBindServiceProxies,
		LifecycleKick:               c.LifecycleKick,
		DataProcessors:              c.DataProcessors,
		ProducerVerbKick:            c.ProducerVerbKick,
	}
}

func (c *CallbackServer) findCanonicalSuccessor(ctx context.Context, ac AsyncContext) *shared.UUID {
	var successor *shared.UUID
	if err := c.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := c.Persist.Nodes().GetRunByDispatchIDForUpdate(ctx, ac.NodeRunID, tx)
		if err != nil {
			return err
		}
		if row == nil {
			return nil
		}
		nextID, ok, err := c.Queue.GetInFlightRunForNode(ctx, row.NodeID, row.RunScopeID, tx)
		if err != nil {
			return err
		}
		if !ok || nextID == ac.NodeRunID {
			return nil
		}
		successor = &nextID
		return nil
	}); err != nil && c.Logger != nil {
		c.Logger.Warn("CALLBACK.CANONICALSUCCESSOR.LOOKUPFAILED", "site", "findCanonicalSuccessor", "detail", "current_dispatch_id is omitted from the ack",
			"node_run_id", ac.NodeRunID.String(),
			"error", err.Error())
	}
	return successor
}

// @decision: async-callback-outcome-oneof
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
			Tags:          shared.DedupStrings(body.Success.Tags),
			Scratch:       body.Success.Scratch,
		}, nil
	case body.Error != nil:
		errPayload, _ := body.Error.Payload.(map[string]any)
		return terminalEvent{
			Kind:          terminalKindErrored,
			ErrorClass:    body.Error.ErrorClass,
			Payload:       errPayload,
			AttributesDel: body.Error.AttributesDelta,
			Tags:          shared.DedupStrings(body.Error.Tags),
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
			Tags:         shared.DedupStrings(body.Park.Tags),
			Scratch:      body.Park.Scratch,
		}, nil
	}
	return terminalEvent{}, errors.New("unreachable")
}

func (c *CallbackServer) driveTerminal(ctx context.Context, ac AsyncContext, t terminalEvent) (ackOutcomeRecord, error) {
	args := c.runArgs(ac.SupervisorID, ac.ClaimProducerRegistry)
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
		// @decision: lineage-records-computation-only
		ExecutorInvoked: true,
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
			c.Logger.Warn("CALLBACK.RUN.LATEORSTALE",
				"dispatch_id", ac.NodeRunID.String(),
				"reason", "run_not_found")
			return true, nil
		}
		if row.State != cascade.NodeStateRunning && row.State != cascade.NodeStateHeld {
			rejected = true
			phase = string(row.State)
			ackStatus = ackStatusForState(row.State)
			c.Logger.Warn("CALLBACK.RUN.LATEORSTALE",
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
			c.Logger.Warn("CALLBACK.INSTANCE.LOOKUPFAILED", "site", "driveTerminal", "detail", "the lineage record omits template_hash and params",
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

	scope := resolveAcqScope(ctx, args, acq, nil)
	terminalSig := signalForTerminal(args, acq, t)
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
		c.Logger.Warn("BREAKPOINT.AFTERTERMINALEVAL.FAILED", "detail", "continuing",
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
