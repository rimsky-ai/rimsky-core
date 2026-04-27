// Auto-terminal mechanism (spec §14.4).
//
// At a held claim's holding-subgraph completion, the supervisor fires
// the substrate verb declared in the acquirer's claim_resolutions and
// deletes the lock-holder row. Race-safe via SELECT … FOR UPDATE on
// the lock-holder row plus a state='active' filter on the
// claim-holders rows: concurrent terminations on the same subgraph
// see the row already locked / already deleted and no-op.

package supervisor

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
)

// CheckAndFireResolution implements the spec §14.4 algorithm: lock
// the rimsky_lock_holders row, check whether all rimsky_claim_holders
// rows for the lock-holder are non-active, compute aggregate outcome
// (any 'failed' → on_give_up; else → on_commit), route the substrate
// verb per §14.4.1, and delete the lock-holder row claimant-guarded.
//
// Runs inside the caller's tx so the substrate verb + the lock-holder
// delete + the cascade-cleared claim-holder rows commit atomically
// with whatever else the caller is mutating.
//
// Returns nil when the subgraph is not yet complete (some active
// rows remain) — the next terminating member will re-check.
func CheckAndFireResolution(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	lockHolderID shared.UUID, alias string,
	claimResolutions map[string]node.ClaimResolution,
) error {
	row, err := lockLockHolderRow(ctx, tx, lockHolderID)
	if err != nil {
		return err
	}
	if row == nil {
		// Already deleted by a concurrent termination on the same
		// subgraph (race-safe per §14.4.2).
		return nil
	}
	if row.HolderSupervisorID != args.SupervisorID {
		// Acquirer-supervisor crash case; orphan reaper handles it.
		return nil
	}

	stx := pgstorage.WrapPgxTx(tx)
	holders, err := args.Storage.ClaimHolders().ListByLockHolderID(ctx, lockHolderID, stx)
	if err != nil {
		return fmt.Errorf("CheckAndFireResolution: ListByLockHolderID: %w", err)
	}
	if len(holders) == 0 {
		// No claim-holder rows — non-held claim. Caller should not
		// invoke this function for non-held claims, but tolerate it.
		return nil
	}
	anyActive, anyFailed := false, false
	for _, h := range holders {
		switch h.State {
		case storage.ClaimHolderStateActive:
			anyActive = true
		case storage.ClaimHolderStateFailed:
			anyFailed = true
		}
	}
	if anyActive {
		return nil
	}

	resolution := claimResolutions[alias]
	verbAction, success := selectResolutionAction(resolution, !anyFailed)
	region := []byte(row.RegionData)
	address := []byte(row.Address)
	storeName := ""
	if row.StoreName != nil {
		storeName = *row.StoreName
	}
	s, ok := args.StoreRegistry.GetStore(storeName)
	if !ok {
		return fmt.Errorf("CheckAndFireResolution: unknown store %q", storeName)
	}
	storeCtx := store.WithTx(ctx, tx)
	if err := fireResolutionVerb(storeCtx, s, verbAction, success, region, address); err != nil {
		return fmt.Errorf("CheckAndFireResolution: substrate verb (%s): %w", verbAction, err)
	}

	if err := args.LockHolders.DeleteByID(ctx, tx, lockHolderID, args.SupervisorID); err != nil {
		return fmt.Errorf("CheckAndFireResolution: DeleteByID: %w", err)
	}
	return nil
}

// selectResolutionAction chooses the action vocabulary per §14.4.1.
// Returns (verbAction, successPath). The verbAction passes through
// to the substrate as policyOverride (or empty for default verbs).
func selectResolutionAction(r node.ClaimResolution, success bool) (string, bool) {
	if success {
		if r.OnCommit == "" {
			return "commit", true
		}
		return r.OnCommit, true
	}
	if r.OnGiveUp == "" {
		return "abandon", false
	}
	return r.OnGiveUp, false
}

// fireResolutionVerb maps the action vocabulary to the substrate
// verb call per §14.4.1's routing table.
func fireResolutionVerb(
	ctx context.Context, s store.Store,
	action string, success bool, region, address []byte,
) error {
	switch action {
	case "commit":
		return s.Commit(ctx, region, address, "")
	case "abandon":
		return s.Abandon(ctx, region, address, "")
	case "delete":
		return s.Delete(ctx, region)
	case "release_to_back", "release_to_head":
		if success {
			return s.Commit(ctx, region, address, action)
		}
		return s.Abandon(ctx, region, address, action)
	}
	return fmt.Errorf("fireResolutionVerb: unknown action %q", action)
}

// lockLockHolderRow does SELECT … FOR UPDATE on a lock-holder row.
// Returns (nil, nil) when the row is gone (already deleted by a
// concurrent termination).
func lockLockHolderRow(ctx context.Context, tx pgx.Tx, id shared.UUID) (*store.LockHolderRow, error) {
	const cols = `id, lock_kind, lock_name, store_name, region_data, address, intent,
		holder_supervisor_id, holder_node_id,
		claimed_at, last_heartbeat_at, expires_at, frame_id`
	row := tx.QueryRow(ctx,
		`SELECT `+cols+` FROM rimsky_lock_holders WHERE id = $1 FOR UPDATE`, id,
	)
	out, err := scanLockHolderForResolution(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lockLockHolderRow: %w", err)
	}
	return &out, nil
}

// scanLockHolderForResolution mirrors core/store/lockholders.go's
// scanLockHolder; duplicated here to avoid exporting the helper.
// @source: core/store/lockholders.go:scanLockHolder
func scanLockHolderForResolution(sc interface{ Scan(...any) error }) (store.LockHolderRow, error) {
	var (
		r          store.LockHolderRow
		kind       string
		lockName   *string
		storeName  *string
		regionData []byte
		address    []byte
		intent     *string
		frameID    *shared.UUID
	)
	if err := sc.Scan(
		&r.ID, &kind,
		&lockName, &storeName, &regionData, &address, &intent,
		&r.HolderSupervisorID, &r.HolderNodeID,
		&r.ClaimedAt, &r.LastHeartbeatAt, &r.ExpiresAt, &frameID,
	); err != nil {
		return store.LockHolderRow{}, err
	}
	r.Kind = store.LockHolderKind(kind)
	r.LockName = lockName
	r.StoreName = storeName
	r.RegionData = regionData
	r.Address = address
	r.Intent = intent
	r.FrameID = frameID
	return r, nil
}
