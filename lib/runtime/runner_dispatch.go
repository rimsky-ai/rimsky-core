// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

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

type executorSchemaUnavailableError struct {
	Executor string
}

func (e *executorSchemaUnavailableError) Error() string {
	return fmt.Sprintf(
		"executor_schema_unavailable: executor %q has no visible expected_attributes_schema at dispatch (handshake not completed or discovery cache empty)",
		e.Executor,
	)
}

type dispatchContext struct {
	Args             RunArgs
	Acquired         *acquisition
	Attributes       map[string]any
	AttributesSchema map[string]any
	LivenessInterval time.Duration
	Log              shared.Logger
	RegisterAsync    func(ackID string, actx AsyncContext)
}

type terminalEvent struct {
	Kind          terminalKind
	Changed       bool
	ChangeSummary string
	// @concept: attribute
	AttributesDel map[string]any
	Tags       []string
	ErrorClass string
	Payload    map[string]any
	ParkReason     genv1.ParkReason
	ParkReasonNote string
	ParkReasonLabel string
	ParkResumeAt time.Time
	// @concept: executor
	Scratch []byte
}

type terminalKind int

const (
	terminalKindNone terminalKind = iota
	terminalKindComplete
	terminalKindErrored
	terminalKindAsyncAccepted
	terminalKindInfra
	terminalKindPark
)

func dispatch(ctx context.Context, dctx dispatchContext) (terminalEvent, *RunnerResult, error) {
	acq := dctx.Acquired
	args := dctx.Args
	metrics := metricsOf(args)
	dispatchStart := args.Clock.Now()
	defer func() {
		metrics.ObserveDispatchLatency(acq.Executor, args.Clock.Now().Sub(dispatchStart).Seconds())
	}()
	metrics.IncDispatch(acq.Executor, "started")

	if acq.NodeDef != nil && acq.NodeDef.EmitsMessage != "" {
		// @concept: message-emitter-node
		return terminalEvent{
			Kind:          terminalKindComplete,
			Changed:       true,
			ChangeSummary: "emits_message:" + acq.NodeDef.EmitsMessage,
		}, nil, nil
	}
	if acq.Executor == "" {
		summary := "pure_cascade"
		for _, lk := range acq.Locks {
			if _, ok := lk.Spec.(claimproducer.ClaimSpec); ok {
				summary = "claim_acquired"
				break
			}
		}
		return terminalEvent{Kind: terminalKindComplete, Changed: true, ChangeSummary: summary}, nil, nil
	}

	ep, ok := args.Resolver.Resolve(acq.Executor, executor.DispatchContext{
		Ctx:        ctx,
		InstanceID: acq.InstanceID.String(),
		RunScopeID: acq.RunScopeID.String(),
	})
	if !ok {
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: events.KindUnresolvedExecutor(),
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
	ctx = peer.WithServiceName(ctx, acq.Executor)
	deadline := resolveSyncRPCDeadline(acq.NodeDef, dctx.Args.SyncRPCDeadlineDefault)
	if deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}
	outcome, err := client.Execute(ctx, req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return terminalEvent{
				Kind:       terminalKindErrored,
				ErrorClass: "executor_sync_timeout",
				Payload: map[string]any{
					"deadline": deadline.String(),
					"error":    err.Error(),
				},
			}, nil, nil
		}
		return terminalEvent{Kind: terminalKindInfra, ErrorClass: "executor_dial_failed",
			Payload: map[string]any{"error": err.Error()}}, nil, nil
	}
	terminal, asyncAck := readExecutorOutcome(ctx, dctx, outcome)

	if asyncAck != "" {
		registerAsyncIfSet(dctx, asyncAck)
		// @concept: signal
		// @concept: async-callback-persistence
		// @concept: dispatch-deadlines
		if dctx.Args.Persist != nil && dctx.Args.Queue != nil {
			maxQuietSec, maxRuntimeSec := computeEffectiveDeadlineSecs(acq.NodeDef, dctx.Args.MaxQuietPeriodDefault, dctx.Args.MaxRuntimeDefault)
			awaitSig := signalpkg.Signal{
				Type: "transient/await_async",
				Payload: map[string]any{
					"async_ack_id": asyncAck,
					"callback_url": dctx.Args.CallbackURL,
				},
			}
			if err := dctx.Args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				if err := dctx.Args.Queue.RegisterAsyncAck(ctx, tx, acq.DispatchID, asyncAck, dctx.Args.Clock.Now(), maxQuietSec, maxRuntimeSec); err != nil {
					return fmt.Errorf("register async ack: %w", err)
				}
				return signalaudit.EmitSignal(ctx, dctx.Args.Persist.Events(),
					acq.InstanceID, acq.NodeID, awaitSig, dctx.Args.Clock.Now(), tx)
			}); err != nil && dctx.Args.Logger != nil {
				dctx.Args.Logger.Warn("runner_dispatch: persist async-callback registry failed; await_async signal not emitted, restart-survival not in effect for this ack",
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

func readExecutorOutcome(
	ctx context.Context, dctx dispatchContext, outcome *genv1.Outcome,
) (terminalEvent, string) {
	if outcome == nil {
		return terminalEvent{
			Kind:       terminalKindInfra,
			ErrorClass: "executor_returned_nil_outcome",
		}, ""
	}
	switch oc := outcome.GetOutcome().(type) {
	case *genv1.Outcome_Success:
		t := terminalEvent{
			Kind:          terminalKindComplete,
			Changed:       oc.Success.Changed,
			ChangeSummary: oc.Success.ChangeSummary,
			Scratch:       oc.Success.Scratch,
			Tags:          dedupTagsRT(oc.Success.Tags),
		}
		if oc.Success.AttributesDelta != nil {
			t.AttributesDel = oc.Success.AttributesDelta.AsMap()
		}
		return validateTags(ctx, dctx, t), ""
	case *genv1.Outcome_Error:
		var payloadGo any
		if oc.Error.Payload != nil {
			payloadGo = oc.Error.Payload.AsMap()
		}
		t := terminalEvent{
			Kind:       terminalKindErrored,
			ErrorClass: oc.Error.ErrorClass,
			Payload:    map[string]any{"payload": payloadGo},
			Scratch:    oc.Error.Scratch,
			Tags:       dedupTagsRT(oc.Error.Tags),
		}
		if oc.Error.AttributesDelta != nil {
			t.AttributesDel = oc.Error.AttributesDelta.AsMap()
		}
		return validateTags(ctx, dctx, t), ""
	case *genv1.Outcome_Park:
		t := terminalEvent{
			Kind:            terminalKindPark,
			ParkReason:      oc.Park.Reason,
			ParkReasonNote:  oc.Park.ReasonNote,
			ParkReasonLabel: oc.Park.ReasonLabel,
			Scratch:         oc.Park.Scratch,
			Tags:            dedupTagsRT(oc.Park.Tags),
		}
		if oc.Park.AttributesDelta != nil {
			t.AttributesDel = oc.Park.AttributesDelta.AsMap()
		}
		if oc.Park.ResumeAt != nil {
			t.ParkResumeAt = oc.Park.ResumeAt.AsTime()
		}
		return validateTags(ctx, dctx, t), ""
	case *genv1.Outcome_AwaitAsync:
		return terminalEvent{Kind: terminalKindAsyncAccepted}, oc.AwaitAsync.AsyncAckId
	default:
		return terminalEvent{
			Kind:       terminalKindInfra,
			ErrorClass: "executor_returned_unknown_outcome",
		}, ""
	}
}

func dedupTagsRT(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func validateTags(_ context.Context, dctx dispatchContext, t terminalEvent) terminalEvent {
	if len(t.Tags) == 0 {
		return t
	}
	if t.Kind != terminalKindComplete && t.Kind != terminalKindErrored && t.Kind != terminalKindPark {
		return t
	}
	args := dctx.Args
	acq := dctx.Acquired
	if args.DeclaredTagsFor == nil || acq.Executor == "" {
		return t
	}
	declared, ok := args.DeclaredTagsFor(acq.Executor)
	if !ok {
		return t
	}
	declaredSet := make(map[string]struct{}, len(declared))
	for _, d := range declared {
		declaredSet[d] = struct{}{}
	}
	for _, tag := range t.Tags {
		if _, ok := declaredSet[tag]; !ok {
			return terminalEvent{
				Kind:       terminalKindErrored,
				ErrorClass: "executor_protocol_violation",
				Payload: map[string]any{
					"reason":        "undeclared_tag",
					"tag":           tag,
					"declared_tags": declared,
				},
			}
		}
	}
	return t
}

func resolveAttributes(ctx context.Context, args RunArgs, acq *acquisition) (map[string]any, map[string]any, error) {
	if acq.NodeDef == nil || acq.NodeDef.Attributes == nil {
		empty := map[string]any{}
		bp, err := evaluateBeforeDispatchBreakpoints(ctx, args, acq, "", empty, nil)
		if err != nil {
			return nil, nil, err
		}
		return bp, nil, nil
	}
	schema, execSchema, execSchemaVisible := computeEffectiveAttributeSchema(args, acq)
	if schema == nil {
		empty := map[string]any{}
		bp, err := evaluateBeforeDispatchBreakpoints(ctx, args, acq, "", empty, nil)
		if err != nil {
			return nil, nil, err
		}
		return bp, nil, nil
	}
	if acq.Executor != "" && !execSchemaVisible {
		return nil, schema, &executorSchemaUnavailableError{Executor: acq.Executor}
	}
	if errs := node.CheckEffectiveAttributesSchema(
		schema,
		acq.NodeDef.Attributes.Schema,
		execSchema,
		extractReadOnlyPropsLocal(execSchema),
		execSchemaVisible,
	); len(errs) > 0 {
		first := errs[0]
		return nil, schema, &attributeValidationError{
			Reason: fmt.Sprintf("attributes_schema_invalid: %s: %s", first.Path, first.Msg),
		}
	}
	scope := resolveAcqScope(ctx, args, acq)
	rctx, err := buildResolveContextForDispatch(ctx, args, acq, scope.PartitionKey)
	if err != nil {
		return nil, schema, err
	}
	// @concept: attribute
	var carryForward map[string]any
	if args.Persist != nil {
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			priorRow, err := args.Persist.NodeAttributes().GetLatestByNode(ctx, acq.NodeID, acq.RunScopeID, tx)
			if err != nil {
				return err
			}
			if priorRow != nil {
				carryForward = priorRow.Data
			}
			return nil
		}); err != nil && args.Logger != nil {
			args.Logger.Warn("resolveAttributes: carry-forward lookup failed; proceeding with empty bag",
				"node_id", acq.NodeID.String(),
				"run_scope_id", acq.RunScopeID.String(),
				"error", err.Error())
		}
	}
	resolved, err := substituteAttributesSchema(schema, rctx, carryForward)
	if err != nil {
		return nil, schema, err
	}
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

	bpResolved, bpErr := evaluateBeforeDispatchBreakpoints(ctx, args, acq, scope.PartitionKey, resolved, schema)
	if bpErr != nil {
		return nil, schema, bpErr
	}
	resolved = bpResolved
	acq.MergedAttributes = resolved

	dispatchSchema := relaxRequiredToSourceDriven(schema)
	if err := attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch); err != nil {
		return nil, schema, &attributeValidationError{Reason: "dispatch_bag_invalid", Cause: err}
	}
	if execSchema != nil {
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
			Kind: events.KindAttributesSubstituted(),
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

// @concept: breakpoint
func evaluateBeforeDispatchBreakpoints(
	ctx context.Context,
	args RunArgs,
	acq *acquisition,
	partitionKey string,
	merged map[string]any,
	schema map[string]any,
) (map[string]any, error) {
	bpResolved, bpErr := EvaluateBreakpoints(ctx, args, CheckpointContext{
		InstanceID:       acq.InstanceID,
		NodeID:           acq.NodeID,
		DispatchID:       acq.DispatchID,
		FrameID:          acq.FrameID,
		Executor:         acq.Executor,
		NodeType:         acq.NodeType,
		Graph:            acq.GraphName,
		ChildKey:         partitionKey,
		MergedAttributes: merged,
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
			return merged, nil
		}
		return nil, bpErr
	}
	return bpResolved, nil
}

func buildResolveContextForDispatch(
	ctx context.Context, args RunArgs, acq *acquisition, partitionKey string,
) (attributes.ResolveContext, error) {
	deps := loadSubscribedNodeAttributesByID(ctx, args, acq)
	claims := map[string]claimproducer.ClaimResult{}
	for _, lk := range acq.Locks {
		if lk.Alias == "" {
			continue
		}
		claims[lk.Alias] = lk.ClaimResult
	}
	// @concept: claim-co-holdership — held claims (per the node's template `holds:` block)
	// are populated at the co-holder's own acquire-tx by loadInheritedClaimsForNode. They
	// carry the same `claimproducer.ClaimResult` shape (Address + ClaimScope)
	// that an opened claim carries, so the substitution grammar resolves
	// `{{claim.<alias>.address|payload.<f>|claim_scope}}` identically whether
	// the alias was opened (acq.Locks) or co-held (acq.HeldClaims).
	//
	// Acquired claims win on alias collision: if the node both `claims:`
	// and `holds:` the same alias, the opened entry in claims[] is
	// authoritative and the held entry is informational. Mirrors the
	// precedence at buildStoreHandles below.
	for alias, held := range acq.HeldClaims {
		if _, alreadyPresent := claims[alias]; alreadyPresent {
			continue
		}
		claims[alias] = held
	}
	var paramsRaw json.RawMessage
	if len(acq.InstanceParams) > 0 {
		b, err := json.Marshal(acq.InstanceParams)
		if err != nil {
			return attributes.ResolveContext{}, err
		}
		paramsRaw = b
	}
	triggerPayload, triggerType := lookupTriggerMessageForFrame(ctx, args, acq.FrameID)
	registryTypes := declaredMessageTypesForTemplate(ctx, args, acq.TemplateHash, nil)
	return attributes.ResolveContext{
		Deps:                  deps,
		Claim:                 claims,
		Params:                paramsRaw,
		ChildPartitionKey:     partitionKey,
		TriggerMessagePayload: triggerPayload,
		TriggerMessageType:    triggerType,
		RegistryDeclaredTypes: registryTypes,
	}, nil
}

func lookupTriggerMessageForFrame(
	ctx context.Context, args RunArgs, frameID shared.UUID,
) (json.RawMessage, string) {
	if args.Persist == nil || args.Persist.Messages() == nil {
		return nil, ""
	}
	var (
		payload json.RawMessage
		mtype   string
	)
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, t := triggerMessageForFrame(ctx, args, tx, frameID)
		payload = p
		mtype = t
		return nil
	})
	return payload, mtype
}

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

// @source: lib/graph/node/template_validator.go
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

// @concept: attribute
func substituteAttributesSchema(
	schema map[string]any, rctx attributes.ResolveContext, carryForward map[string]any,
) (map[string]any, error) {
	out := map[string]any{}
	if schema == nil {
		return out, nil
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return out, nil
	}
	for k, v := range carryForward {
		out[k] = v
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
				return nil, err
			}
			if val == nil {
				out[name] = emptyValueForSchemaProperty(prop)
				continue
			}
			out[name] = val
		case hasDefault:
			if _, present := out[name]; !present {
				out[name] = defaultVal
			}
		}
	}
	return out, nil
}

// @concept: attribute
func emptyValueForSchemaProperty(prop map[string]any) any {
	return emptyValueForSchemaType(firstConcreteSchemaType(prop["type"]))
}

func firstConcreteSchemaType(typeField any) string {
	switch t := typeField.(type) {
	case string:
		return t
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

func emptyValueForSchemaType(typeName string) any {
	switch typeName {
	case "number", "integer":
		return float64(0)
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default:
		return ""
	}
}

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

// @concept: run-scope
func priorDispositionFromStorageForm(s string) genv1.PriorDispatchDisposition {
	switch s {
	case "stale_recovery":
		return genv1.PriorDispatchDisposition_PRIOR_STALE_RECOVERY
	case "retry_after_error":
		return genv1.PriorDispatchDisposition_PRIOR_RETRY_AFTER_ERROR
	case "recalculate":
		return genv1.PriorDispatchDisposition_PRIOR_RECALCULATE
	}
	return genv1.PriorDispatchDisposition_PRIOR_NONE
}

// @concept: host-agent-proxy
func runScopeIDString(id shared.UUID) string {
	if id == (shared.UUID{}) {
		return ""
	}
	return id.String()
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
		NodeId:     acq.NodeID.String(),
		InstanceId: acq.InstanceID.String(),
		// @concept: host-agent-proxy — RunScopeId keys per-run-scope spawn isolation in the
		// host-agent-proxy: two concurrent run-scopes of one instance
		// must land on distinct late-bound child processes. Opaque to
		// in-process executors. Empty (not the zero-UUID string) when the
		// run-scope is unset, so the proxy's empty→instance-id fallback
		// keeps the non-fanned-out happy path keyed per instance rather
		// than collapsing every instance onto one shared zero-UUID child.
		RunScopeId:       runScopeIDString(acq.RunScopeID),
		NodeType:         acq.NodeType,
		Attributes:       attrStruct,
		AttributesSchema: schemaStruct,
		Stores:           stores,
		CallbackUrl:      dctx.Args.CallbackURL,
		CancelToken:      cancelToken,
		DispatchId:       acq.DispatchID.String(),
		// @concept: executor
		Scratch: acq.Scratch,
	}
	if acq.PriorDispatchID != nil {
		pid := acq.PriorDispatchID.String()
		req.PriorDispatchId = &pid
	}
	if acq.PriorDispatchDisposition != "" {
		disposition := priorDispositionFromStorageForm(acq.PriorDispatchDisposition)
		req.PriorDispatchDisposition = &disposition
	}
	return req, nil
}

// @concept: claim-co-holdership
func buildStoreHandles(acq *acquisition) (map[string]*genv1.StoreHandle, error) {
	out := make(map[string]*genv1.StoreHandle, len(acq.Locks)+len(acq.HeldClaims))
	for _, lk := range acq.Locks {
		spec, ok := lk.Spec.(claimproducer.ClaimSpec)
		if !ok {
			continue
		}
		h, err := makeClaimHandle(lk, spec)
		if err != nil {
			return nil, err
		}
		key := spec.Alias
		if key == "" {
			key = spec.ProducerName
		}
		out[key] = h
	}
	for alias, claim := range acq.HeldClaims {
		if _, alreadyPresent := out[alias]; alreadyPresent {
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

func makeHeldClaimHandle(alias string, claim claimproducer.ClaimResult) (*genv1.StoreHandle, error) {
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

func makeClaimHandle(lk AcquiredLock, spec claimproducer.ClaimSpec) (*genv1.StoreHandle, error) {
	out := &genv1.StoreHandle{}
	if lk.Producer != nil {
		out.Kind = lk.Producer.Name()
	}
	fields := map[string]any{}
	if len(lk.ClaimResult.Address) > 0 {
		var addrAny any
		if err := json.Unmarshal(lk.ClaimResult.Address, &addrAny); err != nil {
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
	out.CandidateHandle = lk.ProducerCandidateHandle
	return out, nil
}
