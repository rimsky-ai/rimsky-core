// Package scheduler — orphan-reap and visibility-timeout sweeps
// (spec §13.5 / §12.12). Extracted from scheduler.go for the
// ~500-line cold-read guideline.
//
// Under stores-redesign-v2 (spec §13.5 / §14.4) the sweep shape is
// simpler than the prior reference-counted scheme:
//   - Orphan-reap walks rimsky_lock_holders.expires_at < now() rows;
//     for region rows, it calls Store.Abandon (substrate undoes any
//     in-progress state per its on_give_up_default), then deletes the
//     row claimant-guarded. Cascade FK on rimsky_claim_holders cleans
//     up held-claim rows.
//   - Visibility-timeout sweep walks every store's configured
//     pick_policies (per spec §12.12) and resets in_progress rows
//     whose claimed_at is older than the per-policy timeout AND for
//     which no rimsky_lock_holders row exists.

package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	pgstore "github.com/fallguy/rimsky/core/store/postgres"
)

// sweepLockHolders implements spec §13.5 orphan-reap. For each
// rimsky_lock_holders row whose expires_at < now():
//   - For lock_kind='region': open a tx, call Store.Abandon(region,
//     address, "") so the substrate undoes any in-progress state per
//     its on_give_up_default.
//   - For lock_kind='named': no store-side work.
//
// Then DELETE the lock-holder row claimant-guarded on
// holder_supervisor_id, emit a `lock_orphan_reaped` event, and COMMIT
// the transaction. Cascade FK cleans up rimsky_claim_holders rows.
// One tx per row so a single failure doesn't block the rest of the
// sweep.
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

// reapOneLockHolder runs the per-row §13.5 work in its own
// transaction.
func reapOneLockHolder(ctx context.Context, cfg Config, lh store.LockHolderRow, log shared.Logger) error {
	tx, err := cfg.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	txCtx := store.WithTx(ctx, tx)

	if lh.Kind == store.LockHolderKindRegion {
		if err := reapRegionRow(txCtx, cfg, lh); err != nil {
			return err
		}
	}

	if err := cfg.LockHolders.DeleteByID(ctx, tx, lh.ID, lh.HolderSupervisorID); err != nil {
		return fmt.Errorf("delete lock-holder row: %w", err)
	}

	if err := appendEventInTx(ctx, tx, lh.HolderNodeID, "lock_orphan_reaped", lockReapPayload(lh)); err != nil {
		log.Warn("tick: append lock_orphan_reaped failed",
			"lock_holder_id", lh.ID.String(), "error", err.Error())
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reap tx: %w", err)
	}
	return nil
}

// reapRegionRow fires Store.Abandon to let the substrate undo any
// in-progress state. The empty policyOverride lets the substrate apply
// its on_give_up_default.
func reapRegionRow(txCtx context.Context, cfg Config, lh store.LockHolderRow) error {
	if cfg.StoreRegistry == nil {
		return errors.New("region reap requires a store registry, none configured")
	}
	if lh.StoreName == nil {
		return errors.New("region lock-holder row missing store_name")
	}
	s, ok := cfg.StoreRegistry.GetStore(*lh.StoreName)
	if !ok {
		return fmt.Errorf("store %q not registered", *lh.StoreName)
	}
	if err := s.Abandon(txCtx, lh.RegionData, lh.Address, ""); err != nil {
		return fmt.Errorf("Store.Abandon: %w", err)
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

// visibilityTimeoutSweep implements spec §12.12 + §13.5 step 4. For
// each postgres store's configured pick_policies, reset items-table
// rows whose claimed_at + visibility_timeout < now() AND for which no
// rimsky_lock_holders row exists. The NOT EXISTS guard ensures the
// heartbeat-driven path always wins over the visibility-timeout
// backstop.
func visibilityTimeoutSweep(ctx context.Context, cfg Config, log shared.Logger) error {
	if cfg.StoreRegistry == nil {
		return nil
	}
	for storeName, s := range cfg.StoreRegistry.Stores() {
		ps, ok := s.(*pgstore.Store)
		if !ok {
			continue
		}
		for selector, pp := range ps.PickPolicies() {
			if pp.VisibilityTimeout <= 0 {
				continue
			}
			if err := sweepOnePickPolicy(ctx, cfg, storeName, selector, pp); err != nil {
				log.Warn("tick: visibility-timeout sweep failed",
					"store", storeName, "selector", selector,
					"items_table", pp.ItemsTable, "error", err.Error())
			}
		}
	}
	return nil
}

// sweepOnePickPolicy runs the §12.12 UPDATE for one pick policy. The
// items_table identifier is interpolated directly (validated as
// [a-z0-9_] at registry build time, see core/store/postgres/factory.go's
// validIdent).
func sweepOnePickPolicy(ctx context.Context, cfg Config, storeName, _ string, pp pgstore.PickPolicySnapshot) error {
	q := fmt.Sprintf(
		`UPDATE %s
		    SET state = 'available', claim_token = NULL, claimed_at = NULL
		  WHERE state = 'in_progress'
		    AND claimed_at < now() - ($1 * interval '1 second')
		    AND NOT EXISTS (
		          SELECT 1 FROM rimsky_lock_holders
		           WHERE store_name = $2
		             AND region_data = to_jsonb(%s.item_id::text)
		        )`,
		pp.ItemsTable, pp.ItemsTable,
	)
	secs := int(pp.VisibilityTimeout / time.Second)
	if _, err := cfg.Pool.Exec(ctx, q, secs, storeName); err != nil {
		return err
	}
	return nil
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
