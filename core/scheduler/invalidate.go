// Package scheduler port of rimsky/src/scheduler/invalidate.ts and
// recalculate.ts. Pure functions over persistence.Store +
// persistence.Queue + shared.Clock + shared.Logger.
package scheduler

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

// InvalidateArgs is the payload for InvalidateNode.
type InvalidateArgs struct {
	// Persist is the unified persistence.Store handle. Required.
	Persist      persistence.Store
	Queue        persistence.Queue
	Clock        shared.Clock
	Logger       shared.Logger
	SourceNodeID *shared.UUID
	TargetNodeID shared.UUID
	Reason       string
	// SupervisorID, when set, claimant-guards RemoveForNode so the invalidate
	// path can't drop a dispatch row that belongs to a different supervisor.
	// Callers originating from the scheduler tick / cron fire leave this
	// empty (no supervisor is holding the row then).
	SupervisorID string
}

// InvalidateNode routes an invalidate event to TargetNodeID per the
// frame-resolution design (docs/specs/2026-04-26-frame-resolution-design.md
// §3.1, §3.2). Under the frame model, "invalidate this node" is a frame
// source event: producers enqueue (or coalesce into) a rimsky_frames row
// rather than mutating rimsky_nodes.state directly. The scheduler tick's
// frame engine (§4.1) advances the queued frame to running and writes the
// source nodes stale + frame_id atomically.
//
// Flow:
//  1. Append message_emitted + message_received events for audit.
//  2. Load target node to resolve its instance_id.
//  3. Run frame.EnqueueOrCoalesce inside a tx, keyed on (instance_id, target.ID).
//
// kill_requested writes are gone (§blessed-invariant 11): operator
// invalidates do not preempt running work; they enqueue a frame.
func InvalidateNode(ctx context.Context, args InvalidateArgs) error {
	if args.Persist == nil {
		return fmt.Errorf("InvalidateNode: Persist is required (frame.EnqueueOrCoalesce dereferences it)")
	}
	sb, log := args.Persist, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// Emit + receive events for the audit trail.
	params := map[string]any{
		"reason": args.Reason,
	}
	sourceStr := "(external)"
	if args.SourceNodeID != nil {
		sourceStr = args.SourceNodeID.String()
	}
	_ = sb.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &args.TargetNodeID,
		Kind:   "message_emitted",
		Payload: map[string]any{
			"type":           "invalidate",
			"source_node_id": sourceStr,
			"target_node_id": args.TargetNodeID.String(),
			"params":         params,
		},
	}, nil)
	_ = sb.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &args.TargetNodeID,
		Kind:   "message_received",
		Payload: map[string]any{
			"type":           "invalidate",
			"source_node_id": sourceStr,
			"target_node_id": args.TargetNodeID.String(),
			"params":         params,
		},
	}, nil)

	// Load target to resolve instance_id.
	target, err := sb.Nodes().Get(ctx, args.TargetNodeID, nil)
	if err != nil {
		return err
	}
	if target == nil {
		log.Warn("InvalidateNode: target not found", "node_id", args.TargetNodeID.String())
		return nil
	}

	// Enqueue/coalesce into a frame for this instance.
	return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := frame.EnqueueOrCoalesce(ctx, args.Persist, tx, target.InstanceID, target.ID)
		if err != nil {
			return err
		}
		log.Debug("InvalidateNode: frame enqueued/coalesced",
			"frame_id", fid.String(),
			"instance_id", target.InstanceID.String(),
			"target_node_id", target.ID.String(),
			"reason", args.Reason)
		return nil
	})
}
