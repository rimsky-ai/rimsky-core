package scheduler

import (
	"context"

	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

// RecalculateArgs is the payload for RecalculateNode.
type RecalculateArgs struct {
	Persist      persistence.Store
	Queue        persistence.Queue
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
	sb, log := args.Persist, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	_ = log

	sourceStr := "(external)"
	if args.SourceNodeID != nil {
		sourceStr = args.SourceNodeID.String()
	}
	_ = sb.Events().Append(ctx, persistence.EventAppendInput{
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
	// FrameID is sourced from the target node row — a stale node always
	// belongs to the in-flight frame (blessed-invariant 19). A nil frame_id
	// here means the frame engine hasn't yet advanced the source-node's
	// queued frame; defer to the next scheduler tick.
	if target.FrameID == nil {
		log.Debug("RecalculateNode: skip enqueue: target frame_id is nil",
			"node_id", target.ID.String())
		return nil
	}
	// RequiredStores is intentionally empty here. Per spec §6.2 an empty
	// slice trivially satisfies the supervisor-pool predicate
	// (RequiredStores ⊆ AcceptedStores).
	return args.Queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:         target.ID,
		ExecutorName:   target.Executor,
		RequiredStores: []string{},
		EnqueuedAt:     args.Clock.Now(),
		FrameID:        *target.FrameID,
	})
}
