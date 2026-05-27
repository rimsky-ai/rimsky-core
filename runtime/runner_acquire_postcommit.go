// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// runner_acquire_postcommit.go — post-acquisition-tx helpers. Split
// out of `runner_acquire.go` per the 2026-05-17 cold-read paydown
// (Item 4 / Tier 1). Covers the verify-before-run race-detection
// guard, the orphaned-claim bail path, the running-state transition,
// the lock_acquired event emission, and the small claim-scope /
// claim-address accessors.

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
)

// verifyBeforeRun is the separate-read guard.
func verifyBeforeRun(ctx context.Context, args RunArgs, acq acquisition) bool {
	ownership, err := args.Queue.GetClaimedBy(ctx, acq.DispatchID)
	if err != nil {
		args.Logger.Warn("verifyBeforeRun: GetClaimedBy failed",
			"dispatch_id", acq.DispatchID.String(), "error", err.Error())
		return false
	}
	return ownership.Kind == "claimed_by" && ownership.SupervisorID == args.SupervisorID
}

// handleOrphanedClaim is the race-detection bail path: the supervisor
// has already opened the claim, inserted the lock-holder row, and
// committed the acquisition tx — and then verify-before-run discovered
// that another supervisor stole the dispatch row in the gap between
// commit and the second-read guard. The supervisor knows it just
// opened the store state and is now unwinding the in-progress
// acquisition; it owns the cleanup and calls Abandon on the store
// to release any partial state (via the shared abandonOpenedClaim
// helper — see @concept terminal-resolution), then deletes its own
// lock-holder row claimant-guarded, then emits orphaned_claim_lost_race.
//
// This is NOT the periodic orphan reaper. The periodic reaper at
// `graph/scheduler/sweep_locks.go::sweepClaimHandles` deletes expired
// lock-holder rows WITHOUT firing Abandon, per v3 spec §7.5: the
// store's own TTL/sweep handles internal state for owners that
// crashed without unwinding. The two paths are deliberately distinct:
// the bail path fires Abandon because the supervisor knows what it
// just did; the reaper does NOT fire Abandon because it can't
// distinguish a crashed-supervisor state from any other.
//
// @concept: terminal-resolution
func handleOrphanedClaim(ctx context.Context, args RunArgs, acq acquisition) {
	for _, lk := range acq.Locks {
		if lk.Producer != nil {
			scope := claimScope(lk)
			address := claimAddress(lk)
			if err := abandonOpenedClaim(ctx, lk.Producer, lk.ClaimHandleID, scope, address); err != nil {
				args.Logger.Warn("handleOrphanedClaim: Abandon failed",
					"producer", producerNameForSpec(lk.Spec), "error", err.Error())
			}
		}
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.ClaimHandles.Delete(ctx, lk.ClaimHandleID, args.SupervisorID, tx)
		}); err != nil && args.Logger != nil {
			args.Logger.Warn("handleOrphanedClaim: delete claim handle failed",
				"claim_handle_id", lk.ClaimHandleID.String(),
				"dispatch_id", acq.DispatchID.String(),
				"error", err.Error())
		}
	}
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "orphaned_claim_lost_race",
			Payload: map[string]any{
				"dispatch_id":   acq.DispatchID.String(),
				"supervisor_id": args.SupervisorID,
			},
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("handleOrphanedClaim: append orphaned_claim_lost_race event failed",
			"node_id", acq.NodeID.String(),
			"dispatch_id", acq.DispatchID.String(),
			"error", err.Error())
	}
}

// transitionToRunning is the short-tx state transition. Threads
// `acq.RunScopeID` as the run-row disambiguator so the state-machine
// update lands on this child's row even when fan-out siblings share
// the same node_id (per `concept:fan-out`).
func transitionToRunning(ctx context.Context, args RunArgs, acq acquisition) error {
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	})
}

// emitLockAcquired emits the per-spec lock_acquired event using the
// caller's open `tx`. Tx-required to prevent the nested-tx footgun
// (mirrors emitLockReleased): a fresh inner Persist.Transaction would
// self-deadlock under SQLite (MaxOpenConns=1) and tie up two pool
// connections under postgres if any future callsite invoked this from
// inside an open tx.
func emitLockAcquired(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq acquisition, lk AcquiredLock,
) error {
	payload := map[string]any{
		"holder_id":     lk.ClaimHandleID.String(),
		"supervisor_id": args.SupervisorID,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload["lock_kind"] = string(persistence.LockKindNamed)
		payload["lock_name"] = sp.Name
	case claimproducer.ClaimSpec:
		payload["lock_kind"] = string(persistence.LockKindScope)
		payload["producer_name"] = sp.ProducerName
		payload["alias"] = sp.Alias
		payload["intent"] = string(sp.Intent)
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "lock_acquired", Payload: payload,
	}, tx); err != nil {
		return fmt.Errorf("emitLockAcquired: %w", err)
	}
	return nil
}

// claimScope returns the store's scope bytes for a ClaimSpec
// acquisition; nil for NamedLockSpec.
func claimScope(lk AcquiredLock) []byte {
	if lk.Producer == nil {
		return nil
	}
	return []byte(lk.ClaimResult.ClaimScope)
}

// claimAddress returns the store's address bytes for a ClaimSpec
// acquisition; nil for NamedLockSpec.
func claimAddress(lk AcquiredLock) []byte {
	if lk.Producer == nil {
		return nil
	}
	return []byte(lk.ClaimResult.Address)
}
