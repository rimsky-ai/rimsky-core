// Package scheduler — orphan-reap sweep.
//
// Per spec docs/history/2026-04-27-stores-redesign-v3-design.md §7.5: the
// orphan reaper deletes the lock-holder row claimant-guarded WITHOUT
// calling Store.Abandon — the store's own TTL/sweep handles cleanup of
// its internal state. This is a deliberate decoupling from v2, which
// fired Abandon as part of the reap.
//
// The visibility-timeout sweep that v2 did against operator-owned items
// tables is gone — under v3 each store-service runs its own sweep
// internally (per spec §7.8 obligation #1). Rimsky has no visibility
// into store items tables.

package scheduler

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

// sweepLockHolders implements the v3 orphan-reap. For each
// rimsky_lock_holders row whose expires_at < now(), open a tx, DELETE
// the row claimant-guarded on holder_supervisor_id, emit a
// `lock_orphan_reaped` event, COMMIT. Cascade FK on
// rimsky_claim_holders cleans up held-claim rows.
//
// One tx per row so a single failure doesn't block the rest of the
// sweep. The store's TTL/sweep is responsible for any internal state
// the killed claim left behind.
func sweepLockHolders(ctx context.Context, cfg Config, log shared.Logger) error {
	expired, err := cfg.LockHolders.ListExpired(ctx, nil)
	if err != nil {
		return fmt.Errorf("tick: list expired lock-holders: %w", err)
	}
	for _, lh := range expired {
		if err := reapOneLockHolder(ctx, cfg, lh, log); err != nil {
			log.Warn("tick: reap lock-holder failed",
				"lock_holder_id", lh.ID.String(),
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
func reapOneLockHolder(ctx context.Context, cfg Config, lh persistence.LockHolderRow, log shared.Logger) error {
	return cfg.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		deleted, err := cfg.LockHolders.DeleteIfExpired(ctx, lh.ID, lh.HolderSupervisorID, tx)
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
		nd, _ := cfg.Persist.Nodes().Get(ctx, lh.HolderNodeID, tx)
		var instanceID *shared.UUID
		if nd != nil {
			id := nd.InstanceID
			instanceID = &id
		}
		if err := cfg.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID:     &nodeID,
			InstanceID: instanceID,
			Kind:       "lock_orphan_reaped",
			Payload:    lockReapPayload(lh),
		}, tx); err != nil {
			log.Warn("tick: append lock_orphan_reaped failed",
				"lock_holder_id", lh.ID.String(), "error", err.Error())
		}
		return nil
	})
}

// lockReapPayload builds the structured payload for the
// lock_orphan_reaped event.
//
// Per blessed invariant 20, this payload MUST NOT include claim
// content (region_data, address). We surface only operator-relevant
// identifiers.
func lockReapPayload(lh persistence.LockHolderRow) map[string]any {
	payload := map[string]any{
		"lock_holder_id": lh.ID.String(),
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
