// Package scheduler — orphan-reap sweep.
//
// Per spec docs/specs/2026-04-27-stores-redesign-v3-design.md §7.5: the
// orphan reaper deletes the lock-holder row claimant-guarded WITHOUT
// calling Store.Abandon — the store's own TTL/sweep handles cleanup of
// its internal state. This is a deliberate decoupling from v2, which
// fired Abandon as part of the reap.
//
// The visibility-timeout sweep that v2 did against operator-owned items
// tables is gone — under v3 each store-service runs its own sweep
// internally (per spec §7.8 obligation #1). Rimsky has no visibility
// into substrate items tables.

package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
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
	expired, err := cfg.LockHolders.ListExpired(ctx)
	if err != nil {
		return fmt.Errorf("tick: list expired lock-holders: %w", err)
	}
	for _, lh := range expired {
		if err := reapOneLockHolder(ctx, cfg, lh, log); err != nil {
			log.Warn("tick: reap lock-holder failed",
				"lock_holder_id", lh.ID.String(),
				"kind", string(lh.Kind),
				"error", err.Error())
		}
	}
	return nil
}

// reapOneLockHolder runs the per-row reap in its own transaction. No
// substrate verb is fired; the store's TTL is the source of truth for
// its own state.
//
// If DeleteIfExpired finds no row to delete (claimant mismatch, or the
// row was heartbeat-extended in the race window between ListExpired
// and DeleteIfExpired), the function returns early without emitting
// `lock_orphan_reaped` and without committing the tx. This avoids
// false-positive observability noise when the reaper loses the race.
func reapOneLockHolder(ctx context.Context, cfg Config, lh store.LockHolderRow, log shared.Logger) error {
	tx, err := cfg.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	deleted, err := cfg.LockHolders.DeleteIfExpired(ctx, tx, lh.ID, lh.HolderSupervisorID)
	if err != nil {
		return fmt.Errorf("delete lock-holder row: %w", err)
	}
	if !deleted {
		// Lost the race (heartbeat-extended or claimant mismatch).
		// Defer-rollback closes the empty tx; nothing to emit.
		return nil
	}

	// Event emission is best-effort; the DELETE above is the load-bearing
	// operation. A failed event-append is logged but does NOT abort the
	// surrounding tx — the reap-DELETE still commits because the event is
	// observability-only (an audit-trail entry; nothing in the supervisor
	// or scheduler hot path consumes `lock_orphan_reaped`). Losing one
	// observability row is preferable to leaving a stale lock-holder
	// row across a deploy: the row would block fresh acquisitions of
	// the same region until the next sweep tick, and a held subgraph
	// would stay live with no producer to advance it.
	if err := appendEventInTx(ctx, tx, lh.HolderNodeID, "lock_orphan_reaped", lockReapPayload(lh)); err != nil {
		log.Warn("tick: append lock_orphan_reaped failed",
			"lock_holder_id", lh.ID.String(), "error", err.Error())
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reap tx: %w", err)
	}
	return nil
}

// lockReapPayload builds the structured payload for the
// lock_orphan_reaped event.
//
// Per blessed invariant 20, this payload MUST NOT include claim
// content (region_data, address). We surface only operator-relevant
// identifiers.
func lockReapPayload(lh store.LockHolderRow) map[string]any {
	payload := map[string]any{
		"lock_holder_id": lh.ID.String(),
		"lock_kind":      string(lh.Kind),
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

// appendEventInTx writes a row directly into rimsky_events inside the
// supplied tx. The payload is JSON-marshalled here so pgx writes a
// jsonb-compatible bytes value.
//
// instance_id is looked up from rimsky_nodes via a subquery so the
// event row is anchored to the same instance as the node it describes.
//
// @source: core/storage/postgres/events.go:Append (inlined for in-tx
// event emission during sweep)
func appendEventInTx(ctx context.Context, tx pgx.Tx, nodeID shared.UUID, kind string, payload map[string]any) error {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO rimsky_events (instance_id, node_id, kind, payload)
		 VALUES ((SELECT instance_id FROM rimsky_nodes WHERE id = $1), $1, $2, $3)`,
		nodeID, kind, bytes,
	)
	return err
}
