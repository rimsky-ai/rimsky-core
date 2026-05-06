// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package scheduler port of rimsky/src/scheduler/invalidate.ts and
// recalculate.ts. Pure functions over persistence.Store +
// persistence.Queue + shared.Clock + shared.Logger.
package integration

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/frame"
	"github.com/fallguy/rimsky/modeling/shared"
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
	// Frame controls whether the invalidate joins the current cascade
	// (FrameIn) or buffers through frame.EnqueueOrCoalesce as a new
	// frame (FrameNext; default).
	//
	// Empty string is treated as FrameNext for backwards compatibility
	// with all existing call sites (operator invalidate, scheduler
	// cron-fire, cascade-from-commit).
	//
	// See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §5.
	Frame string
	// SourceFrameID, when non-nil, overrides the frame_id read from the
	// source node row in invalidateInFrame. Used by post-Complete
	// handler.invalidate emits where the running-tx has already cleared
	// the source's frame_id (per the defensive guard in
	// nodes.UpdateState on transitions to 'fresh'). Without the
	// override, in-frame self-invalidate from on_executor_complete
	// would always fall back to next-frame, defeating the spec's
	// "single frame for the entire drain" property.
	SourceFrameID *shared.UUID
}

// InvalidateNode routes an invalidate event to TargetNodeID per the
// frame-resolution design (docs/history/2026-04-26-frame-resolution-design.md
// §3.1, §3.2). Under the frame model, "invalidate this node" is a frame
// source event: producers enqueue (or coalesce into) a rimsky_frames row
// rather than mutating rimsky_nodes.state directly. The scheduler tick's
// frame engine (§4.1) advances the queued frame to running and writes the
// source nodes stale + frame_id atomically.
//
// Default Flow (Frame == "" or "next"):
//  1. Append message_emitted + message_received events for audit.
//  2. Load target node to resolve its instance_id.
//  3. Run frame.EnqueueOrCoalesce inside a tx, keyed on (instance_id, target.ID).
//
// In-frame Flow (Frame == "in"):
//  1. Append the audit events.
//  2. Load target + source to resolve their instance_id and frame_id.
//  3. If the source has a non-NULL frame_id and target/source are in the
//     same instance, mark the target stale + frame_id = source's frame_id
//     in a single tx (no frame enqueue, no coalesce).
//  4. Otherwise fall back to the next-frame path.
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
	_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &args.TargetNodeID,
			Kind:   "message_emitted",
			Payload: map[string]any{
				"type":           "invalidate",
				"source_node_id": sourceStr,
				"target_node_id": args.TargetNodeID.String(),
				"params":         params,
			},
		}, tx); err != nil {
			return err
		}
		return sb.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &args.TargetNodeID,
			Kind:   "message_received",
			Payload: map[string]any{
				"type":           "invalidate",
				"source_node_id": sourceStr,
				"target_node_id": args.TargetNodeID.String(),
				"params":         params,
			},
		}, tx)
	})

	// Load target to resolve instance_id.
	var target *persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		t, err := sb.Nodes().Get(ctx, args.TargetNodeID, tx)
		target = t
		return err
	}); err != nil {
		return err
	}
	if target == nil {
		log.Warn("InvalidateNode: target not found", "node_id", args.TargetNodeID.String())
		return nil
	}

	useFrame := args.Frame
	if useFrame == "" {
		useFrame = "next"
	}
	if useFrame == "in" {
		return invalidateInFrame(ctx, args, target, log)
	}
	return invalidateNextFrame(ctx, args, target, log)
}

// invalidateNextFrame is the default path: enqueue or coalesce a frame
// for the target's instance, sourced on the target's id. Today's
// behavior, preserved unchanged.
func invalidateNextFrame(ctx context.Context, args InvalidateArgs, target *persistence.NodeRow, log shared.Logger) error {
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
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

// invalidateInFrame is the frame: in path. Bypasses
// frame.EnqueueOrCoalesce and directly transitions the target
// fresh → stale within the source's frame (the source's frame_id).
//
// Frame-id resolution order:
//  1. args.SourceFrameID, if non-nil (post-Complete handler.invalidate
//     where the running-tx has already cleared the source row's
//     frame_id);
//  2. otherwise re-read from the source node row.
//
// Falls back to the next-frame path when:
//   - SourceNodeID is nil and SourceFrameID is nil (no source frame
//     to join);
//   - the source can't be loaded and SourceFrameID is nil;
//   - the resolved frame_id is nil (e.g., the source is itself stale
//     and the cascade hasn't established a frame for this propagation).
//
// Per the reactive-loops + lifecycle-handlers spec §5.
func invalidateInFrame(ctx context.Context, args InvalidateArgs, target *persistence.NodeRow, log shared.Logger) error {
	if args.SourceFrameID == nil && args.SourceNodeID == nil {
		log.Debug("InvalidateNode: frame=in fallback (no source); next-frame")
		return invalidateNextFrame(ctx, args, target, log)
	}
	// Resolve frame_id outside the mutating tx. Calling
	// invalidateNextFrame from inside an open tx would self-deadlock
	// under SQLite (MaxOpenConns=1) and tie up two pool connections
	// concurrently under postgres. Per spec §5 this fallback
	// must remain reachable from the legacy invalidateTargets policy
	// chain (frame: in + nil source frame_id), so we resolve first
	// and only open the mutating tx on the success path.
	frameID := args.SourceFrameID
	if frameID == nil {
		var src *persistence.NodeRow
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			s, err := args.Persist.Nodes().Get(ctx, *args.SourceNodeID, tx)
			src = s
			return err
		}); err != nil || src == nil || src.FrameID == nil {
			srcStr := "(nil)"
			if args.SourceNodeID != nil {
				srcStr = args.SourceNodeID.String()
			}
			log.Debug("InvalidateNode: frame=in fallback (no source frame); next-frame",
				"source_node_id", srcStr)
			return invalidateNextFrame(ctx, args, target, log)
		}
		frameID = src.FrameID
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := args.Persist.Nodes().MarkStaleForCascade(ctx, target.ID, *frameID, tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &target.ID, InstanceID: &target.InstanceID,
			Kind: "state_transition",
			Payload: map[string]any{
				"from": "fresh", "to": "stale", "reason": "in_frame_invalidate",
				"frame_id": frameID.String(),
			},
		}, tx)
	})
}
