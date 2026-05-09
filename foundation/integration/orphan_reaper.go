// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Foundation orphan reaper — unified worker-request + claim-handle
// reaping per the Phase-6 layer-crystallization design.
//
// Two complementary primitives, both invoked off the conductor's tick:
//
//   - SweepOrphanedClaims (in conductor.go): stale `phase='active'`
//     rimsky_worker_request rows. Releases the claim claimant-guarded,
//     reverts the row to phase='pending' so a fresh supervisor can
//     pick it up. Held-phase rows are NEVER reaped here — they have
//     `claimed_by IS NULL` so the SQL predicate excludes them, and
//     auto-terminal handles their resolution.
//
//   - SweepLockHolders (this file): stale rimsky_claim_handle rows
//     whose `expires_at < now()`. Hard-deletes the row claimant-
//     guarded. Held claim handles whose owning worker-request was
//     already deleted (worker_request_id NULL via the FK SET NULL)
//     are reaped here too once their expires_at lapses, completing
//     the cleanup that auto-terminal couldn't (e.g. when the held
//     subgraph's nodes were themselves orphaned). No producer verb
//     fires; the producer's own TTL/sweep handles cleanup of its
//     internal state per foundation contract §4.5.
//
// Distinct from the dispatch-row reaper SweepOrphanedClaims, which
// keys on heartbeat staleness (dynamic). The claim-handle reaper
// keys on expires_at (acquisition-time + 5×heartbeat_interval).
//
// The visibility-timeout sweep that v2 did against operator-owned
// items tables is gone — each store-service runs its own sweep
// internally (foundation contract §4.5). Rimsky has no visibility
// into producer items tables.
//
// Plan E2: parked rows (phase='parked') are intentionally excluded
// from SweepOrphanedClaims because they have claimed_by IS NULL by
// construction (the active→parked transition in
// queue_park.go::ParkActiveInTx clears the claim). The SQL predicate
// `claimed_by IS NOT NULL AND last_heartbeat_at < cutoff` therefore
// never matches a parked row. Held claim handles for parked nodes
// remain in rimsky_claim_handle and are not reaped here either; the
// auto-terminal mechanism + the holder-row TTL handle them.

package integration

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// OrphanReaperArgs bundles the dependencies for SweepLockHolders.
type OrphanReaperArgs struct {
	Persist     persistence.Store
	LockHolders persistence.LockHoldersStore
	Logger      shared.Logger
}

// SweepLockHolders implements the v3 orphan-reap. For each
// rimsky_claim_handle row whose expires_at < now(), open a tx, DELETE
// the row claimant-guarded on holder_supervisor_id, emit a
// `lock_orphan_reaped` event, COMMIT. Cascade FK on
// rimsky_claim_holders cleans up held-claim rows.
//
// One tx per row so a single failure doesn't block the rest of the
// sweep. The store's TTL/sweep is responsible for any internal state
// the killed claim left behind.
func SweepLockHolders(ctx context.Context, args OrphanReaperArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	var expired []persistence.LockHolderRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.LockHolders.ListExpired(ctx, tx)
		expired = rows
		return err
	}); err != nil {
		return fmt.Errorf("tick: list expired lock-holders: %w", err)
	}
	for _, lh := range expired {
		if err := reapOneLockHolder(ctx, args, lh, log); err != nil {
			log.Warn("tick: reap lock-holder failed",
				"claim_handle_id", lh.ID.String(),
				"kind", string(lh.LockKind),
				"error", err.Error())
		}
	}
	return nil
}

// reapOneLockHolder runs the per-row reap in its own transaction. No
// store verb is fired; the store's TTL is the source of truth for
// its own state.
//
// If DeleteIfExpired finds no row to delete (claimant mismatch, or the
// row was heartbeat-extended in the race window between ListExpired
// and DeleteIfExpired), the function returns early without emitting
// `lock_orphan_reaped`. This avoids false-positive observability noise
// when the reaper loses the race.
func reapOneLockHolder(ctx context.Context, args OrphanReaperArgs, lh persistence.LockHolderRow, log shared.Logger) error {
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		deleted, err := args.LockHolders.DeleteIfExpired(ctx, lh.ID, lh.HolderSupervisorID, tx)
		if err != nil {
			return fmt.Errorf("delete lock-holder row: %w", err)
		}
		if !deleted {
			// Lost the race (heartbeat-extended or claimant mismatch).
			return nil
		}
		// Event emission is best-effort.
		nodeID := lh.HolderNodeID
		// Look up instance_id for the event row.
		nd, _ := args.Persist.Nodes().Get(ctx, lh.HolderNodeID, tx)
		var instanceID *shared.UUID
		if nd != nil {
			id := nd.InstanceID
			instanceID = &id
		}
		if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID:     &nodeID,
			InstanceID: instanceID,
			Kind:       "lock_orphan_reaped",
			Payload:    lockReapPayload(lh),
		}, tx); err != nil {
			log.Warn("tick: append lock_orphan_reaped failed",
				"claim_handle_id", lh.ID.String(), "error", err.Error())
		}
		return nil
	})
}

// lockReapPayload builds the structured payload for the
// lock_orphan_reaped event.
//
// Per blessed invariant 20, this payload MUST NOT include claim
// content (scope_data, address). We surface only operator-relevant
// identifiers.
func lockReapPayload(lh persistence.LockHolderRow) map[string]any {
	payload := map[string]any{
		"claim_handle_id": lh.ID.String(),
		"lock_kind":      string(lh.LockKind),
		"supervisor_id":  lh.HolderSupervisorID,
		"holder_node_id": lh.HolderNodeID.String(),
		"expires_at":     lh.ExpiresAt,
		"claimed_at":     lh.ClaimedAt,
		"last_heartbeat": lh.LastHeartbeatAt,
	}
	if lh.LockName != nil {
		payload["lock_name"] = *lh.LockName
	}
	if lh.StoreName != nil {
		payload["store_name"] = *lh.StoreName
	}
	if lh.Intent != nil {
		payload["intent"] = *lh.Intent
	}
	return payload
}
