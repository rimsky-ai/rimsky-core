// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// Logger is the minimum logging surface RunTick needs. Both
// *log/slog.Logger and shared.Logger (the scheduler's structured-log
// wrapper) satisfy this; keeping it tiny lets core/frame avoid
// importing core/shared without losing the scheduler's pre-bound
// fields when the scheduler wires its logger through.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// RunTick performs one frame-engine iteration. The caller must hold the
// scheduler-tick advisory lock (blessed-invariant 7).
//
// Steps per §4.1 of the spec:
//  1. Detect frame-end (transition running → completed|failed).
//  2. Advance queued (serial_queue and coalesce) — promote oldest queued to running.
//  3. Reap stuck frames (timeout exceeded with no claimed dispatches).
//  4. Reap orphan dispatches (frame in terminal state but dispatch still claimed).
//
// Each step opens its own short tx so partial failures don't poison the
// whole tick. The advisory lock guarantees serialization across replicas;
// within one process this is just a sequential loop.
func RunTick(ctx context.Context, store persistence.Store, queue persistence.Queue, logger Logger) error {
	if err := runFrameEndDetection(ctx, store, logger); err != nil {
		return fmt.Errorf("frame.RunTick: frame-end: %w", err)
	}
	if err := runAdvanceQueued(ctx, store, logger); err != nil {
		return fmt.Errorf("frame.RunTick: advance: %w", err)
	}
	if err := runReapStuckFrames(ctx, store, logger); err != nil {
		return fmt.Errorf("frame.RunTick: reap stuck: %w", err)
	}
	if err := runReapOrphanFrameDispatches(ctx, store, queue, logger); err != nil {
		return fmt.Errorf("frame.RunTick: reap orphan: %w", err)
	}
	return nil
}

func runFrameEndDetection(ctx context.Context, store persistence.Store, logger Logger) error {
	// Step 1: collect pendings outside any subsequent transition tx so a
	// single bad frame doesn't poison the whole tick.
	var pendings []persistence.FramePending
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ps, err := store.Frames().ListRunningFramesNoPendingNodes(ctx, tx)
		if err != nil {
			return err
		}
		pendings = ps
		return nil
	}); err != nil {
		return err
	}

	// Step 2: per-frame transition tx so a single frame's failure leaves
	// the rest unaffected.
	for _, p := range pendings {
		if err := transitionFrameEnd(ctx, store, p.FrameID, p.InstanceID, logger); err != nil {
			logger.Warn("frame.end.transition_failed",
				"frame_id", p.FrameID,
				"instance_id", p.InstanceID,
				"error", err.Error())
			continue
		}
	}
	return nil
}

// transitionFrameEnd applies one frame's running → completed|failed
// transition in its own short tx. After the frame transitions to
// terminal, the same tx evaluates the instance's terminal predicate
// and sets rimsky_instances.terminated_at if satisfied
// (control-plane spec §2.4: idempotent, set-once).
func transitionFrameEnd(ctx context.Context, store persistence.Store, frameID, instanceID shared.UUID, logger Logger) error {
	var transitioned bool
	var finalState persistence.FrameState
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		anyFailed, err := store.Frames().HasFailedNode(ctx, instanceID, frameID, tx)
		if err != nil {
			return err
		}
		finalState = persistence.FrameStateCompleted
		if anyFailed {
			finalState = persistence.FrameStateFailed
		}
		moved, err := store.Frames().MarkRunningFrameTerminal(ctx, frameID, finalState, tx)
		if err != nil {
			return err
		}
		transitioned = moved
		return store.Frames().MarkInstanceTerminatedIfDone(ctx, instanceID, tx)
	}); err != nil {
		return err
	}
	if transitioned {
		logger.Info("frame.end",
			"frame_id", frameID,
			"instance_id", instanceID,
			"final_state", finalState)
	}
	return nil
}

func runAdvanceQueued(ctx context.Context, store persistence.Store, logger Logger) error {
	var advances []persistence.FrameQueuedReady
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		as, err := store.Frames().ListQueuedFramesReadyToStart(ctx, tx)
		if err != nil {
			return err
		}
		advances = as
		return nil
	}); err != nil {
		return err
	}

	for _, a := range advances {
		if err := advanceOneFrame(ctx, store, a.FrameID, a.InstanceID, a.SourceNodeIDs, logger); err != nil {
			logger.Warn("frame.start.advance_failed",
				"frame_id", a.FrameID,
				"instance_id", a.InstanceID,
				"error", err.Error())
			continue
		}
	}
	return nil
}

// advanceOneFrame promotes one queued frame to running and writes its
// source nodes stale-with-frame_id. Runs in its own tx so failures are
// isolated to this frame; a wedged source node logs a warning and
// returns nil so the queued frame remains and the next tick can retry.
func advanceOneFrame(
	ctx context.Context, store persistence.Store, frameID, instanceID uuid.UUID, sources []uuid.UUID, logger Logger,
) error {
	var promoted bool
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		moved, err := store.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if !moved {
			// Another replica won; nothing to do.
			return nil
		}
		// Set source nodes stale with frame_id. In-bounds states: fresh,
		// failed, OR stale-with-nil-frame_id. Out-of-bounds rolls back the
		// promotion so the queued row remains; next tick retries.
		for _, src := range sources {
			matched, err := store.Frames().MarkSourceNodeStale(ctx, instanceID, src, frameID, tx)
			if err != nil {
				return err
			}
			if !matched {
				logger.Warn("frame.start.source_not_in_bounds",
					"frame_id", frameID,
					"instance_id", instanceID,
					"source_node_id", src)
				return errSourceOutOfBounds
			}
		}
		promoted = true
		return nil
	}); err != nil {
		if err == errSourceOutOfBounds {
			return nil
		}
		return err
	}
	if promoted {
		logger.Info("frame.start",
			"frame_id", frameID,
			"instance_id", instanceID,
			"source_node_ids", sources)
	}
	return nil
}

// errSourceOutOfBounds is a sentinel that triggers a tx rollback in
// advanceOneFrame without surfacing as a failure to the caller.
var errSourceOutOfBounds = fmt.Errorf("frame.advance: source node out of bounds")

func runReapStuckFrames(ctx context.Context, store persistence.Store, logger Logger) error {
	var stuckFrames []persistence.FrameStuck
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ss, err := store.Frames().ListStuckRunningFrames(ctx, tx)
		if err != nil {
			return err
		}
		stuckFrames = ss
		return nil
	}); err != nil {
		return err
	}

	for _, s := range stuckFrames {
		if err := reapOneStuckFrame(ctx, store, s.FrameID, s.InstanceID); err != nil {
			logger.Warn("frame.stuck.reap_failed",
				"frame_id", s.FrameID,
				"instance_id", s.InstanceID,
				"timeout_ms", s.FrameTimeoutMs,
				"error", err.Error())
			continue
		}
		logger.Warn("frame.stuck.reaped",
			"frame_id", s.FrameID,
			"instance_id", s.InstanceID,
			"timeout_ms", s.FrameTimeoutMs)
	}
	return nil
}

// reapOneStuckFrame fails one stuck frame in its own tx. Marks all
// stale/running nodes in the instance as failed, transitions the frame
// to failed, and (per spec §2.4) sets rimsky_instances.terminated_at
// when no further work remains.
func reapOneStuckFrame(ctx context.Context, store persistence.Store, frameID, instanceID shared.UUID) error {
	return store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Frames().FailAllPendingNodes(ctx, instanceID, tx); err != nil {
			return err
		}
		if _, err := store.Frames().MarkRunningFrameTerminal(ctx, frameID, persistence.FrameStateFailed, tx); err != nil {
			return err
		}
		return store.Frames().MarkInstanceTerminatedIfDone(ctx, instanceID, tx)
	})
}

// runReapOrphanFrameDispatches releases dispatch claims whose frame has
// already reached a terminal state. Per blessed-invariant 4 (claimant-
// guarded release), the per-row UPDATE filters by `claimed_by =
// supervisor_id` so a fresh supervisor that re-claimed the row keeps
// its live claim.
func runReapOrphanFrameDispatches(ctx context.Context, store persistence.Store, queue persistence.Queue, logger Logger) error {
	var orphans []persistence.OrphanFrameDispatch
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		os, err := store.Frames().ListOrphanFrameDispatches(ctx, tx)
		if err != nil {
			return err
		}
		orphans = os
		return nil
	}); err != nil {
		return err
	}

	for _, o := range orphans {
		// Queue.ReleaseClaim auto-commits its own tx; claimant-guarded by
		// expectedClaimedBy.
		if err := queue.ReleaseClaim(ctx, o.DispatchID, o.ClaimedBy); err != nil {
			logger.Warn("frame.orphan_dispatch.release_failed",
				"dispatch_id", o.DispatchID,
				"frame_id", o.FrameID,
				"prior_claimed_by", o.ClaimedBy,
				"error", err.Error())
			continue
		}
		logger.Warn("frame.orphan_dispatch.reaped",
			"dispatch_id", o.DispatchID,
			"frame_id", o.FrameID,
			"prior_claimed_by", o.ClaimedBy)
	}
	return nil
}
