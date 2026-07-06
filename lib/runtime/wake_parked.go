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
	WakeDeadlineElapsed WakeReason = "deadline_elapsed"
)

type WakeParkedArgs struct {
	Persist      persistence.Tables
	Queue        persistence.Queue
	Logger       shared.Logger
	TargetNodeID shared.UUID
	SupervisorID string
}

// @concept: parked-state
// @decision: walker-rule-per-sender-node
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
	return wakeParkedNode(ctx, args, target, reason)
}

// @concept: parked-state
// @story: resume-preserves-snapshot
func wakeParkedNode(ctx context.Context, args WakeParkedArgs, target *persistence.NodeRow, reason WakeReason) error {
	var targetRunScopeID shared.UUID
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := args.Persist.Nodes().GetLatestRunForNode(ctx, tx, target.ID)
		if err != nil {
			return err
		}
		if latest != nil {
			targetRunScopeID = latest.RunScopeID
		}
		return nil
	}); err != nil {
		return fmt.Errorf("wakeParkedNode: latest run lookup: %w", err)
	}
	if targetRunScopeID == (shared.UUID{}) {
		return nil
	}
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
		resumed, err := args.Queue.ResumeParkedInTx(ctx, tx, parked.NodeRunID)
		if err != nil {
			return err
		}
		if !resumed {
			return nil
		}
		if err := args.Persist.Nodes().UpdateState(ctx, parked.NodeRunID,
			cascade.NodeStateStale, cascade.ReasonDeadlineResume, nil, tx); err != nil {
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

func loadTargetNode(ctx context.Context, persist persistence.Tables, id shared.UUID) (*persistence.NodeRow, error) {
	var out *persistence.NodeRow
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := persist.Nodes().Get(ctx, id, tx)
		out = row
		return err
	})
	return out, err
}
