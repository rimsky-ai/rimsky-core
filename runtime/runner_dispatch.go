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

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	attributes "github.com/fallguy/rimsky/graph/attribute"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// userdataValidationError wraps a UserdataValidator failure so the
// dispatch path can classify it as an application-error
// ("userdata_validation_failed") rather than infra. Plan F7.
type userdataValidationError struct {
	cause error
}

func (e *userdataValidationError) Error() string { return e.cause.Error() }
func (e *userdataValidationError) Unwrap() error { return e.cause }

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
	ParkReasonLabel  string // freeform label required when ParkReason == PARK_REASON_OTHER (spec E12)
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

	ep, ok := args.Resolver.Resolve(acq.Executor)
	if !ok {
		_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "unresolved_executor",
				Payload: map[string]any{
					"executor_name": acq.Executor,
					"supervisor_id": args.SupervisorID,
				},
			}, tx)
		})
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
		// Plan F7: userdata-validation failures route through the
		// application-Error chain (Error{error_class} →
		// on_executor_errored) rather than the infra-error chain. This
		// lets templates declare an on_executor_errored handler with a
		// `userdata_validation_failed` branch and react (e.g.
		// invalidate the upstream attribute-producing node, or
		// give_up).
		var uerr *userdataValidationError
		if errors.As(err, &uerr) {
			return terminalEvent{
				Kind:       terminalKindErrored,
				ErrorClass: "userdata_validation_failed",
				Payload:    map[string]any{"error": uerr.cause.Error()},
			}, nil, nil
		}
		return terminalEvent{Kind: terminalKindInfra, ErrorClass: "build_request_failed",
			Payload: map[string]any{"error": err.Error()}}, nil, nil
	}
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
		_ = dctx.Args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return dctx.Args.Queue.ClearResumeMetadataInTx(ctx, tx, dctx.Acquired.DispatchID)
		})
	}
	terminal, asyncAck := readExecutorStream(ctx, dctx, stream)
	_ = stream.Close()

	if asyncAck != "" {
		registerAsyncIfSet(dctx, asyncAck)
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
			_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return args.Persist.Nodes().UpdateHeartbeat(ctx, acq.NodeID, args.Clock.Now(), args.SupervisorID, tx)
			})
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
func resolveAttributes(ctx context.Context, args RunArgs, acq *acquisition) (map[string]any, map[string]any, error) {
	if acq.NodeDef == nil || acq.NodeDef.Attributes == nil {
		return map[string]any{}, nil, nil
	}
	schema := acq.NodeDef.Attributes.Schema
	if schema == nil {
		return map[string]any{}, nil, nil
	}
	rctx, err := buildResolveContextForDispatch(ctx, args, acq)
	if err != nil {
		return nil, schema, err
	}
	resolved, err := substituteAttributesSchema(schema, rctx)
	if err != nil {
		return nil, schema, err
	}
	dispatchSchema := relaxRequiredToSourceDriven(schema)
	if err := attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch); err != nil {
		return nil, schema, err
	}
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "attributes_substituted",
			Payload: map[string]any{
				"substituted_fields": fieldNames(resolved),
			},
		}, tx)
	})
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
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
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
	})
	return out, found
}

// substituteAttributesSchema walks the schema's `properties` map and
// substitutes any property with a `source:` string into the output.
func substituteAttributesSchema(schema map[string]any, rctx attributes.ResolveContext) (map[string]any, error) {
	out := map[string]any{}
	if schema == nil {
		return out, nil
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return out, nil
	}
	required := stringSetFrom(schema["required"])
	for name, propAny := range props {
		prop, _ := propAny.(map[string]any)
		if prop == nil {
			continue
		}
		srcRaw, hasSource := prop["source"]
		if !hasSource {
			continue
		}
		source, _ := srcRaw.(string)
		if source == "" {
			continue
		}
		val, err := attributes.Substitute(source, rctx)
		if err != nil {
			if attributes.IsMissingSource(err) {
				if _, isReq := required[name]; isReq {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		out[name] = val
	}
	return out, nil
}

// relaxRequiredToSourceDriven returns a shallow copy of the supplied
// JSON Schema whose `required` array is filtered to retain only
// properties carrying a non-empty `source:` directive. Executor-
// populated requireds get re-validated at commit (@blessed-invariant 12).
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
		if src == "" {
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

func stringSetFrom(v any) map[string]struct{} {
	out := map[string]struct{}{}
	arr, ok := v.([]any)
	if !ok {
		return out
	}
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out[s] = struct{}{}
		}
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
// Post-T23: the subscribed-sender set is resolved from the per-template
// cached subscription-edge inverse map (see graph/node/subscription_edges.go
// + runtime/subscription_loaders.go) — no longer reads the retired
// nodes.dependencies column.
//
//	@concept: subscription
func loadSubscribedNodeAttributesByID(ctx context.Context, args RunArgs, acq *acquisition) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		senders, err := resolveSubscribedSenders(ctx, args, acq.NodeID, tx)
		if err != nil {
			return nil
		}
		out = loadSubscribedNodeAttributes(ctx, args, tx, senders)
		return nil
	})
	return out
}

// buildExecuteRequest assembles the gRPC ExecuteRequest payload.
func buildExecuteRequest(ctx context.Context, dctx dispatchContext) (*genv1.ExecuteRequest, error) {
	acq := dctx.Acquired
	def := acq.NodeDef

	// Build per-node userdata, then deep-merge per-instance overrides on
	// top in order of increasing specificity:
	//   template userdata → by_executor[node's executor] → by_node[node's name]
	// Per @blessed-invariant 11 the merge is shape-blind and rimsky never
	// inspects the resulting fragment values.
	var baseUserdata map[string]any
	if def != nil && len(def.Userdata) > 0 {
		baseUserdata = def.Userdata
	}
	merged := applyUserdataOverrides(baseUserdata, acq.InstanceUserdataOverrides, acq.Executor, acq.NodeType, dctx.Args.Logger)
	// Plan F7: dispatch-time userdata schema validation. Runs after
	// applyUserdataOverrides so per-instance overrides are validated
	// too. Failure routes through on_executor_errored (handled by the
	// caller via the returned error).
	if dctx.Args.UserdataValidator != nil && acq.Executor != "" {
		if err := dctx.Args.UserdataValidator(acq.Executor, merged); err != nil {
			return nil, &userdataValidationError{cause: err}
		}
	}
	userdataStruct := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if len(merged) > 0 {
		s, err := structpb.NewStruct(merged)
		if err != nil {
			// Steady-state this is unreachable: both layers feed the
			// merge through json.Unmarshal so values are restricted to
			// types structpb.NewStruct accepts. With per-instance
			// overrides now operator-influenced, surface a Warn so
			// "override silently dropped" leaves a trace rather than an
			// empty userdata payload at the executor.
			dctx.Args.Logger.Warn("buildExecuteRequest: structpb.NewStruct failed for userdata",
				"node_id", acq.NodeID.String(),
				"error", err.Error())
		} else {
			userdataStruct = s
		}
	}
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
	var prior *persistence.NodeAttributesRow
	_ = dctx.Args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := dctx.Args.Persist.NodeAttributes().Get(ctx, acq.NodeID, tx)
		prior = p
		return err
	})
	runAttempt := 1
	if prior != nil {
		runAttempt = prior.RunAttempt
	}
	req := &genv1.ExecuteRequest{
		NodeId:           acq.NodeID.String(),
		InstanceId:       acq.InstanceID.String(),
		NodeType:         acq.NodeType,
		Userdata:         userdataStruct,
		Attributes:       attrStruct,
		AttributesSchema: schemaStruct,
		Stores:           stores,
		CallbackUrl:      dctx.Args.CallbackURL,
		CancelToken:      cancelToken,
		RunAttempt:       int32(runAttempt),
		DispatchId:       acq.DispatchID.String(),
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
