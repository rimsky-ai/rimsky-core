package scheduler

import (
	"context"

	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// RecalculateArgs is the payload for RecalculateNode.
type RecalculateArgs struct {
	Storage      storage.StorageBackend
	Queue        queue.DispatchQueue
	Clock        shared.Clock
	Logger       shared.Logger
	SourceNodeID *shared.UUID
	TargetNodeID shared.UUID
}

// RecalculateNode routes a recalculate message to TargetNodeID. Flow:
//  1. Append message_received.
//  2. Load target.
//  3. If fresh: no-op.
//  4. If running or failed: no-op.
//  5. If stale: check all dependencies. If any dep != fresh, no-op (we'll
//     be nudged again when that dep completes). If all fresh AND target
//     has an executor, enqueue dispatch row. If all fresh AND no executor,
//     the scheduler's pure-cascade sweep handles it — no dispatch needed;
//     no-op here.
func RecalculateNode(ctx context.Context, args RecalculateArgs) error {
	sb, log := args.Storage, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	_ = log

	sourceStr := "(external)"
	if args.SourceNodeID != nil {
		sourceStr = args.SourceNodeID.String()
	}
	_ = sb.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &args.TargetNodeID,
		Kind:   "message_received",
		Payload: map[string]any{
			"type":           "recalculate",
			"source_node_id": sourceStr,
			"target_node_id": args.TargetNodeID.String(),
		},
	}, nil)

	target, err := sb.Nodes().Get(ctx, args.TargetNodeID, nil)
	if err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	if target.State != shared.NodeStateStale {
		// fresh / running / failed — no-op.
		return nil
	}

	// Check all deps.
	for _, depID := range target.Dependencies {
		dep, err := sb.Nodes().Get(ctx, depID, nil)
		if err != nil {
			return err
		}
		if dep == nil || dep.State != shared.NodeStateFresh {
			return nil
		}
	}

	// All deps fresh. If no executor → pure-cascade sweep handles. If executor → enqueue.
	if target.Executor == "" {
		return nil
	}
	return args.Queue.Enqueue(ctx, queue.DispatchRequest{
		NodeID:          target.ID,
		ExecutorName:    target.Executor,
		ConcurrencyTags: target.ConcurrencyTags,
		EnqueuedAt:      args.Clock.Now(),
	})
}
