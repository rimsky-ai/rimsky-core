// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Cascade propagation: the cascade-of-stale walk handler. Marks
// dependents stale and recurses on `fresh_changed`. Driven by
// `concept:invalidate` (graph-level message). Pure functions over
// persistence.Tables + persistence.Queue + shared.Clock + shared.Logger.
//
// @concept: cascade
package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/frame"
)

// InvalidateArgs is the payload for InvalidateNode.
type InvalidateArgs struct {
	// Persist is the unified persistence.Tables handle. Required.
	Persist      persistence.Tables
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
	// source node row in invalidateInFrame. Used by post-Success-outcome
	// handler.invalidate emits where the running-tx has already cleared
	// the source's frame_id (per the defensive guard in
	// nodes.UpdateState on transitions to 'fresh'). Without the
	// override, in-frame self-invalidate from on_executor_complete
	// would always fall back to next-frame, defeating the spec's
	// "single frame for the entire drain" property.
	SourceFrameID *shared.UUID
	// Metrics is the per-invalidate instrumentation hook (plan I3).
	// Optional. Threaded through so invalidate sources can be classified
	// by reason (admin vs scheduler vs handler vs policy).
	Metrics MetricsHook
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
	// Plan I3: classify invalidate sources by Reason for the metric.
	// Reasons today: "admin_invalidate" (G3), "policy_invalidate"
	// (handler/error_types), "schedule_fired" (cron), "cascade" (post-
	// commit), and bespoke event-handler reasons. The metric label uses
	// a coarse bucket so high-cardinality reasons don't blow up
	// Prometheus storage.
	if args.Metrics != nil {
		args.Metrics.IncInvalidate(invalidateSourceBucket(args.Reason))
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
// for the target's instance, sourced on the target's id. The cascade
// walk for the target's invalidation does NOT fire here — under
// serial_queue mode the next-frame is queued behind the running
// frame; a wait-set row keyed on the queued frame_id can only gate
// receivers once that frame opens, by which time the cascade walk at
// applyTerminalComplete (firing on the source's settlement in the
// running frame) has propagated through the regular chain. The
// coalesce mode is a similar story: the queued/running frame is the
// same row, and the existing cascade walk at applyTerminalComplete
// covers the gating. For the multi-invalidator scenario described in
// spec Piece 1 the cascade walk at sender-invalidation transitions
// (`invalidateInFrame`, `applyResolvedAction` retry, heartbeat-lost,
// wake-parked) covers the in-flight gating; the next-frame variant
// relies on the receiver-side cascade-on-fresh_changed chain rather
// than seeding the queued frame eagerly.
//
//	@concept: cascade
//	@concept: wait-set
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
//  1. args.SourceFrameID, if non-nil (post-Success-outcome handler.invalidate
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
		if _, err := args.Persist.Nodes().MarkStaleForCascade(ctx, target.ID, *frameID, tx); err != nil {
			return err
		}
		if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &target.ID, InstanceID: &target.InstanceID,
			Kind: "state_transition",
			Payload: map[string]any{
				"from": "fresh", "to": "stale", "reason": "in_frame_invalidate",
				"frame_id": frameID.String(),
			},
		}, tx); err != nil {
			return err
		}
		// Pessimistic-invalidate per spec Piece 1: the target's
		// invalidation triggers the cascade walk that gates the
		// target's subscribers across multiple in-flight upstream
		// senders. Without this, a receiver gated on N senders only
		// sees one wait-set row at a time (whichever sender's
		// settlement most recently fired the walk).
		//
		//	@concept: cascade
		//	@concept: wait-set
		return walkCascadeForInvalidatedNode(ctx, args.Persist, args.Queue, tx,
			args.Logger, target.ID, target.InstanceID, *frameID)
	})
}

// walkCascadeForInvalidatedNode invokes the runtime cascade walk for a
// node that just transitioned from a settled state into stale/running.
// Loads the node's type via a tx-bound read (the persistence-side
// MarkStaleForCascade does not return the type) then drives the BFS
// walk over the subscription edge map.
//
// Placed in cascade_invalidate.go so the cascade-on-invalidation entry
// points can call it without depending on runtime/runner_terminal.go's
// internal acquisition shape. The `queue` parameter is required so
// the BFS walk can route parked receivers through
// wakeParkedReceiverInTx (which dereferences args.Queue); pass the
// caller's persistence.Queue handle through.
//
//	@concept: cascade
//	@concept: wait-set
func walkCascadeForInvalidatedNode(
	ctx context.Context, sb persistence.Tables, queue persistence.Queue, tx persistence.Tx,
	logger shared.Logger,
	senderNodeID, instanceID, frameID shared.UUID,
) error {
	// Minimal RunArgs shape with what cascadeSubscribersStaleInTx
	// reads (Persist for the subscription-edge cache miss path;
	// Queue for the parked-receiver wake fallback + the new
	// GetInFlightRunForNode resolver on the receiver side).
	args := RunArgs{Persist: sb, Queue: queue, Logger: logger}
	// Look up the sender's node-type for the inverse-edge map key.
	n, err := sb.Nodes().Get(ctx, senderNodeID, tx)
	if err != nil || n == nil {
		return err
	}
	// Resolve the sender's in-flight run id for the post-stage-5 wait-set
	// (rimsky_wait_set keys on the sender's run id). When the sender has
	// no in-flight row in this frame the cascade walk has nothing to
	// gate on; bail out quietly.
	senderRunID, ok, err := queue.GetInFlightRunForNode(ctx, tx, senderNodeID, frameID)
	if err != nil {
		return fmt.Errorf("walkCascadeForInvalidatedNode: resolve sender run: %w", err)
	}
	if !ok {
		return nil
	}
	return cascadeSubscribersStaleInTx(ctx, args, tx,
		senderNodeID, n.NodeType, senderRunID, instanceID, frameID)
}

// stalemarkAndEnqueueInFrame stale-marks `target` in `frameID` inside
// the caller-owned tx, emits a `state_transition` audit event with
// reason `hard_dep_pull` (only when the stale-mark actually inserted a
// new run row), then recursively walks the cascade so the just-stale
// upstream's own subscribers (within this frame) are gated on it too.
//
// Canonical in-tx sequence mirrors invalidateInFrame (#212-#268), but
// accepts a pre-existing tx rather than opening its own — the hard-dep
// pull runs inside cascadeSubscribersStaleInTx's existing tx, and
// calling InvalidateNode would self-deadlock (it opens its own tx).
//
// Idempotency: `MarkStaleForCascade` returns `inserted=false` when the
// row already had an in-flight run (e.g., diamond hard-dep topology
// where A hard-deps B and C; both B and C hard-dep D, so the BFS
// reaches D twice). On the no-op branch we skip the audit event AND
// the recursive walk — both would be duplicates of work already done on
// the first visit.
//
// Recursion choice: when the stale-mark DID insert a new run row, the
// helper MUST call walkCascadeForInvalidatedNode. Skipping the
// recursion would gate the upstream itself but leave its own
// subscribers ungated within this frame, breaking the cascade's
// single-frame-drain property.
//
//	@concept: cascade
//	@concept: attribute
func stalemarkAndEnqueueInFrame(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	target *persistence.NodeRow, frameID shared.UUID,
) error {
	inserted, err := args.Persist.Nodes().MarkStaleForCascade(ctx, target.ID, frameID, tx)
	if err != nil {
		return fmt.Errorf("stalemarkAndEnqueueInFrame: mark stale %s: %w", target.ID, err)
	}
	if !inserted {
		// Row already in-flight under this (or another) frame — the
		// earlier visit handled the audit event and the recursive walk.
		// Re-emitting would duplicate the event and re-walk the cascade.
		return nil
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &target.ID, InstanceID: &target.InstanceID,
		Kind: "state_transition",
		Payload: map[string]any{
			"from":     "fresh",
			"to":       "stale",
			"reason":   "hard_dep_pull",
			"frame_id": frameID.String(),
		},
	}, tx); err != nil {
		return fmt.Errorf("stalemarkAndEnqueueInFrame: append event %s: %w", target.ID, err)
	}
	return walkCascadeForInvalidatedNode(ctx, args.Persist, args.Queue, tx,
		args.Logger, target.ID, target.InstanceID, frameID)
}

// invalidateSourceBucket classifies the Reason string into a small
// fixed set of metric labels (admin / scheduler / handler / cascade /
// other) so the Prometheus invalidate counter has bounded cardinality.
//
// The case list MUST stay in lockstep with the Reason values fired by
// the integration layer. Today those are:
//
//   - "admin_invalidate"            (runner_lifecycle.go admin path,
//     wake_parked.go::InvalidateNode)
//   - "schedule_fired"              (graph/scheduler/schedule_ticker.go)
//   - "policy_invalidate"           (on_error.go, runner_terminal_errors.go)
//   - "handler_invalidate"          (runner_lifecycle.go on_event handler)
//   - "cascade"                     (cascade_invalidate.go)
//   - "parked_resume_deadline_elapsed" (sweep_parked.go)
//
// Anything else is bucketed as "other" so a missing entry surfaces as
// label drift rather than silent metric loss.
func invalidateSourceBucket(reason string) string {
	switch reason {
	case "admin_invalidate":
		return "admin"
	case "schedule_fired", "parked_resume_deadline_elapsed":
		return "scheduler"
	case "policy_invalidate", "handler_invalidate":
		return "handler"
	case "cascade":
		return "cascade"
	}
	return "other"
}
