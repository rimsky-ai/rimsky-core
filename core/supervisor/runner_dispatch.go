// Spec §17.1 step 3 + step 4: attribute substitution + executor /
// native dispatch path. Terminal handling lives in runner_terminal.go.

package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/core/attributes"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

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
}

type terminalKind int

const (
	terminalKindNone terminalKind = iota
	terminalKindComplete
	terminalKindBlocked
	terminalKindErrored
	terminalKindAsyncAccepted
	terminalKindInfra
)

// dispatch routes the candidate to the appropriate execution path.
func dispatch(ctx context.Context, dctx dispatchContext) (terminalEvent, *RunnerResult, error) {
	acq := dctx.Acquired
	args := dctx.Args

	if acq.Executor == "" {
		// Native dispatch: claim-only or pure-cascade. Synthesize a
		// Complete{changed: true} so dependents recalc.
		summary := "pure_cascade"
		for _, lk := range acq.Locks {
			if _, ok := lk.Spec.(store.ClaimSpec); ok {
				summary = "claim_acquired"
				break
			}
		}
		return terminalEvent{Kind: terminalKindComplete, Changed: true, ChangeSummary: summary}, nil, nil
	}

	ep, ok := args.Resolver.Resolve(acq.Executor)
	if !ok {
		_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "unresolved_executor",
			Payload: map[string]any{
				"executor_name": acq.Executor,
				"supervisor_id": args.SupervisorID,
			},
		}, nil)
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
	stream, err := client.Execute(ctx, req)
	if err != nil {
		return terminalEvent{Kind: terminalKindInfra, ErrorClass: "executor_dial_failed",
			Payload: map[string]any{"error": err.Error()}}, nil, nil
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
func readExecutorStream(
	ctx context.Context, dctx dispatchContext, stream interface {
		Recv() (*genv1.ExecuteEvent, error)
	},
) (terminalEvent, string) {
	args := dctx.Args
	acq := dctx.Acquired
	for {
		ev, rerr := stream.Recv()
		if rerr == io.EOF {
			return terminalEvent{
				Kind:       terminalKindInfra,
				ErrorClass: "stream_closed_without_terminal",
			}, ""
		}
		if rerr != nil {
			return terminalEvent{
				Kind:       terminalKindInfra,
				ErrorClass: "stream_error",
				Payload:    map[string]any{"error": rerr.Error()},
			}, ""
		}
		switch e := ev.Event.(type) {
		case *genv1.ExecuteEvent_Heartbeat:
			_ = args.Storage.Nodes().UpdateHeartbeat(ctx, acq.NodeID, args.Clock.Now(), args.SupervisorID, nil)
			_ = e
		case *genv1.ExecuteEvent_Complete:
			t := terminalEvent{
				Kind:          terminalKindComplete,
				Changed:       e.Complete.Changed,
				ChangeSummary: e.Complete.ChangeSummary,
			}
			if e.Complete.AttributesDelta != nil {
				t.AttributesDel = e.Complete.AttributesDelta.AsMap()
			}
			return t, ""
		case *genv1.ExecuteEvent_Blocked:
			var ctxGo any
			if e.Blocked.Context != nil {
				ctxGo = e.Blocked.Context.AsMap()
			}
			return terminalEvent{
				Kind:       terminalKindBlocked,
				ErrorClass: "executor_blocked",
				Payload:    map[string]any{"reason": e.Blocked.Reason, "context": ctxGo},
			}, ""
		case *genv1.ExecuteEvent_Errored:
			var payloadGo any
			if e.Errored.Payload != nil {
				payloadGo = e.Errored.Payload.AsMap()
			}
			return terminalEvent{
				Kind:       terminalKindErrored,
				ErrorClass: e.Errored.ErrorClass,
				Payload:    map[string]any{"payload": payloadGo},
			}, ""
		case *genv1.ExecuteEvent_AsyncAccepted:
			return terminalEvent{Kind: terminalKindAsyncAccepted}, e.AsyncAccepted.AsyncAckId
		}
	}
}

// resolveAttributes is the §17.1 step 3 substitution + validation
// pass. Returns the populated attribute object and the schema (so
// the terminal handler can re-validate at commit time).
func resolveAttributes(ctx context.Context, args RunArgs, acq *acquisition) (map[string]any, map[string]any, error) {
	if acq.NodeDef == nil {
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
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "attributes_substituted",
		Payload: map[string]any{
			"substituted_fields": fieldNames(resolved),
		},
	}, nil)
	return resolved, schema, nil
}

// buildResolveContextForDispatch assembles the substitution context
// from the candidate's deps, this acquisition's claims (keyed by
// alias), and instance params (marshalled to RawMessage so the
// substitution engine can lazy-walk into nested params).
func buildResolveContextForDispatch(
	ctx context.Context, args RunArgs, acq *acquisition,
) (attributes.ResolveContext, error) {
	deps := loadDepsAttributesByID(ctx, args, acq)
	claims := map[string]store.ClaimResult{}
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
	return attributes.ResolveContext{
		Deps:   deps,
		Claim:  claims,
		Params: paramsRaw,
	}, nil
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

// loadDepsAttributesByID is the per-dispatch dep map.
func loadDepsAttributesByID(ctx context.Context, args RunArgs, acq *acquisition) map[string]json.RawMessage {
	nd, err := args.Storage.Nodes().Get(ctx, acq.NodeID, nil)
	if err != nil || nd == nil {
		return nil
	}
	return loadDepsAttributes(ctx, args, nd)
}

// buildExecuteRequest assembles the gRPC ExecuteRequest payload.
func buildExecuteRequest(ctx context.Context, dctx dispatchContext) (*genv1.ExecuteRequest, error) {
	acq := dctx.Acquired
	def := acq.NodeDef

	userdataStruct := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if def != nil && len(def.Userdata) > 0 {
		if s, err := structpb.NewStruct(def.Userdata); err == nil {
			userdataStruct = s
		}
	}
	attrStruct := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if len(dctx.Attributes) > 0 {
		if s, err := structpb.NewStruct(dctx.Attributes); err == nil {
			attrStruct = s
		}
	}
	schemaStruct := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if len(dctx.AttributesSchema) > 0 {
		if s, err := structpb.NewStruct(dctx.AttributesSchema); err == nil {
			schemaStruct = s
		}
	}

	stores, err := buildStoreHandles(acq)
	if err != nil {
		return nil, err
	}

	cancelToken := dctx.Args.SupervisorID + ":" + acq.DispatchID.String()
	prior, _ := dctx.Args.Storage.NodeAttributes().Get(ctx, acq.NodeID)
	runAttempt := 1
	if prior != nil {
		runAttempt = prior.RunAttempt
	}
	return &genv1.ExecuteRequest{
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
	}, nil
}

// buildStoreHandles converts each ClaimSpec acquisition into a per-
// store StoreHandle proto entry. The handle's `handle` struct carries
// the substrate-supplied address bytes verbatim under the "address"
// key — opaque to Rimsky per @blessed-invariant 20.
func buildStoreHandles(acq *acquisition) (map[string]*genv1.StoreHandle, error) {
	out := make(map[string]*genv1.StoreHandle, len(acq.Locks))
	for _, lk := range acq.Locks {
		spec, ok := lk.Spec.(store.ClaimSpec)
		if !ok {
			continue
		}
		h, err := makeStoreHandle(lk, spec)
		if err != nil {
			return nil, err
		}
		out[spec.StoreName] = h
	}
	return out, nil
}

// makeStoreHandle builds the per-store proto entry. The handle
// payload is `{"address": <opaque shape>, "payload": <opaque shape>,
// "alias": ..., "intent": ...}`; the executor unwraps it per its
// substrate.
//
// @blessed-invariant 20 (wire-encoding site exception): this function
// decodes the substrate-supplied Address and Payload bytes via
// json.Unmarshal solely to project them into a `google.protobuf.Struct`
// for the wire. This is the SOLE sanctioned wire-encoding site outside
// `core/attributes/substitution.go::walkPath` (which is the sole
// sanctioned substitution-leaf extraction site). No transformation,
// logging, normalization, validation, or pattern-matching happens
// here — the bytes round-trip through structpb and are reconstituted
// verbatim on the executor side. If the spec moves the
// `StoreHandle.handle` field to `bytes`, this function shrinks to a
// straight byte-copy and the wire-encoding site disappears entirely.
func makeStoreHandle(lk AcquiredLock, spec store.ClaimSpec) (*genv1.StoreHandle, error) {
	out := &genv1.StoreHandle{}
	if lk.Store != nil {
		out.Kind = lk.Store.Kind()
	}
	fields := map[string]any{}
	if len(lk.ClaimResult.Address) > 0 {
		var addrAny any
		if err := json.Unmarshal(lk.ClaimResult.Address, &addrAny); err == nil {
			fields["address"] = addrAny
		} else {
			fields["address"] = string(lk.ClaimResult.Address)
		}
	}
	if len(lk.ClaimResult.Payload) > 0 {
		var payloadAny any
		if err := json.Unmarshal(lk.ClaimResult.Payload, &payloadAny); err == nil {
			fields["payload"] = payloadAny
		}
	}
	fields["alias"] = spec.Alias
	fields["intent"] = string(spec.Intent)
	s, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, fmt.Errorf("makeStoreHandle: structpb: %w", err)
	}
	out.Handle = s
	return out, nil
}
