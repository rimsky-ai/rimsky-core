// Plan A Task 10.3 — single-node runner.
//
// RunNode is the supervisor's per-dispatch execution path. Given a
// claim-guarded dispatch row, it verifies ownership, resolves the executor
// endpoint, streams ExecuteEvents from the executor, and maps the terminal
// event onto ApplyTerminalOutcome (Commit / OnError / infra-reenqueue).
//
// See spec §6.2 (per-dispatch flow), §7.2 (terminal classification), §17
// (ownership @blessed-invariant).
package supervisor

import (
	"context"
	"errors"
	"io"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// RunnerResult is the outcome of a single RunNode invocation.
type RunnerResult struct {
	Ran        bool   // true if we actually dispatched (false on lost-race / node-missing)
	Async      bool   // true if executor returned AsyncAccepted; terminal arrives later via callback
	AsyncAckID string // populated when Async is true
}

// RunArgs is the caller-supplied dependency bundle for RunNode.
type RunArgs struct {
	Storage      storage.StorageBackend
	Queue        queue.DispatchQueue
	Clock        shared.Clock
	Logger       shared.Logger
	NodeID       shared.UUID
	DispatchID   shared.UUID
	SupervisorID string
	Pool         *executor.ClientPool
	Resolver     executor.Resolver
	GetResource  func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error)
	CallbackURL  string // supervisor's own callback endpoint (for async handoff)
}

// AsyncContext is the per-async-handoff context the runner hands to the
// supervisor's callback registry (see callback.go). The registry resolves
// an incoming callback's ackID back to this AsyncContext, which carries
// everything ApplyTerminalOutcome needs to finalize the node.
type AsyncContext struct {
	NodeID       shared.UUID
	InstanceID   shared.UUID
	DispatchID   shared.UUID
	SupervisorID string
	GetResource  func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error)
}

// RunNode executes a claimed dispatch row end-to-end. Steps track spec §6.2:
//
//  1. Verify claim ownership. On mismatch emit orphaned_claim_lost_race and
//     return {Ran: false}.
//  2. Resolve executor endpoint. On miss emit unresolved_executor and route
//     to OnError("unresolved_executor").
//  3. Transition stale→running; stamp heartbeat; emit work_started.
//  4. Build ExecuteRequest (node/instance IDs, type, userdata,
//     instance_params, deps_data, reads_data, callback_url).
//  5. Stream events via executor.Client.Execute.
//  6. On terminal, route to ApplyTerminalOutcome.
//  7. On AsyncAccepted, register the pending async context (via
//     caller-supplied callback) and return.
//  8. On stream close without terminal, apply infra_error.
//
// registerAsync may be nil when the caller does not yet have a callback
// registry wired in (tests often pass a local closure that captures the ack).
func RunNode(
	ctx context.Context,
	args RunArgs,
	registerAsync func(ackID string, actx AsyncContext),
) (RunnerResult, error) {
	sb := args.Storage
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// Step 1 — verify claim ownership (§17 @blessed-invariant, §6.2 step 4).
	ownership, err := args.Queue.GetClaimedBy(ctx, args.DispatchID)
	if err != nil {
		return RunnerResult{}, err
	}
	if ownership.Kind != "claimed_by" || ownership.SupervisorID != args.SupervisorID {
		current := ""
		if ownership.Kind == "claimed_by" {
			current = ownership.SupervisorID
		}
		_ = sb.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &args.NodeID,
			Kind:   "orphaned_claim_lost_race",
			Payload: map[string]any{
				"supervisor_id":      args.SupervisorID,
				"dispatch_id":        args.DispatchID.String(),
				"ownership_kind":     ownership.Kind,
				"current_claimed_by": current,
			},
		}, nil)
		log.Info("runner: claim lost before dispatch",
			"node_id", args.NodeID.String(),
			"dispatch_id", args.DispatchID.String(),
			"supervisor_id", args.SupervisorID,
			"current", current)
		return RunnerResult{Ran: false}, nil
	}

	// Load node row (needed for instance ID + executor name + type + deps).
	nd, err := sb.Nodes().Get(ctx, args.NodeID, nil)
	if err != nil {
		return RunnerResult{}, err
	}
	if nd == nil {
		return RunnerResult{Ran: false}, errors.New("runner: node not found")
	}

	// Step 2 — resolve executor endpoint. On miss, go directly stale→failed via
	// ReasonDispatchImpossible. No on_error, no policy chain — this is an
	// infrastructural failure, not an application error, and the node never
	// entered running.
	ep, ok := args.Resolver.Resolve(nd.Executor)
	if !ok {
		_ = sb.Events().Append(ctx, storage.EventAppendInput{
			NodeID:     &args.NodeID,
			InstanceID: &nd.InstanceID,
			Kind:       "unresolved_executor",
			Payload: map[string]any{
				"node_id":       args.NodeID.String(),
				"executor_name": nd.Executor,
				"supervisor_id": args.SupervisorID,
			},
		}, nil)
		if err := sb.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateFailed, node.ReasonDispatchImpossible, nil); err != nil {
			return RunnerResult{Ran: true}, err
		}
		_ = sb.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &args.NodeID, InstanceID: &nd.InstanceID,
			Kind: "error",
			Payload: map[string]any{
				"error_class":  "unresolved_executor",
				"details":      map[string]any{"executor_name": nd.Executor},
				"action_taken": "dispatch_impossible",
			},
		}, nil)
		return RunnerResult{Ran: true}, nil
	}

	client, err := args.Pool.GetOrCreate(ep)
	if err != nil {
		return RunnerResult{Ran: false}, err
	}

	// Step 3 — stale→running, stamp heartbeat, emit work_started.
	if err := sb.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateRunning, node.ReasonDispatchClaimed, nil); err != nil {
		return RunnerResult{Ran: false}, err
	}
	_ = sb.Nodes().UpdateHeartbeat(ctx, args.NodeID, args.Clock.Now(), args.SupervisorID, nil)
	_ = sb.Events().Append(ctx, storage.EventAppendInput{
		NodeID:     &args.NodeID,
		InstanceID: &nd.InstanceID,
		Kind:       "work_started",
		Payload:    map[string]any{"supervisor_id": args.SupervisorID},
	}, nil)

	// Step 4 — build ExecuteRequest.
	req := buildExecuteRequest(ctx, sb, args, nd)

	// Step 5 — stream events.
	stream, err := client.Execute(ctx, req)
	if err != nil {
		// Dial / RPC-start failure — infra error (no retry-counter bump).
		return RunnerResult{Ran: true}, ApplyTerminalOutcome(ctx, ApplyTerminalArgs{
			Storage: sb, Queue: args.Queue, Clock: args.Clock, Logger: log,
			NodeID: args.NodeID, InstanceID: nd.InstanceID,
			SupervisorID: args.SupervisorID,
			GetResource:  args.GetResource,
			Outcome: TerminalOutcome{
				Kind:       TerminalInfraError,
				ErrorClass: "executor_dial_failed",
				Payload:    map[string]any{"error": err.Error()},
			},
		})
	}

	var (
		sawTerminal bool
		outcome     TerminalOutcome
		async       RunnerResult
	)

	for {
		ev, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			outcome = TerminalOutcome{
				Kind:       TerminalInfraError,
				ErrorClass: "stream_error",
				Payload:    map[string]any{"error": rerr.Error()},
			}
			sawTerminal = true
			break
		}
		switch e := ev.Event.(type) {
		case *genv1.ExecuteEvent_Heartbeat:
			_ = sb.Nodes().UpdateHeartbeat(ctx, args.NodeID, args.Clock.Now(), args.SupervisorID, nil)
			_ = e
		case *genv1.ExecuteEvent_Complete:
			var resultGo any
			if e.Complete.Result != nil {
				resultGo = e.Complete.Result.AsInterface()
			}
			outcome = TerminalOutcome{
				Kind:          TerminalRunSucceeded,
				Result:        resultGo,
				Changed:       e.Complete.Changed,
				ChangeSummary: e.Complete.ChangeSummary,
			}
			sawTerminal = true
		case *genv1.ExecuteEvent_Blocked:
			var ctxGo any
			if e.Blocked.Context != nil {
				ctxGo = e.Blocked.Context.AsInterface()
			}
			outcome = TerminalOutcome{
				Kind:       TerminalAppError,
				ErrorClass: "executor_blocked",
				Payload:    map[string]any{"reason": e.Blocked.Reason, "context": ctxGo},
			}
			sawTerminal = true
		case *genv1.ExecuteEvent_Errored:
			var payloadGo any
			if e.Errored.Payload != nil {
				payloadGo = e.Errored.Payload.AsInterface()
			}
			outcome = TerminalOutcome{
				Kind:       TerminalAppError,
				ErrorClass: e.Errored.ErrorClass,
				Payload:    map[string]any{"payload": payloadGo},
			}
			sawTerminal = true
		case *genv1.ExecuteEvent_AsyncAccepted:
			if registerAsync != nil {
				registerAsync(e.AsyncAccepted.AsyncAckId, AsyncContext{
					NodeID:       args.NodeID,
					InstanceID:   nd.InstanceID,
					DispatchID:   args.DispatchID,
					SupervisorID: args.SupervisorID,
					GetResource:  args.GetResource,
				})
			}
			async = RunnerResult{Ran: true, Async: true, AsyncAckID: e.AsyncAccepted.AsyncAckId}
			sawTerminal = true
		}
		if sawTerminal {
			break
		}
	}

	// Drain to EOF so the server's send goroutine can exit cleanly. Safe —
	// the server should have closed the stream by now.
	if sawTerminal {
		for {
			if _, rerr := stream.Recv(); rerr != nil {
				break
			}
		}
	}
	// stream.Close() MUST run before any return below — including the
	// AsyncAccepted path. Leaking the gRPC stream when we return early from
	// the async branch would strand the server-side send goroutine and
	// ultimately exhaust the connection pool. Keep this call at top-level,
	// not inside any branch.
	_ = stream.Close()

	if async.Async {
		return async, nil
	}
	if !sawTerminal {
		outcome = TerminalOutcome{
			Kind:       TerminalInfraError,
			ErrorClass: "stream_closed_without_terminal",
		}
	}
	return RunnerResult{Ran: true}, ApplyTerminalOutcome(ctx, ApplyTerminalArgs{
		Storage: sb, Queue: args.Queue, Clock: args.Clock, Logger: log,
		NodeID: args.NodeID, InstanceID: nd.InstanceID,
		SupervisorID: args.SupervisorID,
		GetResource:  args.GetResource,
		Outcome:      outcome,
	})
}

// buildExecuteRequest assembles the gRPC ExecuteRequest payload from the
// node row + instance params + template userdata + dep resource versions.
func buildExecuteRequest(
	ctx context.Context,
	sb storage.StorageBackend,
	args RunArgs,
	nd *storage.NodeRow,
) *genv1.ExecuteRequest {
	// instance_params: pull instance row (best-effort; missing params ok).
	instanceParams := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	inst, _ := sb.Instances().Get(ctx, nd.InstanceID, nil)
	if inst != nil && len(inst.Params) > 0 {
		if s, err := structpb.NewStruct(inst.Params); err == nil {
			instanceParams = s
		}
	}

	// userdata: pull from template's TemplateNodeDef for this node's type.
	userdata := findNodeUserdata(ctx, sb, nd)
	userdataStruct := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if len(userdata) > 0 {
		if s, err := structpb.NewStruct(userdata); err == nil {
			userdataStruct = s
		}
	}

	// deps_data: map each dep's node_type → its current resource version's
	// inline JSON. Dependencies with no owned resource or no committed
	// version are simply absent from the map.
	depsData := make(map[string]*structpb.Value)
	for _, depID := range nd.Dependencies {
		depNode, _ := sb.Nodes().Get(ctx, depID, nil)
		if depNode == nil {
			continue
		}
		depResources, _ := sb.Resources().ListByOwner(ctx, depNode.ID, nil)
		if len(depResources) == 0 {
			continue
		}
		r := depResources[0]
		if r.CurrentVersionID == nil {
			continue
		}
		v, _ := sb.Resources().GetVersion(ctx, *r.CurrentVersionID, nil)
		if v == nil || len(v.Data) == 0 {
			continue
		}
		var parsed any
		if err := jsonUnmarshalImpl(v.Data, &parsed); err != nil {
			continue
		}
		if val, err := structpb.NewValue(parsed); err == nil {
			depsData[depNode.NodeType] = val
		}
	}

	return &genv1.ExecuteRequest{
		NodeId:         args.NodeID.String(),
		InstanceId:     nd.InstanceID.String(),
		NodeType:       nd.NodeType,
		Userdata:       userdataStruct,
		InstanceParams: instanceParams,
		DepsData:       depsData,
		ReadsData:      map[string]*structpb.Value{}, // reads_resources wiring is post-v1
		CallbackUrl:    args.CallbackURL,
	}
}

// findNodeUserdata looks up the userdata block for this node's node_type
// from the parent template's spec. Returns nil when the template or type
// is missing (both are degraded-but-valid states for a running node).
func findNodeUserdata(ctx context.Context, sb storage.StorageBackend, nd *storage.NodeRow) map[string]any {
	inst, _ := sb.Instances().Get(ctx, nd.InstanceID, nil)
	if inst == nil {
		return nil
	}
	tmpl, _ := sb.Templates().Get(ctx, inst.TemplateID, nil)
	if tmpl == nil {
		return nil
	}
	for _, td := range tmpl.Spec.Nodes {
		if td.Type == nd.NodeType {
			return td.Userdata
		}
	}
	return nil
}
