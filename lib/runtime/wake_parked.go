// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type WakeReason string

const (
	WakeDeadlineElapsed    WakeReason = "deadline_elapsed"
	WakeExternalInvalidate WakeReason = "external_invalidate"
)

type WakeParkedArgs struct {
	Persist      persistence.Tables
	Queue        persistence.Queue
	Logger       shared.Logger
	TargetNodeID shared.UUID
	SupervisorID string
}

// @concept: parked-state
func WakeParkedNode(ctx context.Context, args WakeParkedArgs, reason WakeReason) error {
	if args.Persist == nil {
		return errors.New("WakeParkedNode: Persist required")
	}
	if args.Queue == nil {
		return errors.New("WakeParkedNode: Queue required")
	}
	if args.SupervisorID == "" {
		return errors.New("WakeParkedNode: SupervisorID required")
	}
	target, err := loadTargetNode(ctx, args.Persist, args.TargetNodeID)
	if err != nil {
		return err
	}
	if target == nil {
		if args.Logger != nil {
			args.Logger.Debug("WakeParkedNode: target not found",
				"node_id", args.TargetNodeID.String())
		}
		return nil
	}
	if target.State != cascade.NodeStateParked {
		return nil
	}
	return wakeParkedNode(ctx, args, target, reason)
}

// @concept: parked-state
// @story: resume-preserves-snapshot
func wakeParkedNode(ctx context.Context, args WakeParkedArgs, target *persistence.NodeRow, reason WakeReason) error {
	if target.RunScopeID == nil {
		return nil
	}
	targetRunScopeID := *target.RunScopeID
	parked, err := args.Queue.GetParkedByNode(ctx, target.ID, targetRunScopeID)
	if err != nil {
		return fmt.Errorf("wakeParkedNode: GetParkedByNode: %w", err)
	}
	if parked == nil {
		if args.Logger != nil {
			args.Logger.Warn("wakeParkedNode: no parked node-run row found",
				"node_id", target.ID.String())
		}
		return nil
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		resumed, err := args.Queue.ResumeParkedInTx(ctx, tx, parked.DispatchID)
		if err != nil {
			return err
		}
		if !resumed {
			return nil
		}
		if err := args.Persist.Nodes().UpdateState(ctx, target.ID, targetRunScopeID,
			cascade.NodeStateResuming, cascade.ReasonDeadlineResume, nil, tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &target.ID, InstanceID: &target.InstanceID,
			Kind: events.KindParkedResumeStarted(),
			Payload: map[string]any{
				"resume_reason": string(reason),
				"supervisor_id": args.SupervisorID,
				"prior_reason":  parked.Reason,
			},
		}, tx)
	})
}

// @concept: parked-state
// @concept: cascade
func wakeParkedReceiverInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	receiver persistence.NodeRow, frameID shared.UUID,
) error {
	return wakeParkedReceiverWithDepsInTx(ctx, args.Persist, args.Queue, tx, receiver, frameID)
}

// @concept: parked-state
// @concept: cascade
func wakeParkedReceiverWithDepsInTx(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue, tx persistence.Tx,
	receiver persistence.NodeRow, frameID shared.UUID,
) error {
	if receiver.RunScopeID == nil {
		return nil
	}
	receiverRunScopeID := *receiver.RunScopeID
	parked, err := queue.GetParkedByNode(ctx, receiver.ID, receiverRunScopeID)
	if err != nil {
		return fmt.Errorf("wakeParkedReceiverInTx: GetParkedByNode: %w", err)
	}
	if parked == nil {
		return nil
	}
	resumed, err := queue.ResumeParkedInTx(ctx, tx, parked.DispatchID)
	if err != nil {
		return fmt.Errorf("wakeParkedReceiverInTx: ResumeParkedInTx: %w", err)
	}
	if err := persist.Nodes().SetFrameID(ctx, receiver.ID, &frameID, tx); err != nil {
		return fmt.Errorf("wakeParkedReceiverInTx: stamp node.frame_id: %w", err)
	}
	if err := queue.RebindRunFrameInTx(ctx, tx, parked.DispatchID, frameID); err != nil {
		if resumed || !errors.Is(err, persistence.ErrRunRowMissing) {
			return fmt.Errorf("wakeParkedReceiverInTx: rebind run frame: %w", err)
		}
	}
	if !resumed {
		return nil
	}
	if err := persist.Nodes().UpdateState(ctx, receiver.ID, receiverRunScopeID,
		cascade.NodeStateStale, cascade.ReasonHandlerResume, nil, tx); err != nil {
		return fmt.Errorf("wakeParkedReceiverInTx: UpdateState: %w", err)
	}
	return persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &receiver.ID, InstanceID: &receiver.InstanceID,
		Kind: events.KindParkedResumeStarted(),
		Payload: map[string]any{
			"resume_reason": "cascade_wake",
			"prior_reason":  parked.Reason,
		},
	}, tx)
}

func loadTargetNode(ctx context.Context, persist persistence.Tables, id shared.UUID) (*persistence.NodeRow, error) {
	var out *persistence.NodeRow
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := persist.Nodes().Get(ctx, id, tx)
		out = row
		return err
	})
	return out, err
}
