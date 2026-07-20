// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

type MetricsHook interface {
	ObserveFrameDuration(seconds float64)
}

// @concept: run-scope
type RunScopeTerminalFanout func(ctx context.Context, tx persistence.Tx, instanceID, runScopeID shared.UUID, terminalReason string)

const settledScopeTerminalReason = "frame_settled"

func RunTick(ctx context.Context, store persistence.Tables, queue persistence.Queue, logger Logger, scopeFanout RunScopeTerminalFanout, metrics ...MetricsHook) error {
	var m MetricsHook
	if len(metrics) > 0 {
		m = metrics[0]
	}
	if err := runFrameEndDetection(ctx, store, logger, scopeFanout, m); err != nil {
		return fmt.Errorf("frame.RunTick: frame-end: %w", err)
	}
	if err := runOpenNewFrames(ctx, store, queue, logger); err != nil {
		return fmt.Errorf("frame.RunTick: open: %w", err)
	}
	if err := runReapOrphanFrameDispatches(ctx, store, queue, logger); err != nil {
		return fmt.Errorf("frame.RunTick: reap orphan: %w", err)
	}
	return nil
}

func runFrameEndDetection(ctx context.Context, store persistence.Tables, logger Logger, scopeFanout RunScopeTerminalFanout, metrics MetricsHook) error {
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

	for _, p := range pendings {
		if err := transitionFrameEnd(ctx, store, p.FrameID, p.InstanceID, logger, scopeFanout, metrics); err != nil {
			logger.Warn("frame.end.transition_failed",
				"frame_id", p.FrameID,
				"instance_id", p.InstanceID,
				"error", err.Error())
			continue
		}
	}
	return nil
}

func transitionFrameEnd(ctx context.Context, store persistence.Tables, frameID, instanceID shared.UUID, logger Logger, scopeFanout RunScopeTerminalFanout, metrics MetricsHook) error {
	var transitioned bool
	var finalState string
	var startedAt, endedAt *time.Time
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		anyFailed, err := store.Frames().HasFailedNode(ctx, instanceID, frameID, tx)
		if err != nil {
			return err
		}
		finalState = "completed"
		if anyFailed {
			finalState = "failed"
		}
		row, gerr := store.Frames().GetForObservability(ctx, frameID, tx)
		if gerr != nil {
			return gerr
		}
		moved, err := store.Frames().EndFrameIfSettled(ctx, frameID, tx)
		if err != nil {
			return err
		}
		transitioned = moved
		if moved && row != nil {
			startedAt = row.StartedAt
			now := time.Now()
			endedAt = &now
		}
		if moved && row != nil && row.RootRunScopeID != (shared.UUID{}) {
			if err := closeSettledFrameScopeTree(ctx, store, tx, row.RootRunScopeID, frameID, instanceID, logger, scopeFanout); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if transitioned {
		logger.Info("frame.end",
			"frame_id", frameID,
			"instance_id", instanceID,
			"final_state", finalState)
		if metrics != nil && startedAt != nil && endedAt != nil {
			metrics.ObserveFrameDuration(endedAt.Sub(*startedAt).Seconds())
		}
	}
	return nil
}

// @concept: run-scope
// @concept: frame
func closeSettledFrameScopeTree(
	ctx context.Context, store persistence.Tables, tx persistence.Tx,
	rootRunScopeID, frameID, instanceID shared.UUID,
	logger Logger, scopeFanout RunScopeTerminalFanout,
) error {
	tree, err := store.RunScopes().ListTreeDeepestFirst(ctx, tx, rootRunScopeID)
	if err != nil {
		return fmt.Errorf("list run-scope tree for settled frame %s: %w", frameID, err)
	}
	for _, scope := range tree {
		if scope.ClosedAt == nil {
			if scope.ID != rootRunScopeID {
				logger.Warn("frame.end.orphan_child_scope_closed_at_settlement",
					"frame_id", frameID,
					"instance_id", instanceID,
					"run_scope_id", scope.ID,
					"root_run_scope_id", rootRunScopeID)
			}
			if err := store.RunScopes().Close(ctx, tx, scope.ID); err != nil {
				return fmt.Errorf("close run scope %s for settled frame %s: %w", scope.ID, frameID, err)
			}
		}
		if scopeFanout != nil {
			scopeFanout(ctx, tx, instanceID, scope.ID, settledScopeTerminalReason)
		}
	}
	return nil
}

func runOpenNewFrames(ctx context.Context, store persistence.Tables, queue persistence.Queue, logger Logger) error {
	_ = queue
	var picks []persistence.PendingMessagePick
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ps, err := store.Messages().PickPendingMessagesForIdleInstances(ctx, tx)
		if err != nil {
			return err
		}
		picks = ps
		return nil
	}); err != nil {
		return err
	}

	for _, p := range picks {
		var frameID shared.UUID
		err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			fid, oerr := openRunningFrameForMessage(ctx, store, tx, p.InstanceID, p.MessageID)
			if oerr != nil {
				return oerr
			}
			frameID = fid
			return nil
		})
		if err != nil {
			logger.Warn("frame.start.open_failed",
				"instance_id", p.InstanceID,
				"triggering_message_id", p.MessageID,
				"error", err.Error())
			continue
		}
		logger.Info("frame.start",
			"frame_id", frameID,
			"instance_id", p.InstanceID,
			"triggering_message_id", p.MessageID)
	}
	return nil
}

func runReapOrphanFrameDispatches(ctx context.Context, store persistence.Tables, queue persistence.Queue, logger Logger) error {
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
		if err := queue.ReleaseClaim(ctx, o.NodeRunID, o.ClaimedBy); err != nil {
			logger.Warn("frame.orphan_dispatch.release_failed",
				"dispatch_id", o.NodeRunID,
				"frame_id", o.FrameID,
				"prior_claimed_by", o.ClaimedBy,
				"error", err.Error())
			continue
		}
		logger.Warn("frame.orphan_dispatch.reaped",
			"dispatch_id", o.NodeRunID,
			"frame_id", o.FrameID,
			"prior_claimed_by", o.ClaimedBy)
	}
	return nil
}
