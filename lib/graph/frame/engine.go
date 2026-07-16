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

func RunTick(ctx context.Context, store persistence.Tables, queue persistence.Queue, logger Logger, metrics ...MetricsHook) error {
	var m MetricsHook
	if len(metrics) > 0 {
		m = metrics[0]
	}
	if err := runFrameEndDetection(ctx, store, logger, m); err != nil {
		return fmt.Errorf("frame.RunTick: frame-end: %w", err)
	}
	if err := runOpenNewFrames(ctx, store, queue, logger); err != nil {
		return fmt.Errorf("frame.RunTick: open: %w", err)
	}
	if err := runWarnStuckFrames(ctx, store, logger); err != nil {
		return fmt.Errorf("frame.RunTick: warn stuck: %w", err)
	}
	if err := runReapOrphanFrameDispatches(ctx, store, queue, logger); err != nil {
		return fmt.Errorf("frame.RunTick: reap orphan: %w", err)
	}
	return nil
}

func runFrameEndDetection(ctx context.Context, store persistence.Tables, logger Logger, metrics MetricsHook) error {
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
		if err := transitionFrameEnd(ctx, store, p.FrameID, p.InstanceID, logger, metrics); err != nil {
			logger.Warn("frame.end.transition_failed",
				"frame_id", p.FrameID,
				"instance_id", p.InstanceID,
				"error", err.Error())
			continue
		}
	}
	return nil
}

func transitionFrameEnd(ctx context.Context, store persistence.Tables, frameID, instanceID shared.UUID, logger Logger, metrics MetricsHook) error {
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

func runWarnStuckFrames(ctx context.Context, store persistence.Tables, logger Logger) error {
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
		logger.Warn("frame.stuck.observed",
			"frame_id", s.FrameID,
			"instance_id", s.InstanceID,
			"timeout_ms", s.FrameTimeoutMs)
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
