// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package frame

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

type MetricsHook interface {
	ObserveFrameDuration(seconds float64)
}

// @concept: run-scope
type RunScopeTerminalFanout func(ctx context.Context, instanceID, runScopeID shared.UUID, terminalReason string, tx persistence.Tx)

const settledScopeTerminalReason = "frame_settled"

func RunTick(ctx context.Context, store persistence.Tables, queue persistence.Queue, logger Logger, scopeFanout RunScopeTerminalFanout, metrics MetricsHook) error {
	if err := runFrameEndDetection(ctx, store, logger, scopeFanout, metrics); err != nil {
		return fmt.Errorf("frame.RunTick: frame-end: %w", err)
	}
	if err := runOpenNewFrames(ctx, store, logger); err != nil {
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
	var result persistence.FrameEndResult
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, gerr := store.Frames().GetForObservability(ctx, frameID, tx)
		if gerr != nil {
			return gerr
		}
		if row != nil {
			triggeringMsg, merr := store.Messages().Get(ctx, row.TriggeringMessageID, tx)
			if merr != nil {
				return merr
			}
			if triggeringMsg == nil || triggeringMsg.DeliveredAt == nil {
				return nil
			}
		}
		res, err := store.Frames().EndFrameIfSettled(ctx, frameID, tx)
		if err != nil {
			return err
		}
		result = res
		if res.Transitioned && row != nil && row.RootRunScopeID != (shared.UUID{}) {
			if err := closeSettledFrameScopeTree(ctx, store, row.RootRunScopeID, frameID, instanceID, logger, scopeFanout, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if result.Transitioned {
		logger.Info("frame.end",
			"frame_id", frameID,
			"instance_id", instanceID,
			"final_state", result.FinalState)
		if metrics != nil && result.StartedAt != nil && result.EndedAt != nil {
			metrics.ObserveFrameDuration(result.EndedAt.Sub(*result.StartedAt).Seconds())
		}
	}
	return nil
}

// @concept: run-scope
// @concept: frame
func closeSettledFrameScopeTree(
	ctx context.Context, store persistence.Tables, rootRunScopeID, frameID, instanceID shared.UUID, logger Logger, scopeFanout RunScopeTerminalFanout, tx persistence.Tx,
) error {
	tree, err := store.RunScopes().ListTreeDeepestFirst(ctx, rootRunScopeID, tx)
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
			if err := store.RunScopes().Close(ctx, scope.ID, tx); err != nil {
				return fmt.Errorf("close run scope %s for settled frame %s: %w", scope.ID, frameID, err)
			}
		}
		if scopeFanout != nil {
			scopeFanout(ctx, instanceID, scope.ID, settledScopeTerminalReason, tx)
		}
	}
	return nil
}

// @decision: one-message-per-frame
func runOpenNewFrames(ctx context.Context, store persistence.Tables, logger Logger) error {
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
			fid, oerr := openRunningFrameForMessage(ctx, store, p.InstanceID, p.MessageID, tx)
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
