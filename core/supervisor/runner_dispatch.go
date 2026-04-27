// Spec §17.1 step 3 + step 4: attribute substitution + executor / native
// dispatch path + heartbeat loop. The §17.1 step 6 terminal handling
// lives in runner_terminal.go.

package supervisor

import (
	"context"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/core/attributes"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/claimstorepg"
	"github.com/fallguy/rimsky/core/store/filesystem"
	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// dispatchContext carries the per-dispatch state through the executor
// stream loop. Built once at the dispatch entry point in runner.go.
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
// stream's terminal event. The terminal handler in runner_terminal.go
// branches on Kind. Mirrors the post-redesign vocabulary without going
// through the legacy TerminalOutcome type (which still references
// fields that have moved out of the codebase).
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
	terminalKindInfra // stream-level error
)

// dispatch routes the candidate to the appropriate execution path:
//
//   - executor != ""        → executor RPC + stream loop (§17.1 step 4a)
//   - executor == "" + claim → synthetic Complete{changed: true} (§17.1 step 4b)
//   - executor == "" + no claim → synthetic Complete (pure cascade — §17.1 step 4c)
//
// Returns either a terminal classification or, if the executor returned
// AsyncAccepted, a non-nil RunnerResult (the runner returns this directly
// without applying a terminal).
func dispatch(ctx context.Context, dctx dispatchContext) (terminalEvent, *RunnerResult, error) {
	acq := dctx.Acquired
	args := dctx.Args

	// Native dispatch paths (executor empty).
	if acq.Executor == "" {
		// Claim-only native: success when at least one claim was acquired.
		hasClaim := false
		for _, lk := range acq.Locks {
			if lk.Handle.Kind == string(store.LockHolderKindClaim) {
				hasClaim = true
				break
			}
		}
		if hasClaim {
			return terminalEvent{Kind: terminalKindComplete, Changed: true, ChangeSummary: "claim_acquired"}, nil, nil
		}
		// Pure cascade — synthetic success with changed=true so dependents
		// recalc. Preserves the existing pure-cascade semantics.
		return terminalEvent{Kind: terminalKindComplete, Changed: true, ChangeSummary: "pure_cascade"}, nil, nil
	}

	// Executor-backed dispatch.
	ep, ok := args.Resolver.Resolve(acq.Executor)
	if !ok {
		// Resolver miss is a structural failure, not a policy retry. Map
		// to dispatch_impossible via the give-up branch.
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
		// Hand off to the callback registry; the terminal will arrive
		// via /v1/callback/{async_ack_id} later. The AsyncContext
		// carries every piece the terminal handler reads — locks,
		// node-def for policy chain + quality rules, resolved
		// attributes + schema for Complete-branch validation. See
		// callback.go's driveTerminal which reconstructs RunArgs +
		// acquisition from this struct.
		if dctx.RegisterAsync != nil {
			dctx.RegisterAsync(asyncAck, AsyncContext{
				NodeID:             acq.NodeID,
				InstanceID:         acq.InstanceID,
				DispatchID:         acq.DispatchID,
				SupervisorID:       args.SupervisorID,
				StoreRegistry:      args.StoreRegistry,
				FrameID:            acq.FrameID,
				AcquiredLocks:      acq.Locks,
				NodeType:           acq.NodeType,
				Executor:           acq.Executor,
				NodeDef:            acq.NodeDef,
				ResolvedAttributes: dctx.Attributes,
				AttributesSchema:   dctx.AttributesSchema,
			})
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

// readExecutorStream consumes the executor's gRPC stream up to the
// terminal event. Returns (terminal, "") on Complete/Blocked/Errored,
// (zeroTerminal, ackID) on AsyncAccepted, or
// (TerminalInfra{stream_closed_without_terminal}, "") on EOF without
// terminal. Heartbeat events update the supervisor's per-node
// heartbeat row.
//
// The read loop is synchronous; the supervisor's runLoop heartbeat
// tick (spec §13.4) is the authoritative refresher of
// `rimsky_dispatch.last_heartbeat_at` and `rimsky_lock_holders.expires_at`
// for active runs. Adding a per-run heartbeat goroutine here would
// duplicate that work and create a second writer to the same row.
//
// Operator invalidates do not preempt running work — they enqueue or
// coalesce a frame (per docs/specs/2026-04-26-frame-resolution-design.md
// §3.3 / §5.4). The kill-poll branch and `isKillRequested` helper are gone.
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

// openNativeHandles is the §17.1 step 2 OpenHandle loop. Threads the
// per-store context (filesystem regions, claim-store handle data)
// before each call so the store's NativeHandle is fully populated.
//
// Per spec §17.2, when a lock was rebound (Resumed=true) the runner
// asserts that the underlying store actually supports resume — both
// the capability flag and the ResumableStore sub-interface. Failing
// here surfaces template / store-registry mismatches as
// open-handle-time errors instead of letting OpenHandle silently
// run a non-resumable code path.
func openNativeHandles(ctx context.Context, acq *acquisition) error {
	for i := range acq.Locks {
		lk := &acq.Locks[i]
		if lk.Store == nil {
			continue // named lock — no native handle.
		}
		if lk.Resumed {
			if !lk.Store.Capabilities().SupportsResume {
				return fmt.Errorf("resume requested for non-resumable store %q", lk.Handle.StoreName)
			}
			if _, ok := lk.Store.(store.ResumableStore); !ok {
				return fmt.Errorf("store %q lacks ResumableStore capability", lk.Handle.StoreName)
			}
		}
		hctx := ctx
		switch v := lk.Spec.(type) {
		case store.RegionLockSpec:
			var write, read []string
			if ws, ok := v.Region.([]string); ok {
				write = ws
			}
			if rs, ok := v.ReadRegion.([]string); ok {
				read = rs
			}
			hctx = filesystem.WithRegions(hctx, write, read)
		case store.ClaimLockSpec:
			hctx = claimstorepg.WithHandleData(hctx, lk.ClaimResult.Payload, lk.ClaimResult.ClaimID)
		}
		nh, err := lk.Store.OpenHandle(hctx, lk.Handle, lk.Resumed)
		if err != nil {
			return fmt.Errorf("OpenHandle(%s): %w", lk.Handle.StoreName, err)
		}
		lk.Native = nh
	}
	return nil
}

// resolveAttributes is the §17.1 step 3 substitution + validation pass.
// Returns the populated attribute object and the schema (so the
// terminal handler can re-validate at commit time per
// @blessed-invariant 12).
//
// On a missing required source the function returns an
// *attributes.ErrMissingSource the caller maps to
// template_resolution_failed.
func resolveAttributes(ctx context.Context, args RunArgs, acq *acquisition) (map[string]any, map[string]any, error) {
	if acq.NodeDef == nil {
		return map[string]any{}, nil, nil
	}
	schema := acq.NodeDef.Attributes.Schema
	if schema == nil {
		return map[string]any{}, nil, nil
	}
	deps := loadDepsAttributesByID(ctx, args, acq)

	// Build claim-result map keyed by store name for substitution.
	claims := map[string]store.ClaimResult{}
	for _, lk := range acq.Locks {
		if lk.Handle.Kind != string(store.LockHolderKindClaim) {
			continue
		}
		claims[lk.Handle.StoreName] = lk.ClaimResult
	}
	rctx := attributes.ResolveContext{
		Deps:   deps,
		Claims: claims,
		Params: acq.InstanceParams,
	}

	resolved, err := substituteAttributesSchema(schema, rctx)
	if err != nil {
		return nil, schema, err
	}
	// Validate at the dispatch gate. The dispatch-time schema is a
	// per-call derivative of the full schema with `required` restricted
	// to source-driven fields (executor-populated fields by definition
	// have no value yet at dispatch and would always trip
	// `missing properties` here). The unmodified schema is re-validated
	// at commit so executor-populated requireds still gate
	// attributes_committed (@blessed-invariant 12).
	dispatchSchema := relaxRequiredToSourceDriven(schema)
	if err := attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch); err != nil {
		return nil, schema, err
	}
	// Emit attributes_substituted for observability.
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "attributes_substituted",
		Payload: map[string]any{
			"substituted_fields": fieldNames(resolved),
		},
	}, nil)
	return resolved, schema, nil
}

// substituteAttributesSchema walks the schema's `properties` map and
// substitutes any property with a `source:` string into the output. A
// substitution miss on a required field returns an
// *attributes.ErrMissingSource; misses on optional fields are silently
// dropped.
//
// The schema is the JSON Schema fragment from
// `TemplateNodeDef.Attributes.Schema`. Required-vs-optional comes from
// the schema's own `required` array; absence-from-required means
// optional.
func substituteAttributesSchema(schema, rctx any) (map[string]any, error) {
	out := map[string]any{}
	sch, ok := schema.(map[string]any)
	if !ok {
		return out, nil
	}
	props, _ := sch["properties"].(map[string]any)
	if props == nil {
		return out, nil
	}
	required := stringSetFrom(sch["required"])
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
		val, err := attributes.Substitute(source, rctx.(attributes.ResolveContext))
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
// populated fields (no source) by definition cannot be present at
// dispatch — keeping them in `required` would trip the dispatch-time
// validator on every node that declares them. The unmodified schema is
// re-validated at commit time, so executor-populated requireds still
// gate the commit (@blessed-invariant 12).
//
// The returned map is a fresh top-level object; nested maps are
// shared with the original (Validate only reads them). Returns the
// input unchanged when there are no executor-populated requireds.
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

// stringSetFrom converts an interface containing a []any of strings
// (the JSON-schema `required` shape) into a string-set. Tolerates nil
// and unexpected shapes by returning an empty set.
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

// loadDepsAttributesByID is the per-dispatch dep map. Walks the
// candidate's storage.NodeRow.Dependencies, fetches each upstream's
// rimsky_node_attributes.data, keys by upstream node_type.
func loadDepsAttributesByID(ctx context.Context, args RunArgs, acq *acquisition) map[string]map[string]any {
	nd, err := args.Storage.Nodes().Get(ctx, acq.NodeID, nil)
	if err != nil || nd == nil {
		return nil
	}
	return loadDepsAttributes(ctx, args, nd)
}

// buildExecuteRequest assembles the gRPC ExecuteRequest payload per
// spec §12.1. Exposed as a function rather than a method so tests can
// reuse it later.
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

	stores := make(map[string]*genv1.StoreHandle, len(acq.Locks))
	for _, lk := range acq.Locks {
		if lk.Handle.Kind == string(store.LockHolderKindNamed) {
			continue
		}
		sh, err := makeStoreHandle(lk)
		if err != nil {
			return nil, err
		}
		stores[lk.Handle.StoreName] = sh
	}
	cancelToken := dctx.Args.SupervisorID + ":" + acq.DispatchID.String()
	prior, _ := dctx.Args.Storage.NodeAttributes().Get(ctx, acq.NodeID)
	runAttempt := 1
	if prior != nil {
		runAttempt = prior.RunAttempt
	}
	resumed := false
	for _, lk := range acq.Locks {
		if lk.Resumed {
			resumed = true
			break
		}
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
		Resumed:          resumed,
		RunAttempt:       int32(runAttempt),
	}, nil
}

// makeStoreHandle converts an AcquiredLock + NativeHandle into the
// per-store `StoreHandle` proto entry. Direct-mode filesystem maps to
// {Path, WriteRegions, ReadRegions}; claim_store maps to
// {Payload, ClaimID, StoreName}.
func makeStoreHandle(lk AcquiredLock) (*genv1.StoreHandle, error) {
	out := &genv1.StoreHandle{
		Resumed: lk.Resumed,
	}
	if lk.Store != nil {
		out.Kind = lk.Store.Kind()
	}
	switch nh := lk.Native.(type) {
	case store.FilesystemDirectHandle:
		s, err := structpb.NewStruct(map[string]any{"path": nh.Path})
		if err != nil {
			return nil, err
		}
		out.Handle = s
		out.WriteRegions = nh.WriteRegions
		out.ReadRegions = nh.ReadRegions
	case store.ClaimStoreHandle:
		fields := map[string]any{
			"claim_id":   nh.ClaimID,
			"store_name": nh.StoreName,
		}
		if nh.Payload != nil {
			fields["payload"] = nh.Payload
		}
		s, err := structpb.NewStruct(fields)
		if err != nil {
			return nil, err
		}
		out.Handle = s
	default:
		// Unknown / nil native handle: empty Handle.
		empty, _ := structpb.NewStruct(map[string]any{})
		out.Handle = empty
	}
	return out, nil
}

