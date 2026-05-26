// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Omnibus runner — attribute substitution + executor / native
// dispatch path. Terminal handling lives in runner_terminal.go.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	signalpkg "github.com/fallguyconsulting/rimsky/foundation/signal"
	signalaudit "github.com/fallguyconsulting/rimsky/foundation/signal/audit"
	attributes "github.com/fallguyconsulting/rimsky/graph/attribute"
	"github.com/fallguyconsulting/rimsky/graph/node"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	"github.com/fallguyconsulting/rimsky/runtime/executor"
	"github.com/fallguyconsulting/rimsky/runtime/peer"
)

// attributeValidationError wraps non-resolution attribute failures
// raised by resolveAttributes: schema-invalid composition errors,
// JSON-Schema-validation failures on the dispatch bag, and (post the
// 2026-05-21 gap-closure cycle) the dispatch-time defense-in-depth
// validation against the executor's raw expected_attributes_schema.
//
// Routed by applyAttributeFailure to the `template_validation_failed`
// policy chain — distinct from `template_resolution_failed` (strict-
// directive misses, *attributes.ErrMissingSource) and
// `executor_schema_unavailable` (executor's schema not visible at
// dispatch). Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Error handling".
//
// @concept: attribute
type attributeValidationError struct {
	Reason string
	Cause  error
}

func (e *attributeValidationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Reason, e.Cause)
	}
	return e.Reason
}

func (e *attributeValidationError) Unwrap() error { return e.Cause }

// executorSchemaUnavailableError is the typed marker for the
// `executor_schema_unavailable` class: the executor's
// expected_attributes_schema isn't visible at dispatch (handshake not
// completed, discovery cache empty, or executor advertises no schema).
// Routed to its own policy chain so operators can override retry-after-
// handshake-completes behaviour separately from validation failures.
type executorSchemaUnavailableError struct {
	Executor string
}

func (e *executorSchemaUnavailableError) Error() string {
	return fmt.Sprintf(
		"executor_schema_unavailable: executor %q has no visible expected_attributes_schema at dispatch (handshake not completed or discovery cache empty)",
		e.Executor,
	)
}

// dispatchContext carries the per-dispatch state through the
// executor stream loop.
type dispatchContext struct {
	Args              RunArgs
	Acquired          *acquisition
	Attributes        map[string]any
	AttributesSchema  map[string]any
	HeartbeatInterval time.Duration
	Log               shared.Logger
	RegisterAsync     func(ackID string, actx AsyncContext)
}

// terminalEvent is the runner-internal classification of an executor
// stream's terminal event.
type terminalEvent struct {
	Kind          terminalKind
	Changed       bool
	ChangeSummary string
	AttributesDel map[string]any
	ErrorClass    string
	Payload       map[string]any
	// Park fields — set when Kind == terminalKindPark.
	ParkReason       genv1.ParkReason
	ParkReasonNote   string
	ParkReasonLabel  string // freeform optional classification tag; opaque to rimsky (closed enum no longer requires it).
	ParkPayload      []byte
	ParkResumeAt     time.Time // zero ⇒ indefinite
	ParkSessionToken string
	// NamedEvents is the optional list of non-terminal NamedEvent
	// emissions captured during the dispatch (gRPC stream) or in the
	// async-callback body. Processed in arrival order before the
	// terminal verdict (per plan H1).
	NamedEvents []namedEventRecord
}

// namedEventRecord captures one NamedEvent emission for ledger persistence.
//
// PayloadInline / PayloadHandle / PayloadHandleBackend follow the same
// inline-or-spill discipline as parked payloads and node attributes.
// At the runtime entry point (before spill), only PayloadInline is
// populated; spill-write happens in H1's persist path.
type namedEventRecord struct {
	Name                 string
	PayloadInline        []byte
	PayloadHandle        string
	PayloadHandleBackend string
}

type terminalKind int

const (
	terminalKindNone terminalKind = iota
	terminalKindComplete
	// terminalKindErrored covers every error path. Pre-2026-05-12, the
	// wire protocol split Blocked from Errored; per spec E.2/E.9 they
	// collapsed into Error{error_class}, and `executor_blocked` is
	// just one of the error classes the operator's `error_types:`
	// policy can route on.
	terminalKindErrored
	terminalKindAsyncAccepted
	terminalKindInfra
	// terminalKindPark is the in-runner flavor of the protocol-level
	// Park event. The supervisor's terminal-handler chain dispatches
	// to applyTerminalPark for this kind.
	terminalKindPark
)

// dispatch routes the candidate to the appropriate execution path.
func dispatch(ctx context.Context, dctx dispatchContext) (terminalEvent, *RunnerResult, error) {
	acq := dctx.Acquired
	args := dctx.Args
	metrics := metricsOf(args)
	dispatchStart := args.Clock.Now()
	defer func() {
		// Record dispatch latency unconditionally — async paths return
		// before the executor terminal arrives, so this measures the
		// supervisor-side dispatch envelope rather than full executor
		// duration. Async terminals are observed separately in the
		// callback path.
		metrics.ObserveDispatchLatency(acq.Executor, args.Clock.Now().Sub(dispatchStart).Seconds())
	}()
	metrics.IncDispatch(acq.Executor, "started")

	if acq.Executor == "" {
		// Native dispatch: claim-only or pure-cascade. Synthesize a
		// Success{changed: true} so dependents recalc.
		summary := "pure_cascade"
		for _, lk := range acq.Locks {
			if _, ok := lk.Spec.(locks.ClaimSpec); ok {
				summary = "claim_acquired"
				break
			}
		}
		return terminalEvent{Kind: terminalKindComplete, Changed: true, ChangeSummary: summary}, nil, nil
	}

	// Real per-dispatch instance/run-scope context for late-bind
	// resolution. A LateBindResolver consults service_bindings on the
	// instance; a StaticResolver ignores these fields.
	ep, ok := args.Resolver.Resolve(acq.Executor, executor.DispatchContext{
		Ctx:        ctx,
		InstanceID: acq.InstanceID.String(),
		RunScopeID: acq.RunScopeID.String(),
	})
	if !ok {
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "unresolved_executor",
				Payload: map[string]any{
					"executor_name": acq.Executor,
					"supervisor_id": args.SupervisorID,
				},
			}, tx)
		}); err != nil && args.Logger != nil {
			args.Logger.Warn("runner_dispatch: append unresolved_executor event failed",
				"node_id", acq.NodeID.String(),
				"executor_name", acq.Executor,
				"error", err.Error())
		}
		return terminalEvent{
			Kind:       terminalKindErrored,
			ErrorClass: "unresolved_executor",
			Payload:    map[string]any{"executor_name": acq.Executor},
		}, nil, nil
	}

	client, err := args.Pool.GetOrCreate(ep)
	if err != nil {
		return terminalEvent{Kind: terminalKindInfra, ErrorClass: "executor_dial_failed",
			Payload: map[string]any{"error": err.Error()}}, nil, nil
	}
	req, err := buildExecuteRequest(ctx, dctx)
	if err != nil {
		return terminalEvent{Kind: terminalKindInfra, ErrorClass: "build_request_failed",
			Payload: map[string]any{"error": err.Error()}}, nil, nil
	}
	// Stamp the executor name so a host-agent-proxy fronting the
	// executor protocol can route this Execute by service name. The
	// stream interceptor reads it off the context; a directly-dialed
	// hosted executor ignores the header.
	ctx = peer.WithServiceName(ctx, acq.Executor)
	stream, err := client.Execute(ctx, req)
	if err != nil {
		return terminalEvent{Kind: terminalKindInfra, ErrorClass: "executor_dial_failed",
			Payload: map[string]any{"error": err.Error()}}, nil, nil
	}
	// At this point the executor has accepted the Execute RPC (the
	// stream handle is live). The resume metadata has been
	// materialized into ResumeContext on the wire; clear it now so a
	// re-park during this run, or any later retry cycle, starts a
	// fresh resume cycle. If the stream subsequently errors mid-flight
	// (executor crash, network blip), the dispatch is re-enqueued
	// fresh via applyTerminalInfraError — by design, since the
	// executor already saw the resume payload.
	if dctx.Acquired.Resume != nil {
		if err := dctx.Args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return dctx.Args.Queue.ClearResumeMetadataInTx(ctx, tx, dctx.Acquired.DispatchID)
		}); err != nil {
			dctx.Args.Logger.Warn("runner_dispatch: clear resume metadata failed",
				"dispatch_id", dctx.Acquired.DispatchID.String(),
				"node_id", dctx.Acquired.NodeID.String(),
				"error", err.Error())
		}
	}
	terminal, asyncAck := readExecutorStream(ctx, dctx, stream)
	_ = stream.Close()

	if asyncAck != "" {
		registerAsyncIfSet(dctx, asyncAck)
		// Canonical signal emission per concept:signal. transient/
		// await_async fires when the executor returns
		// AwaitAsyncCallback; the node stays in running state and
		// the actual settling happens via the callback path. No
		// fixed-string audit row existed for this transition
		// pre-Pass-1, so the signal write stands alone.
		if dctx.Args.Persist != nil {
			awaitSig := signalpkg.Signal{
				Type: "transient/await_async",
				Payload: map[string]any{
					"async_ack_id": asyncAck,
					"callback_url": dctx.Args.CallbackURL,
				},
			}
			if err := dctx.Args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return signalaudit.EmitSignal(ctx, dctx.Args.Persist.Events(),
					acq.InstanceID, acq.NodeID, awaitSig, dctx.Args.Clock.Now(), tx)
			}); err != nil && dctx.Args.Logger != nil {
				dctx.Args.Logger.Warn("runner_dispatch: emit transient/await_async signal failed",
					"node_id", acq.NodeID.String(),
					"async_ack_id", asyncAck,
					"error", err.Error())
			}
		}
		return terminalEvent{}, &RunnerResult{
			Ran:        true,
			Async:      true,
			AsyncAckID: asyncAck,
			NodeID:     acq.NodeID,
			DispatchID: acq.DispatchID,
		}, nil
	}
	return terminal, nil, nil
}

// registerAsyncIfSet hands the per-run AsyncContext to the callback
// registry so the deferred terminal-handler can reconstruct
// RunArgs+acquisition.
func registerAsyncIfSet(dctx dispatchContext, asyncAck string) {
	if dctx.RegisterAsync == nil {
		return
	}
	acq := dctx.Acquired
	dctx.RegisterAsync(asyncAck, AsyncContext{
		NodeID:             acq.NodeID,
		InstanceID:         acq.InstanceID,
		DispatchID:         acq.DispatchID,
		SupervisorID:       dctx.Args.SupervisorID,
		StoreRegistry:      dctx.Args.StoreRegistry,
		FrameID:            acq.FrameID,
		AcquiredLocks:      acq.Locks,
		NodeType:           acq.NodeType,
		Executor:           acq.Executor,
		NodeDef:            acq.NodeDef,
		ResolvedAttributes: dctx.Attributes,
		AttributesSchema:   dctx.AttributesSchema,
	})
}

// readExecutorStream consumes the executor's gRPC stream up to the
// terminal event.
//
// Non-terminal NamedEvent records are accumulated on the returned
// terminalEvent's NamedEvents slice; the H1 terminal-handler entry
// point persists them via NodeEventTable before applying the terminal
// verdict.
func readExecutorStream(
	ctx context.Context, dctx dispatchContext, stream interface {
		Recv() (*genv1.ExecuteEvent, error)
	},
) (terminalEvent, string) {
	args := dctx.Args
	acq := dctx.Acquired
	var pending []namedEventRecord
	for {
		ev, rerr := stream.Recv()
		if rerr == io.EOF {
			return terminalEvent{
				Kind:        terminalKindInfra,
				ErrorClass:  "stream_closed_without_terminal",
				NamedEvents: pending,
			}, ""
		}
		if rerr != nil {
			return terminalEvent{
				Kind:        terminalKindInfra,
				ErrorClass:  "stream_error",
				Payload:     map[string]any{"error": rerr.Error()},
				NamedEvents: pending,
			}, ""
		}
		switch e := ev.Event.(type) {
		case *genv1.ExecuteEvent_Heartbeat:
			if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return args.Persist.Nodes().UpdateHeartbeat(ctx, acq.NodeID, acq.RunScopeID, args.Clock.Now(), args.SupervisorID, tx)
			}); err != nil && args.Logger != nil {
				args.Logger.Warn("runner_dispatch: update heartbeat failed",
					"node_id", acq.NodeID.String(),
					"error", err.Error())
			}
			_ = e
		case *genv1.ExecuteEvent_NamedEvent:
			pending = append(pending, namedEventRecord{
				Name:          e.NamedEvent.Name,
				PayloadInline: e.NamedEvent.Payload,
			})
		case *genv1.ExecuteEvent_StreamClose:
			sc := e.StreamClose
			switch oc := sc.Outcome.(type) {
			case *genv1.StreamClose_Success:
				t := terminalEvent{
					Kind:          terminalKindComplete,
					Changed:       oc.Success.Changed,
					ChangeSummary: oc.Success.ChangeSummary,
					NamedEvents:   pending,
				}
				if oc.Success.AttributesDelta != nil {
					t.AttributesDel = oc.Success.AttributesDelta.AsMap()
				}
				return t, ""
			case *genv1.StreamClose_Error:
				var payloadGo any
				if oc.Error.Payload != nil {
					payloadGo = oc.Error.Payload.AsMap()
				}
				return terminalEvent{
					Kind:        terminalKindErrored,
					ErrorClass:  oc.Error.ErrorClass,
					Payload:     map[string]any{"payload": payloadGo},
					NamedEvents: pending,
				}, ""
			case *genv1.StreamClose_Park:
				t := terminalEvent{
					Kind:             terminalKindPark,
					ParkReason:       oc.Park.Reason,
					ParkReasonNote:   oc.Park.ReasonNote,
					ParkReasonLabel:  oc.Park.ReasonLabel,
					ParkPayload:      oc.Park.Payload,
					ParkSessionToken: oc.Park.SessionToken,
					NamedEvents:      pending,
				}
				if oc.Park.ResumeAt != nil {
					t.ParkResumeAt = oc.Park.ResumeAt.AsTime()
				}
				return t, ""
			case *genv1.StreamClose_AwaitAsync:
				// AwaitAsyncCallback does not carry NamedEvents — the
				// executor will POST them via the async-callback body's
				// `events` array. The runtime drops `pending` here
				// because the async-callback path handles the entire
				// session's events on its own.
				_ = pending
				return terminalEvent{Kind: terminalKindAsyncAccepted}, oc.AwaitAsync.AsyncAckId
			}
		}
	}
}

// resolveAttributes is the runner's pre-dispatch substitution + validation
// pass. Returns the populated attribute object and the schema (so
// the terminal handler can re-validate at commit time).
//
// Under the 2026-05-21 userdata collapse, the schema returned is the
// merged effective schema (executor.expected_attributes_schema ∪ L1
// template defaults ∪ L2 per-node declaration) — the same shape the
// validator computed at registration. The resolved attribute bag
// carries source-resolved values, static-default values, and post-
// merge L3 + L4 instance overrides.
func resolveAttributes(ctx context.Context, args RunArgs, acq *acquisition) (map[string]any, map[string]any, error) {
	if acq.NodeDef == nil || acq.NodeDef.Attributes == nil {
		return map[string]any{}, nil, nil
	}
	schema, execSchema, execSchemaVisible := computeEffectiveAttributeSchema(args, acq)
	if schema == nil {
		return map[string]any{}, nil, nil
	}
	// Reapply the unified-attribute-surface check at dispatch. The
	// registration-time validator soft-fails the readOnly leg when the
	// discovery cache hasn't populated the executor's expected schema
	// yet (e.g. test fixtures with no observability hook wired, or
	// templates registered before an executor's first handshake). By
	// dispatch time the executor MUST be reachable and have handshaked,
	// so a missing schema here is a real problem — fail loud with
	// `executor_schema_unavailable` rather than silently skipping the
	// readOnly leg. This is the authoritative gate per spec
	// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
	// §"Attribute as the unified surface".
	//
	// The executor name being non-empty is the precondition for needing
	// schema visibility — nodes that don't reference an executor (e.g.
	// pure deterministic templates in tests) don't go through expected-
	// schema gating.
	if acq.Executor != "" && !execSchemaVisible {
		return nil, schema, &executorSchemaUnavailableError{Executor: acq.Executor}
	}
	if errs := node.CheckEffectiveAttributesSchema(
		schema,
		acq.NodeDef.Attributes.Schema,
		extractReadOnlyPropsLocal(execSchema),
		execSchemaVisible,
		execSchemaVisible && node.IsPermissiveExecutorSchema(execSchema),
	); len(errs) > 0 {
		first := errs[0]
		return nil, schema, &attributeValidationError{
			Reason: fmt.Sprintf("attributes_schema_invalid: %s: %s", first.Path, first.Msg),
		}
	}
	rctx, err := buildResolveContextForDispatch(ctx, args, acq)
	if err != nil {
		return nil, schema, err
	}
	resolved, err := substituteAttributesSchema(schema, rctx)
	if err != nil {
		return nil, schema, err
	}
	scope := resolveAcqScope(ctx, args, acq)
	merged, matched := applyAttributeOverrides(
		resolved,
		acq.InstanceAttributeOverrides,
		acq.Executor,
		acq.NodeType,
		acq.GraphName,
		scope.PartitionKey,
		args.Logger,
	)
	resolved = merged
	acq.MergedAttributes = resolved
	incrementMatchCountersAfterMerge(ctx, args.Persist, args.Logger, acq.InstanceID, matched)

	// Breakpoint checkpoint: before_dispatch. Runs OUTSIDE any
	// acquisition / dispatch tx (incrementMatchCountersAfterMerge
	// committed its own short tx above; the acquisition tx committed
	// earlier per concept:supervisor invariants). EvaluateBreakpoints
	// opens its own short txns; pause-mode hits block on waitForResume
	// which polls on short txns. May return a different `resolved` map
	// if a one-shot L6 overlay was supplied at resume time — both
	// subsequent validation passes (dispatch-schema check below + the
	// executor-raw-schema defense-in-depth) see the overlay-mutated bag,
	// so an invalid overlay surfaces via the existing
	// `template_validation_failed` route per concept:error-policy.
	//
	// Infrastructure failures (DB blip during ListForInstance / Create /
	// poll, or context cancellation during the overflow-block / resume-poll
	// wait; wrapped as *BreakpointInfraError) are Warn-logged and
	// swallowed here. A debugger-side persistence failure must NOT
	// route through the attribute-failure policy chain — that would
	// surface as `template_resolution_failed` to operators, which is
	// the wrong diagnostic class. The dispatch proceeds with the
	// pre-breakpoint resolved bag.
	bpResolved, bpErr := EvaluateBreakpoints(ctx, args, CheckpointContext{
		InstanceID:       acq.InstanceID,
		DispatchID:       acq.DispatchID,
		FrameID:          acq.FrameID,
		Executor:         acq.Executor,
		NodeType:         acq.NodeType,
		Graph:            acq.GraphName,
		ChildKey:         scope.PartitionKey,
		MergedAttributes: resolved,
		Checkpoint:       persistence.CheckpointBeforeDispatch,
		EffectiveSchema:  schema,
		NodeRunSnapshot:  nodeRunSnapshotForBreakpoint(acq),
		HeldClaims:       heldClaimsSummaryForBreakpoint(acq),
		OpenWaitSet:      openWaitSetSummaryForBreakpoint(ctx, args, acq),
	})
	if bpErr != nil {
		var infraErr *BreakpointInfraError
		if errors.As(bpErr, &infraErr) {
			if args.Logger != nil {
				args.Logger.Warn("runner_dispatch: breakpoint infra failure; dispatching with pre-breakpoint bag",
					"node_id", acq.NodeID.String(),
					"dispatch_id", acq.DispatchID.String(),
					"phase", infraErr.Phase,
					"error", bpErr.Error())
			}
		} else {
			return nil, schema, bpErr
		}
	} else {
		resolved = bpResolved
		acq.MergedAttributes = resolved
	}

	dispatchSchema := relaxRequiredToSourceDriven(schema)
	if err := attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch); err != nil {
		return nil, schema, &attributeValidationError{Reason: "dispatch_bag_invalid", Cause: err}
	}
	// Defense-in-depth: re-validate the merged bag against the executor's
	// raw schema. The dispatch-relaxed `dispatchSchema` above tolerates
	// executor-written `required:` properties (commit gate handles them);
	// the executor's raw schema does not have that relaxation. This pass
	// catches L3 / L4 override values that violate the executor's
	// contract (shape-blind at instance creation per the structural-
	// inertness rule) and any source-resolved value whose runtime type
	// doesn't match what the executor declared. Per the 2026-05-21 gap-
	// closure cycle (spec §"Effective schema computation").
	if execSchema != nil {
		// The executor's raw schema may carry `required:` entries for
		// `readOnly: true` properties (executor-written outputs) — those
		// are populated at commit by write-back, not at dispatch, so
		// enforcing `required:` for them here would fire false positives.
		// Per the userdata-collapse spec §"Effective schema computation":
		// executor-written `required:` is enforced at the commit gate,
		// not at dispatch. Source-bound and static-default `required:`
		// entries stay enforced (they should already be in the dispatch
		// bag).
		execSchemaForDispatch := relaxRequiredForExecutorWritten(execSchema)
		if err := attributes.Validate(execSchemaForDispatch, resolved, attributes.PhaseDispatch); err != nil {
			return nil, schema, &attributeValidationError{
				Reason: "dispatch_bag_violates_executor_schema",
				Cause:  err,
			}
		}
	}
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "attributes_substituted",
			Payload: map[string]any{
				"substituted_fields": fieldNames(resolved),
			},
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("runner_dispatch: append attributes_substituted event failed",
			"node_id", acq.NodeID.String(),
			"error", err.Error())
	}
	return resolved, schema, nil
}

// buildResolveContextForDispatch assembles the substitution context
// from the candidate's deps, this acquisition's claims (keyed by
// alias), and instance params (marshalled to RawMessage so the
// substitution engine can lazy-walk into nested params).
//
// The EventLookup callback resolves
// `nodes.<emitter>.event.<name>.<json_path>` source kinds (plan F4):
// (a) it maps the emitter node-type to a node-id within the same
// instance via Nodes().ListByInstance, (b) reads the most recent
// emission row from rimsky_node_events, (c) materializes the spilled
// payload via BlobBackend if necessary, (d) returns the bytes for
// walkPath. Empty bytes / no row → ok=false.
func buildResolveContextForDispatch(
	ctx context.Context, args RunArgs, acq *acquisition,
) (attributes.ResolveContext, error) {
	deps := loadSubscribedNodeAttributesByID(ctx, args, acq)
	claims := map[string]locks.ClaimResult{}
	for _, lk := range acq.Locks {
		if lk.Alias == "" {
			continue
		}
		claims[lk.Alias] = lk.ClaimResult
	}
	var paramsRaw json.RawMessage
	if len(acq.InstanceParams) > 0 {
		b, err := json.Marshal(acq.InstanceParams)
		if err != nil {
			return attributes.ResolveContext{}, err
		}
		paramsRaw = b
	}
	eventLookup := func(emitter, eventName string) (json.RawMessage, bool) {
		return lookupEventPayload(ctx, args, acq.InstanceID, emitter, eventName)
	}
	return attributes.ResolveContext{
		Deps:        deps,
		Claim:       claims,
		Params:      paramsRaw,
		EventLookup: eventLookup,
	}, nil
}

// lookupEventPayload resolves the most recent NamedEvent emission for
// the (instance, emitter-type, event-name) tuple. Returns ok=false when
// no row exists or the materialization fails.
func lookupEventPayload(
	ctx context.Context, args RunArgs, instanceID shared.UUID, emitterType, eventName string,
) (json.RawMessage, bool) {
	var out json.RawMessage
	var found bool
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		// Map emitter node-type → emitter node-id within the instance.
		rows, err := args.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
		if err != nil {
			return nil
		}
		var emitterID string
		for _, r := range rows {
			if r.NodeType == emitterType {
				emitterID = r.ID.String()
				break
			}
		}
		if emitterID == "" {
			return nil
		}
		evt, err := args.Persist.NodeEvents().LatestByName(ctx, instanceID.String(), emitterID, eventName, tx)
		if err != nil || evt == nil {
			return nil
		}
		// Inline payload wins; otherwise materialize the handle through
		// the configured BlobBackend.
		if len(evt.PayloadInline) > 0 {
			out = json.RawMessage(evt.PayloadInline)
			found = true
			return nil
		}
		if evt.PayloadHandle == "" {
			return nil
		}
		switch {
		case args.Blob == nil:
			args.Logger.Warn("namedEventPayload: spilled payload but no BlobBackend configured; substitution will see empty",
				"event_name", eventName,
				"emitter_node_id", emitterID,
				"handle_backend", evt.PayloadHandleBackend)
		case args.Blob.Name() != evt.PayloadHandleBackend:
			args.Logger.Warn("namedEventPayload: blob backend mismatch; substitution will see empty",
				"event_name", eventName,
				"emitter_node_id", emitterID,
				"current_backend", args.Blob.Name(),
				"handle_backend", evt.PayloadHandleBackend)
		default:
			b, ferr := args.Blob.Read(ctx, persistence.Handle(evt.PayloadHandle))
			if ferr == nil {
				out = json.RawMessage(b)
				found = true
				return nil
			}
			args.Logger.Warn("namedEventPayload: blob fetch failed; substitution will see empty",
				"event_name", eventName,
				"emitter_node_id", emitterID,
				"error", ferr.Error())
		}
		return nil
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("lookupEventPayload: tx failed; substitution will see empty",
			"event_name", eventName,
			"emitter_type", emitterType,
			"instance_id", instanceID.String(),
			"error", err.Error())
	}
	return out, found
}

// computeEffectiveAttributeSchema returns the per-node effective
// attribute schema at dispatch — the same shape the validator computed
// at registration:
//
//	executor.expected_attributes_schema ∪ L1 template defaults ∪ L2 node schema
//
// Returns the effective schema plus the executor's expected schema
// (parsed) and a visibility flag reporting whether the discovery hook
// returned schema bytes. The dispatch path uses the visibility flag +
// parsed executor schema to reapply
// `node.CheckEffectiveAttributesSchema` against the merged effective
// schema — closing the gap left by the registration-time validator's
// soft-fail when the discovery cache isn't populated yet.
//
// The recompute happens at dispatch (per spec open-question 1,
// "recompute rather than persist") so template-registration storage
// stays unchanged. The merge is pure
// (graph/node::MergeAttributeDefaults); shape-blind on the L1 + L4
// value fragments.
//
// @concept: attribute
func computeEffectiveAttributeSchema(args RunArgs, acq *acquisition) (map[string]any, map[string]any, bool) {
	var nodeSchema map[string]any
	if acq.NodeDef != nil && acq.NodeDef.Attributes != nil {
		nodeSchema = acq.NodeDef.Attributes.Schema
	}
	var execSchema map[string]any
	execSchemaVisible := false
	if args.ExpectedAttributesSchemaFor != nil && acq.Executor != "" {
		if bytesIn, ok := args.ExpectedAttributesSchemaFor(acq.Executor); ok && len(bytesIn) > 0 {
			if err := json.Unmarshal(bytesIn, &execSchema); err != nil {
				if args.Logger != nil {
					args.Logger.Warn("computeEffectiveAttributeSchema: executor schema unmarshal failed",
						"executor", acq.Executor, "error", err.Error())
				}
				execSchema = nil
			} else {
				execSchemaVisible = true
			}
		}
	}
	if execSchema == nil && nodeSchema == nil {
		return nil, nil, execSchemaVisible
	}
	return node.MergeAttributeDefaults(execSchema, acq.TemplateAttributeDefaults, nodeSchema), execSchema, execSchemaVisible
}

// extractReadOnlyPropsLocal mirrors graph/node/template_validator.go::
// extractReadOnlyProps. Kept private because the runtime only needs the
// names-of-readOnly-props set when reapplying the unified-attribute-
// surface check. @source: graph/node/template_validator.go:extractReadOnlyProps
func extractReadOnlyPropsLocal(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	if schema == nil {
		return out
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return out
	}
	for name, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if ro, _ := prop["readOnly"].(bool); ro {
			out[name] = true
		}
	}
	return out
}

// substituteAttributesSchema walks the effective schema's `properties`
// map and emits one entry per property:
//   - source-bound (`source:` directive): resolved against rctx via the
//     substitution engine. Strict directives (no marker) raise
//     ErrMissingSource on missing; lenient (`?` marker) and fallback
//     (`| <literal>`) directives are handled inside the engine and
//     produce a typed value or null.
//   - static-default (`default:` value with no source): copied verbatim
//     from the schema. No substitution is applied to defaults.
//   - executor-written (`readOnly: true` in the executor's expected
//     schema; the effective schema carries the marker through): absent
//     from the dispatch-time bag, populated at commit by write-back.
//
// Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Resolution waterfall".
//
// @concept: attribute
func substituteAttributesSchema(schema map[string]any, rctx attributes.ResolveContext) (map[string]any, error) {
	out := map[string]any{}
	if schema == nil {
		return out, nil
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return out, nil
	}
	for name, propAny := range props {
		prop, _ := propAny.(map[string]any)
		if prop == nil {
			continue
		}
		srcRaw, hasSource := prop["source"]
		defaultVal, hasDefault := prop["default"]
		switch {
		case hasSource:
			source, _ := srcRaw.(string)
			if source == "" {
				continue
			}
			val, err := attributes.SubstituteValue(source, rctx)
			if err != nil {
				// Per spec §"Resolution waterfall" step 5: a strict (no
				// `?` marker) missing directive fails dispatch with
				// `template_resolution_failed`, regardless of whether
				// the property is `required`. Lenient (`?`) and
				// fallback (`| literal`) directives are handled inside
				// SubstituteValue and never raise ErrMissingSource.
				return nil, err
			}
			out[name] = val
		case hasDefault:
			// Static-default property — the default value flows into the
			// dispatch bag verbatim. No substitution is applied to
			// defaults; an operator-supplied `"{{X}}"` is a literal
			// string here.
			out[name] = defaultVal
		}
		// Executor-written (readOnly + no source + no default) properties
		// stay absent until the executor's commit write-back populates
		// them; nothing to do at dispatch.
	}
	return out, nil
}

// relaxRequiredToSourceDriven returns a shallow copy of the supplied
// JSON Schema whose `required` array is filtered to drop only
// properties that have neither a `source:` directive nor a `default:`
// value (i.e. executor-written properties). Source-bound and
// static-default properties stay in `required` — the dispatch bag
// will already contain a value for them (resolved or static), and the
// JSON Schema validation step on the dispatch bag should still see
// them as required. Executor-populated requireds get re-validated at
// commit (@blessed-invariant 12).
func relaxRequiredToSourceDriven(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	if len(props) == 0 || len(required) == 0 {
		return schema
	}
	keep := make([]any, 0, len(required))
	dropped := false
	for _, item := range required {
		name, ok := item.(string)
		if !ok {
			keep = append(keep, item)
			continue
		}
		prop, _ := props[name].(map[string]any)
		src, _ := prop["source"].(string)
		_, hasDefault := prop["default"]
		if src == "" && !hasDefault {
			dropped = true
			continue
		}
		keep = append(keep, item)
	}
	if !dropped {
		return schema
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		out[k] = v
	}
	out["required"] = keep
	return out
}

// relaxRequiredForExecutorWritten returns a shallow copy of the
// supplied JSON Schema whose `required` array drops only the
// `readOnly: true` properties (executor-written outputs). Source-bound
// and static-default `required:` entries stay — those properties
// already exist in the dispatch bag, so requiring them there is
// correct. Executor-written `required:` entries are enforced at the
// commit gate when the executor's write-back lands, not at dispatch.
//
// Sibling to `relaxRequiredToSourceDriven`: that helper builds the
// dispatch-relaxed view of the *effective* schema (the executor ∪ L1
// ∪ L2 composition) for the primary dispatch validation pass; this
// helper builds the equivalent view of the executor's *raw* schema
// for the defense-in-depth pass against L3 / L4 overrides. The two
// relaxations differ in detection criterion: the effective-schema
// view classifies via `source:` / `default:` (which only the effective
// schema carries); the raw-executor view classifies via `readOnly:
// true` (which only the executor schema carries).
//
// Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Effective schema computation".
func relaxRequiredForExecutorWritten(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	required, _ := schema["required"].([]any)
	if len(required) == 0 {
		return schema
	}
	props, _ := schema["properties"].(map[string]any)
	keep := make([]any, 0, len(required))
	dropped := false
	for _, item := range required {
		name, ok := item.(string)
		if !ok {
			keep = append(keep, item)
			continue
		}
		prop, _ := props[name].(map[string]any)
		if prop != nil {
			if ro, _ := prop["readOnly"].(bool); ro {
				dropped = true
				continue
			}
		}
		keep = append(keep, item)
	}
	if !dropped {
		return schema
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		out[k] = v
	}
	if len(keep) == 0 {
		delete(out, "required")
	} else {
		out["required"] = keep
	}
	return out
}

func fieldNames(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// loadSubscribedNodeAttributesByID is the per-dispatch subscribed-node
// attribute map. Runs between the acquisition tx and the dispatch tx,
// so it opens its own short-lived read tx — every Store method requires
// an explicit tx (option C / no-nil-tx).
//
// Under per-run keying (2026-05-20) this calls BuildAttributeDeps which
// reads the drained wait-set rows for this receiver in this frame. The
// builder is the only substitution-context source — no scope-walk, no
// cross-frame caching.
//
//	@concept: node-subscription
//	@concept: attribute
func loadSubscribedNodeAttributesByID(ctx context.Context, args RunArgs, acq *acquisition) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		deps, err := BuildAttributeDeps(ctx, tx, args, acq.DispatchID, acq.FrameID)
		if err != nil {
			return err
		}
		out = deps
		return nil
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("loadSubscribedNodeAttributesByID: tx failed",
			"run_id", acq.DispatchID.String(),
			"error", err.Error())
	}
	return out
}

// buildExecuteRequest assembles the gRPC ExecuteRequest payload. Under
// the 2026-05-21 userdata collapse, `attributes` is the unified surface
// for both rimsky-resolved inputs and template-author static defaults;
// the L3/L4 merge already happened in `resolveAttributes`. The wire no
// longer carries a separate `userdata` field.
// priorDispositionFromStorageForm maps the lower_snake_case storage
// form persisted on col:rimsky_node_runs.prior_dispatch_disposition
// back to the proto PriorDispatchDisposition enum. Unknown values
// (including empty, which is the wire default) resolve to
// PRIOR_NONE.
//
// @concept: run-scope
func priorDispositionFromStorageForm(s string) genv1.PriorDispatchDisposition {
	switch s {
	case "heartbeat_stale":
		return genv1.PriorDispatchDisposition_PRIOR_HEARTBEAT_STALE
	case "retry_after_error":
		return genv1.PriorDispatchDisposition_PRIOR_RETRY_AFTER_ERROR
	case "recalculate":
		return genv1.PriorDispatchDisposition_PRIOR_RECALCULATE
	}
	return genv1.PriorDispatchDisposition_PRIOR_NONE
}

func buildExecuteRequest(ctx context.Context, dctx dispatchContext) (*genv1.ExecuteRequest, error) {
	acq := dctx.Acquired
	attrStruct := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if len(dctx.Attributes) > 0 {
		s, err := structpb.NewStruct(dctx.Attributes)
		if err != nil {
			dctx.Args.Logger.Warn("buildExecuteRequest: structpb.NewStruct failed for attributes",
				"node_id", acq.NodeID.String(),
				"error", err.Error())
		} else {
			attrStruct = s
		}
	}
	schemaStruct := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if len(dctx.AttributesSchema) > 0 {
		s, err := structpb.NewStruct(dctx.AttributesSchema)
		if err != nil {
			dctx.Args.Logger.Warn("buildExecuteRequest: structpb.NewStruct failed for attributes_schema",
				"node_id", acq.NodeID.String(),
				"error", err.Error())
		} else {
			schemaStruct = s
		}
	}

	stores, err := buildStoreHandles(acq)
	if err != nil {
		return nil, err
	}

	cancelToken := dctx.Args.SupervisorID + ":" + acq.DispatchID.String()
	req := &genv1.ExecuteRequest{
		NodeId:           acq.NodeID.String(),
		InstanceId:       acq.InstanceID.String(),
		NodeType:         acq.NodeType,
		Attributes:       attrStruct,
		AttributesSchema: schemaStruct,
		Stores:           stores,
		CallbackUrl:      dctx.Args.CallbackURL,
		CancelToken:      cancelToken,
		DispatchId:       acq.DispatchID.String(),
	}
	// Recovery-aware fields: surface the predecessor dispatch identity +
	// classifier when this run supersedes a prior dispatch (heartbeat
	// stale recovery, retry-after-error, recalculate). Both unset on
	// initial dispatches. Per spec
	// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
	// §Recovery-aware executor protocol.
	if acq.PriorDispatchID != nil {
		pid := acq.PriorDispatchID.String()
		req.PriorDispatchId = &pid
	}
	if acq.PriorDispatchDisposition != "" {
		disposition := priorDispositionFromStorageForm(acq.PriorDispatchDisposition)
		req.PriorDispatchDisposition = &disposition
	}
	// Resume dispatch: when this acquisition is resuming a parked node,
	// attach the persisted park metadata as ResumeContext so the
	// executor can re-establish session state. Per plan E4.
	//
	// Note: the clear-of-parked-metadata is deferred to the dispatch
	// caller (after the executor stream produces a terminal). If
	// buildExecuteRequest cleared here and the executor RPC then failed
	// (dial / serialization), the row would be re-enqueued via
	// applyTerminalInfraError but the next pickup would be a fresh
	// dispatch — the executor would never see the parked payload it
	// was resuming. Keeping the clear in the success branch preserves
	// the metadata across pre-RPC failures so the retry is still a
	// resume.
	if acq.Resume != nil {
		req.ResumeContext = &genv1.ResumeContext{
			Payload:      acq.Resume.Payload,
			SessionToken: acq.Resume.SessionToken,
			ResumeReason: string(acq.Resume.Reason),
		}
	}
	return req, nil
}

// buildStoreHandles converts each ClaimSpec acquisition into a per-
// store StoreHandle proto entry, then layers any co-held claims
// (`holds:` / legacy `inherits:`) on top under their local alias. The
// handle's `handle` struct carries the store-supplied address bytes
// verbatim under the "address" key — opaque to Rimsky per
// `@blessed-invariant 20`. The leaf executor cannot tell from
// `ExecuteRequest` whether a given claim was acquired (`claims:`) or
// co-held (`holds:`) — same wire shape per spec §Claim co-holdership.
//
// @concept: claim-co-holdership
func buildStoreHandles(acq *acquisition) (map[string]*genv1.StoreHandle, error) {
	out := make(map[string]*genv1.StoreHandle, len(acq.Locks)+len(acq.HeldClaims))
	for _, lk := range acq.Locks {
		spec, ok := lk.Spec.(locks.ClaimSpec)
		if !ok {
			continue
		}
		h, err := makeClaimHandle(lk, spec)
		if err != nil {
			return nil, err
		}
		// Key by Alias, not ProducerName: two store entries within a node may
		// share the same producer name but must have distinct aliases (e.g. a
		// consolidate node holding both `@consolidate-queue` aliased `doc`
		// and `@guidance-root` aliased `root`, both on the same `content`
		// producer). Keying by ProducerName would let the second overwrite the
		// first. Alias is also the lookup key the executor uses for
		// `cwd_from_store: <alias>` and `claim.<alias>.scope` attribute
		// substitution, so the wire shape now matches what consumers need.
		key := spec.Alias
		if key == "" {
			key = spec.ProducerName
		}
		out[key] = h
	}
	for alias, claim := range acq.HeldClaims {
		if _, alreadyPresent := out[alias]; alreadyPresent {
			// `claims:` ALREADY bound this alias for this run; the held
			// entry is informational only.
			continue
		}
		h, err := makeHeldClaimHandle(alias, claim)
		if err != nil {
			return nil, err
		}
		out[alias] = h
	}
	return out, nil
}

// makeHeldClaimHandle builds a `StoreHandle` proto entry for a co-held
// claim (the upstream's address presented under this run's local alias).
// Mirrors `makeClaimHandle` for the bytes-shape contract, but draws the
// address + payload from the upstream `ClaimResult` rather than this
// run's own `AcquiredLock`. `Intent` is unset (the co-holder does not
// own an intent against the producer); the executor reads only the
// `address` field for held claims.
//
// @blessed-invariant 20 (wire-encoding site exception): the same JSON
// round-trip discipline as `makeClaimHandle`.
func makeHeldClaimHandle(alias string, claim locks.ClaimResult) (*genv1.StoreHandle, error) {
	out := &genv1.StoreHandle{}
	fields := map[string]any{"alias": alias}
	if len(claim.Address) > 0 {
		var addrAny any
		if err := json.Unmarshal(claim.Address, &addrAny); err != nil {
			return nil, fmt.Errorf("makeHeldClaimHandle: claim address bytes not JSON-decodable: %w", err)
		}
		fields["address"] = addrAny
	}
	if len(claim.Payload) > 0 {
		var payloadAny any
		if err := json.Unmarshal(claim.Payload, &payloadAny); err != nil {
			return nil, fmt.Errorf("makeHeldClaimHandle: claim payload bytes not JSON-decodable: %w", err)
		}
		fields["payload"] = payloadAny
	}
	s, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, fmt.Errorf("makeHeldClaimHandle: structpb: %w", err)
	}
	out.Handle = s
	return out, nil
}

// makeClaimHandle builds the per-store proto entry. The handle
// payload is `{"address": <opaque shape>, "payload": <opaque shape>,
// "alias": ..., "intent": ...}`; the executor unwraps it per its
// store knowledge.
//
// @blessed-invariant 20 (wire-encoding site exception): this function
// decodes the store-supplied Address and Payload bytes via
// json.Unmarshal solely to project them into a `google.protobuf.Struct`
// for the wire. This is the SOLE sanctioned wire-encoding site outside
// `graph/attribute/substitution.go::walkPath` (which is the sole
// sanctioned substitution-leaf extraction site). No transformation,
// logging, normalization, validation, or pattern-matching happens
// here — the bytes round-trip through structpb and are reconstituted
// verbatim on the executor side. If the spec moves the
// `StoreHandle.handle` field to `bytes`, this function shrinks to a
// straight byte-copy and the wire-encoding site disappears entirely.
func makeClaimHandle(lk AcquiredLock, spec locks.ClaimSpec) (*genv1.StoreHandle, error) {
	out := &genv1.StoreHandle{}
	if lk.Producer != nil {
		// The wire StoreHandle.kind field is informational only and
		// the executor knows its store's kind from the
		// deployment's operator config. Pass the
		// operator-chosen store name as the closest analogue; the
		// v2 Store.Kind() method is gone in v3.
		out.Kind = lk.Producer.Name()
	}
	fields := map[string]any{}
	if len(lk.ClaimResult.Address) > 0 {
		var addrAny any
		if err := json.Unmarshal(lk.ClaimResult.Address, &addrAny); err != nil {
			// Producer-supplied bytes did not round-trip as JSON. Per
			// @blessed-invariant 20 we MUST NOT mangle, log, or
			// transform the bytes — refuse to dispatch instead so the
			// failure is visible at the supervisor (rather than letting
			// a non-UTF-8 byte sequence travel through structpb as a
			// silently-corrupted Go string field downstream).
			return nil, fmt.Errorf("makeClaimHandle: claim address bytes are not JSON-decodable (producer invariant); refusing to dispatch: %w", err)
		}
		fields["address"] = addrAny
	}
	if len(lk.ClaimResult.Payload) > 0 {
		var payloadAny any
		if err := json.Unmarshal(lk.ClaimResult.Payload, &payloadAny); err != nil {
			return nil, fmt.Errorf("makeClaimHandle: claim payload bytes are not JSON-decodable (producer invariant); refusing to dispatch: %w", err)
		}
		fields["payload"] = payloadAny
	}
	fields["alias"] = spec.Alias
	fields["intent"] = string(spec.Intent)
	s, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, fmt.Errorf("makeClaimHandle: structpb: %w", err)
	}
	out.Handle = s
	return out, nil
}
