// Port of rimsky/src/supervisor/on-error.ts (Plan A Task 10.1). Consults
// the node's error_types policy chain, evaluates an occurrence, persists
// the resolved EvaluatorState, logs an `error` event, and applies the
// resolved action (retry / invalidate / give_up).
package supervisor

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// OnErrorArgs is the payload for OnError.
type OnErrorArgs struct {
	Storage    storage.StorageBackend
	Queue      queue.DispatchQueue
	Clock      shared.Clock
	Logger     shared.Logger
	NodeID     shared.UUID
	InstanceID shared.UUID
	// SupervisorID identifies the supervisor handling the current run. When
	// non-empty, queue deletes (RemoveForNode) are claimant-guarded so a stale
	// sweep from a different supervisor can't accidentally drop our row.
	SupervisorID string
	ErrorClass   string
	Payload      map[string]any
}

// OnError evaluates the node's policy for ErrorClass and applies the
// resolved action. See spec §4.2 (on_error), §7.3 (policy chain).
//
// retry     → persist advanced EvaluatorState, transition running→stale
//
//	(reason policy_retry), re-enqueue dispatch with future enqueued_at
//	reflecting the backoff delay.
//
// invalidate → persist advanced EvaluatorState, transition running→stale
//
//	(reason policy_invalidate), resolve each target node type to a node
//	ID within the same instance, and route scheduler.InvalidateNode to
//	each resolved target. Unresolved targets are logged via the
//	unresolved_invalidate_target event.
//
// give_up   → transition → failed (reason policy_give_up).
func OnError(ctx context.Context, args OnErrorArgs) error {
	sb, log := args.Storage, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	nd, err := sb.Nodes().Get(ctx, args.NodeID, nil)
	if err != nil {
		return err
	}
	if nd == nil {
		return nil
	}

	// Resolve the per-error-class policy from the template spec.
	policy, err := lookupPolicy(ctx, sb, nd, args.ErrorClass)
	if err != nil {
		return err
	}

	state := node.EvaluatorState{
		ActionIndex:       nd.ActionIndex,
		RetryCounter:      nd.RetryCounter,
		CurrentErrorClass: nd.CurrentErrorClass,
	}
	resolved := node.Evaluate(policy, state, args.ErrorClass, nil)

	// Persist resolved EvaluatorState.
	if err := sb.Nodes().UpdateError(ctx, args.NodeID, resolved.NewState, nil); err != nil {
		return err
	}

	// Log the occurrence.
	_ = sb.Events().Append(ctx, storage.EventAppendInput{
		InstanceID: &args.InstanceID,
		NodeID:     &args.NodeID,
		Kind:       "error",
		Payload: map[string]any{
			"error_class":  args.ErrorClass,
			"details":      args.Payload,
			"action_taken": resolved.Kind,
			"action_index": resolved.NewState.ActionIndex,
			"delay_ms":     resolved.DelayMs,
		},
	}, nil)

	switch resolved.Kind {
	case "retry":
		if nd.State == shared.NodeStateRunning {
			if err := sb.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateStale, node.ReasonPolicyRetry, nil); err != nil {
				return err
			}
		}
		_ = args.Queue.RemoveForNode(ctx, args.NodeID, args.SupervisorID)
		return args.Queue.Enqueue(ctx, queue.DispatchRequest{
			NodeID:          args.NodeID,
			ExecutorName:    nd.Executor,
			ConcurrencyTags: nd.ConcurrencyTags,
			EnqueuedAt:      args.Clock.Now().Add(time.Duration(resolved.DelayMs) * time.Millisecond),
		})

	case "invalidate":
		if nd.State == shared.NodeStateRunning {
			if err := sb.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateStale, node.ReasonPolicyInvalidate, nil); err != nil {
				return err
			}
		}
		_ = args.Queue.RemoveForNode(ctx, args.NodeID, args.SupervisorID)

		other, err := sb.Nodes().ListByInstance(ctx, nd.InstanceID, nil)
		if err != nil {
			return err
		}
		typeToID := make(map[string]shared.UUID, len(other))
		for _, o := range other {
			typeToID[o.NodeType] = o.ID
		}
		var resolvedTargets []shared.UUID
		var unresolved []string
		for _, t := range resolved.Targets {
			if id, ok := typeToID[t]; ok {
				resolvedTargets = append(resolvedTargets, id)
			} else {
				unresolved = append(unresolved, t)
			}
		}
		if len(unresolved) > 0 {
			_ = sb.Events().Append(ctx, storage.EventAppendInput{
				InstanceID: &args.InstanceID,
				NodeID:     &args.NodeID,
				Kind:       "unresolved_invalidate_target",
				Payload: map[string]any{
					"error_class":        args.ErrorClass,
					"instance_id":        nd.InstanceID.String(),
					"unresolved_targets": unresolved,
					"resolved_targets":   uuidsToStrings(resolvedTargets),
				},
			}, nil)
		}
		src := args.NodeID
		for _, tid := range resolvedTargets {
			_ = scheduler.InvalidateNode(ctx, scheduler.InvalidateArgs{
				Storage: sb, Queue: args.Queue, Clock: args.Clock, Logger: log,
				SourceNodeID:   &src,
				TargetNodeID:   tid,
				Reason:         "policy_invalidate",
				RestoreVersion: resolved.RestoreVersion,
				SupervisorID:   args.SupervisorID,
			})
		}
		return nil

	case "give_up":
		if err := sb.Nodes().UpdateState(ctx, args.NodeID, shared.NodeStateFailed, node.ReasonPolicyGiveUp, nil); err != nil {
			return err
		}
		_ = args.Queue.RemoveForNode(ctx, args.NodeID, args.SupervisorID)
	}
	return nil
}

// lookupPolicy resolves the error_types[ErrorClass] block from the node's
// template spec. Returns nil (with nil error) when the template does not
// declare a policy for this error class — node.Evaluate treats that as a
// give_up("unknown_error_class").
func lookupPolicy(ctx context.Context, sb storage.StorageBackend, nd *storage.NodeRow, errorClass string) (*node.ErrorTypePolicy, error) {
	inst, err := sb.Instances().Get(ctx, nd.InstanceID, nil)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, nil
	}
	tmpl, err := sb.Templates().Get(ctx, inst.TemplateID, nil)
	if err != nil {
		return nil, err
	}
	if tmpl == nil {
		return nil, nil
	}
	for _, td := range tmpl.Spec.Nodes {
		if td.Type != nd.NodeType {
			continue
		}
		if p, ok := td.ErrorTypes[errorClass]; ok {
			// Copy so the caller can take its address without aliasing the map.
			cp := p
			return &cp, nil
		}
		return nil, nil
	}
	return nil, nil
}

func uuidsToStrings(xs []shared.UUID) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, x.String())
	}
	return out
}
