// Package scheduler — §13.5 step 2/3/4 sweeps. Extracted out of
// scheduler.go so the main tick loop stays close to the ~500-line cold-read
// guideline; these three sweeps are tightly bound to the claim-store
// vocabulary (§5.6.4 resolution algorithm + §7.7 visibility timeout) and
// share helpers, so they live together here.
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
	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

// sweepLockHolders implements spec §13.5 step 2. For each
// `rimsky_lock_holders` row whose `expires_at < now()`:
//
//   - For lock_kind='claim': open a tx, call Store.ReleaseLock(tx,
//     lh, ReleaseGiveUp) so the items-table row goes back to
//     state='available'. If a `rimsky_claim_holders` row is still active for
//     this (claim_id, holder_node_id), run the §5.6.4 resolution algorithm
//     with actual_action = on_give_up.
//   - For lock_kind='region' / 'named': no store-side work.
//
// Then DELETE the lock-holder row claimant-guarded on holder_supervisor_id,
// emit a `lock_orphan_reaped` event, and COMMIT the transaction. One tx per
// row so a single failure doesn't block the rest of the sweep.
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

// reapOneLockHolder runs the per-row §13.5 step-2 work in its own
// transaction.
func reapOneLockHolder(ctx context.Context, cfg Config, lh store.LockHolderRow, log shared.Logger) error {
	tx, err := cfg.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Stash the open tx on the context so claim-store calls
	// (ReleaseLock, ResolveOnTerminal) participate in the same
	// transaction (see core/store/tx.go).
	txCtx := store.WithTx(ctx, tx)

	if lh.Kind == store.LockHolderKindClaim {
		if err := releaseExpiredClaimHolder(txCtx, cfg, lh); err != nil {
			return err
		}
	}

	// DELETE claimant-guarded on holder_supervisor_id.
	if err := cfg.LockHolders.DeleteByID(ctx, tx, lh.ID, lh.HolderSupervisorID); err != nil {
		return fmt.Errorf("delete lock-holder row: %w", err)
	}

	// Emit a lock_orphan_reaped event. We use the same tx so the event
	// commits with the delete; on a downstream failure the rollback drops
	// both. The event append is best-effort — a failure here should not
	// block the row reap, so we log and continue.
	if err := appendEventInTx(ctx, tx, lh.HolderNodeID, "lock_orphan_reaped", lockReapPayload(lh)); err != nil {
		log.Warn("tick: append lock_orphan_reaped failed",
			"lock_holder_id", lh.ID.String(), "error", err.Error())
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reap tx: %w", err)
	}
	return nil
}

// lockReapPayload builds the structured payload for the lock_orphan_reaped
// event. Conditional fields (lock_name, store_name, claim_id) are only
// populated when the row's `lock_kind` constraint admits them (§9.9.2 CHECK
// constraint).
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
	if lh.ClaimID != nil {
		payload["claim_id"] = *lh.ClaimID
	}
	return payload
}

// releaseExpiredClaimHolder runs the claim-kind half of §13.5 step 2:
// Store.ReleaseLock(tx, lh, ReleaseGiveUp) followed by §5.6.4 resolution if
// a `rimsky_claim_holders` row is still active.
func releaseExpiredClaimHolder(txCtx context.Context, cfg Config, lh store.LockHolderRow) error {
	if cfg.StoreRegistry == nil {
		return errors.New("claim-kind reap requires a store registry, none configured")
	}
	if lh.StoreName == nil || lh.ClaimID == nil {
		return errors.New("claim-kind lock-holder row missing store_name or claim_id")
	}
	s, ok := cfg.StoreRegistry.GetStore(*lh.StoreName)
	if !ok {
		return fmt.Errorf("store %q not registered", *lh.StoreName)
	}

	// Synthesize the LockHandle expected by Store.ReleaseLock from the row.
	handle := store.LockHandle{
		ID:           lh.ID.String(),
		Kind:         string(lh.Kind),
		StoreName:    *lh.StoreName,
		HolderNodeID: lh.HolderNodeID.String(),
		SupervisorID: lh.HolderSupervisorID,
		AcquiredAt:   lh.ClaimedAt,
		ExpiresAt:    lh.ExpiresAt,
	}
	if err := s.ReleaseLock(txCtx, handle, store.ReleaseGiveUp); err != nil {
		return fmt.Errorf("Store.ReleaseLock: %w", err)
	}

	// Drive §5.6.4 if a held-claim row is still active for this (claim_id,
	// holder_node_id). For non-claim_store stores this branch is moot —
	// `rimsky_claim_holders` is only populated by claim_store flows — but
	// the type assertion below makes that explicit.
	cs, ok := s.(*claimstorepg.Store)
	if !ok {
		return nil
	}
	return cs.ResolveOnTerminal(txCtx, *lh.ClaimID, lh.HolderNodeID.String(), claimstorepg.TerminalGiveUp)
}

// claimHolderGC implements §13.5 step 3. Find `rimsky_claim_holders` rows
// whose `holder_node_id`'s state is `failed` or `fresh` AND whose `state` is
// still `'active'` — these are leaked holders. Run §5.6.4 with
// actual_action = on_give_up to drain them.
//
// One tx per leaked row so a single failure doesn't block the rest of the
// pass.
func claimHolderGC(ctx context.Context, cfg Config, log shared.Logger) error {
	leaked, err := listLeakedClaimHolders(ctx, cfg)
	if err != nil {
		return err
	}
	for _, lh := range leaked {
		if err := resolveLeakedHolder(ctx, cfg, lh.claimID, lh.storeName, lh.holderNodeID); err != nil {
			log.Warn("tick: resolve leaked claim-holder failed",
				"claim_id", lh.claimID,
				"store_name", lh.storeName,
				"holder_node_id", lh.holderNodeID.String(),
				"error", err.Error())
		}
	}
	return nil
}

// leakedClaimHolder is the §13.5 step-3 lookup result.
type leakedClaimHolder struct {
	claimID      string
	storeName    string
	holderNodeID shared.UUID
}

// listLeakedClaimHolders returns the active claim-holder rows whose holder
// node has terminated (failed or fresh) — leaked-resolution survivors.
func listLeakedClaimHolders(ctx context.Context, cfg Config) ([]leakedClaimHolder, error) {
	rows, err := cfg.Pool.Query(ctx,
		`SELECT ch.claim_id, ch.store_name, ch.holder_node_id
		   FROM rimsky_claim_holders ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE ch.state = 'active'
		    AND n.state IN ('failed', 'fresh')`,
	)
	if err != nil {
		return nil, fmt.Errorf("tick: list leaked claim-holders: %w", err)
	}
	defer rows.Close()
	var out []leakedClaimHolder
	for rows.Next() {
		var lh leakedClaimHolder
		if err := rows.Scan(&lh.claimID, &lh.storeName, &lh.holderNodeID); err != nil {
			return nil, fmt.Errorf("tick: scan leaked claim-holder: %w", err)
		}
		out = append(out, lh)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tick: iter leaked claim-holders: %w", err)
	}
	return out, nil
}

// resolveLeakedHolder opens a tx, runs §5.6.4 with TerminalGiveUp, and
// commits. The §5.6.4 algorithm is a no-op for already-completed rows so
// re-running on the same row is harmless.
func resolveLeakedHolder(ctx context.Context, cfg Config, claimID, storeName string, holderNodeID shared.UUID) error {
	if cfg.StoreRegistry == nil {
		return errors.New("claim-holder GC requires a store registry, none configured")
	}
	s, ok := cfg.StoreRegistry.GetStore(storeName)
	if !ok {
		return fmt.Errorf("store %q not registered", storeName)
	}
	cs, ok := s.(*claimstorepg.Store)
	if !ok {
		return fmt.Errorf("store %q is not a claim_store", storeName)
	}

	tx, err := cfg.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	txCtx := store.WithTx(ctx, tx)
	if err := cs.ResolveOnTerminal(txCtx, claimID, holderNodeID.String(), claimstorepg.TerminalGiveUp); err != nil {
		return fmt.Errorf("ResolveOnTerminal: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit gc tx: %w", err)
	}
	return nil
}

// visibilityTimeoutSweep implements §13.5 step 4 + §7.7. For each
// `claim-store-postgres` store in the local registry, reset items-table
// rows whose `claimed_at + visibility_timeout < now()` AND for which no
// `rimsky_lock_holders` row exists. The NOT EXISTS guard ensures the
// heartbeat-driven path always wins over the visibility-timeout backstop.
func visibilityTimeoutSweep(ctx context.Context, cfg Config, log shared.Logger) error {
	for name, s := range cfg.StoreRegistry.Stores() {
		cs, ok := s.(*claimstorepg.Store)
		if !ok {
			continue
		}
		visTimeout := cs.VisibilityTimeout()
		if visTimeout <= 0 {
			continue
		}
		// We interpolate the items-table identifier directly (factory
		// validates it as [a-z0-9_] at registry build time, see
		// claimstorepg.validIdent). The store name and timeout-seconds go in
		// as parameters.
		q := fmt.Sprintf(
			`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL
			  WHERE state = 'in_progress'
			    AND claimed_at < now() - ($1 * interval '1 second')
			    AND NOT EXISTS (
			          SELECT 1 FROM rimsky_lock_holders
			           WHERE store_name = $2
			             AND claim_id = %s.item_id::text
			        )`,
			cs.ItemsTable(), cs.ItemsTable(),
		)
		secs := int(visTimeout / time.Second)
		if _, err := cfg.Pool.Exec(ctx, q, secs, name); err != nil {
			log.Warn("tick: visibility-timeout sweep failed",
				"store", name, "items_table", cs.ItemsTable(), "error", err.Error())
		}
	}
	return nil
}

// @source: core/storage/postgres/events.go:Append (inlined for in-tx event emission during sweep)
//
// appendEventInTx writes a row directly into rimsky_events inside the
// supplied tx. We bypass storage.EventStore because the storage interface
// does not expose a *pgx.Tx-aware Append for non-storage callers; the SQL
// is trivial and matches the §9.8 schema. Payload is JSON-marshalled here
// so pgx writes a jsonb-compatible bytes value.
//
// instance_id is looked up from rimsky_nodes via a subquery so the event
// row is anchored to the same instance as the node it describes (matching
// EventStore.Append's behaviour, which expects the caller to thread the
// instance id through). A node row that has been deleted between the sweep
// listing and this insert yields a NULL instance_id, which the events
// schema permits (§9.8).
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
