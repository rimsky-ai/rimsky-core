// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type OrphanReaperArgs struct {
	Persist      persistence.Tables
	ClaimHandles persistence.ClaimHandleTable
	Logger       shared.Logger

	PreReapHook func(ctx context.Context, claimHandleID shared.UUID)
}

func SweepOrphanedClaimHandles(ctx context.Context, args OrphanReaperArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	var expired []persistence.ClaimHandleRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.ClaimHandles.ListExpired(ctx, tx)
		expired = rows
		return err
	}); err != nil {
		return fmt.Errorf("tick: list expired claim-handles: %w", err)
	}
	for _, lh := range expired {
		if err := reapOneClaimHandle(ctx, args, lh, log); err != nil {
			log.Warn("tick: reap claim-handle failed",
				"claim_handle_id", lh.ID.String(),
				"kind", string(lh.LockKind),
				"error", err.Error())
		}
	}
	return nil
}

func reapOneClaimHandle(ctx context.Context, args OrphanReaperArgs, lh persistence.ClaimHandleRow, log shared.Logger) error {
	if lh.HolderSupervisorID == nil {
		log.Warn("tick: skip claim-handle reap: nil holder_supervisor_id; row will be re-listed by ListExpired every sweep",
			"claim_handle_id", lh.ID.String(), "kind", string(lh.LockKind))
		return nil
	}
	if args.PreReapHook != nil {
		args.PreReapHook(ctx, lh.ID)
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		deleted, err := args.ClaimHandles.DeleteIfExpired(ctx, lh.ID, *lh.HolderSupervisorID, tx)
		if err != nil {
			return fmt.Errorf("delete lock-holder row: %w", err)
		}
		if !deleted {
			return nil
		}
		nodeID := lh.HolderNodeID
		nd, nerr := args.Persist.Nodes().Get(ctx, lh.HolderNodeID, tx)
		if nerr != nil {
			log.Warn("tick: node lookup failed; lock_orphan_reaped event will omit instance attribution",
				"claim_handle_id", lh.ID.String(), "node_id", nodeID.String(), "error", nerr.Error())
		}
		var instanceID *shared.UUID
		if nd != nil {
			id := nd.InstanceID
			instanceID = &id
		}
		if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID:     &nodeID,
			InstanceID: instanceID,
			Kind:       events.KindLockOrphanReaped(),
			Payload:    lockReapPayload(lh),
		}, tx); err != nil {
			log.Warn("tick: append lock_orphan_reaped failed",
				"claim_handle_id", lh.ID.String(), "error", err.Error())
		}
		return nil
	})
}

func lockReapPayload(lh persistence.ClaimHandleRow) eventpayload.Payload {
	supervisorID := ""
	if lh.HolderSupervisorID != nil {
		supervisorID = *lh.HolderSupervisorID
	}
	p := &genv1.LockOrphanReapedPayload{
		ClaimHandleId: lh.ID.String(),
		LockKind:      string(lh.LockKind),
		SupervisorId:  supervisorID,
		HolderNodeId:  lh.HolderNodeID.String(),
		ExpiresAt:     timestamppb.New(lh.ExpiresAt),
		ClaimedAt:     timestamppb.New(lh.ClaimedAt),
	}
	if lh.LockName != nil {
		p.LockName = *lh.LockName
	}
	if lh.ProducerName != nil {
		p.ProducerName = *lh.ProducerName
	}
	if lh.Intent != nil {
		p.Intent = string(*lh.Intent)
	}
	return eventpayload.New(p)
}
