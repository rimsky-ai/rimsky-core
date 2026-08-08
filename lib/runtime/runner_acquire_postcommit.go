// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func verifyBeforeRun(ctx context.Context, args RunArgs, acq acquisition) bool {
	ownership, err := args.Queue.GetClaimedBy(ctx, acq.NodeRunID)
	if err != nil {
		args.Logger.Warn("verifyBeforeRun: GetClaimedBy failed",
			"dispatch_id", acq.NodeRunID.String(), "error", err.Error())
		return false
	}
	return ownership.Kind == persistence.ClaimOwnershipKindClaimedBy && ownership.SupervisorID == args.SupervisorID
}

// @concept: terminal-resolution
// @decision: walker-rule-per-sender-node
func handleOrphanedClaim(ctx context.Context, args RunArgs, acq acquisition) {
	for _, lk := range acq.Locks {
		if err := bailAcquiredLock(ctx, args, lk); err != nil && args.Logger != nil {
			args.Logger.Warn("handleOrphanedClaim: unwind acquired lock failed",
				"claim_handle_id", lk.ClaimHandleID.String(),
				"producer", producerNameForSpec(lk.Spec),
				"dispatch_id", acq.NodeRunID.String(),
				"error", err.Error())
		}
	}
	if err := args.Queue.ReleaseClaim(ctx, acq.NodeRunID, args.SupervisorID); err != nil && args.Logger != nil {
		args.Logger.Warn("handleOrphanedClaim: release claim failed",
			"dispatch_id", acq.NodeRunID.String(),
			"supervisor_id", args.SupervisorID,
			"error", err.Error())
	}
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: events.KindOrphanedClaimLostRace(),
			Payload: eventpayload.New(&genv1.OrphanedClaimLostRacePayload{
				DispatchId:   acq.NodeRunID.String(),
				SupervisorId: args.SupervisorID,
			}),
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("handleOrphanedClaim: append orphaned_claim_lost_race event failed",
			"node_id", acq.NodeID.String(),
			"dispatch_id", acq.NodeRunID.String(),
			"error", err.Error())
	}
}

// @concept: terminal-resolution
func bailAcquiredLock(ctx context.Context, args RunArgs, lk AcquiredLock) error {
	var post postCommitFn
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if lk.Producer == nil {
			return args.ClaimHandles.Delete(ctx, lk.ClaimHandleID, args.SupervisorID, tx)
		}
		pc, err := ResolveClaimHandleTerminal(ctx, args, TerminalDecision{
			ClaimHandleID: lk.ClaimHandleID,
			SupervisorID:  args.SupervisorID,
			Source:        OwnershipBail,
			Outcome:       OutcomeAbandon,
			Producer:      lk.Producer,
			Scope:         claimScope(lk),
			Address:       claimAddress(lk),
			LeaseToken:    lk.ProducerLeaseToken,
		}, tx)
		if err != nil {
			return err
		}
		post = pc
		return nil
	}); err != nil {
		return err
	}
	if post != nil {
		post(ctx)
	}
	return nil
}

func emitLockAcquired(
	ctx context.Context, args RunArgs, acq acquisition, lk AcquiredLock, tx persistence.Tx,
) error {
	payload := &genv1.LockAcquiredPayload{
		HolderId:     lk.ClaimHandleID.String(),
		SupervisorId: args.SupervisorID,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload.LockKind = string(persistence.LockKindNamed)
		payload.LockName = sp.Name
	case claimproducer.ClaimSpec:
		payload.LockKind = string(persistence.LockKindScope)
		payload.ProducerName = sp.ProducerName
		payload.Alias = sp.Alias
		payload.Intent = string(sp.Intent)
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindLockAcquired(), Payload: eventpayload.New(payload),
	}, tx); err != nil {
		return fmt.Errorf("emitLockAcquired: %w", err)
	}
	return nil
}

func claimScope(lk AcquiredLock) []byte {
	if lk.Producer == nil {
		return nil
	}
	return []byte(lk.ClaimResult.ClaimScope)
}

func claimAddress(lk AcquiredLock) []byte {
	if lk.Producer == nil {
		return nil
	}
	return []byte(lk.ClaimResult.Address)
}
