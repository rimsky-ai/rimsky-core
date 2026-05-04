// Auto-terminal mechanism (spec §4.10 invariant 13, as amended by
// docs/history/2026-04-30-stores-protocol-cleanup-design.md).
//
// At a held claim's holding-subgraph completion, the supervisor fires
// exactly one store verb based on aggregate outcome — Commit if
// every claim-holder reached `'completed'`, Abandon if any reached
// `'failed'` — then deletes the lock-holder row. The store decides
// what Commit / Abandon mean for its own state per its own
// configuration; rimsky carries only the success/failure binary.
// Race-safe via SELECT … FOR UPDATE on the lock-holder row plus a
// state='active' filter on the claim-holders rows: concurrent
// terminations on the same subgraph see the row already locked /
// already deleted and no-op.

package integration

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// CheckAndFireResolution implements the spec §4.10 invariant 13 algorithm: lock
// the rimsky_lock_holders row, check whether all rimsky_claim_holders
// rows for the lock-holder are non-active, compute aggregate outcome
// (any 'failed' → Abandon; else → Commit), fire that store verb,
// and delete the lock-holder row claimant-guarded.
//
// Runs inside the caller's tx so the store verb + the lock-holder
// delete + the cascade-cleared claim-holder rows commit atomically
// with whatever else the caller is mutating.
//
// Returns nil when the subgraph is not yet complete (some active
// rows remain) — the next terminating member will re-check.
//
// Store-verb / commit-failure leak path: the store verb fires
// over the wire BEFORE the surrounding rimsky tx commits. If the
// store verb succeeds but the rimsky tx then fails to commit
// (rare — Postgres connection drop between verb-return and Commit),
// the next sibling-node terminal re-enters this function with the
// lock-holder row still present and will fire the verb a second time.
// This is safe because of v3 spec §7.8 obligation #3: terminal verbs
// MUST be idempotent in `claim_id`. The second call is a no-op.
func CheckAndFireResolution(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	lockHolderID shared.UUID,
) error {
	row, err := args.LockHolders.LockForUpdate(ctx, lockHolderID, tx)
	if err != nil {
		return err
	}
	if row == nil {
		// Already deleted by a concurrent termination on the same
		// subgraph (race-safe per §4.10 invariant 13.2).
		return nil
	}
	if row.HolderSupervisorID != args.SupervisorID {
		// UUID re-use case (defensive: should be impossible given
		// UUID v4). Not the acquirer-supervisor-crash case — the
		// orphan reaper deletes the row outright, so a crashed
		// supervisor's row would have been LockForUpdate'd nil
		// above, not surfaced with a mismatching holder id.
		return nil
	}

	holders, err := args.Persist.ClaimHolders().ListByLockHolderID(ctx, lockHolderID, tx)
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
		case persistence.ClaimHolderStateActive:
			anyActive = true
		case persistence.ClaimHolderStateFailed:
			anyFailed = true
		}
	}
	if anyActive {
		return nil
	}

	scope := []byte(row.ScopeData)
	address := []byte(row.Address)
	storeName := ""
	if row.StoreName != nil {
		storeName = *row.StoreName
	}
	s, ok := args.StoreRegistry.Get(storeName)
	if !ok {
		return fmt.Errorf("CheckAndFireResolution: unknown store %q", storeName)
	}
	claimID := locks.ClaimID(lockHolderID.String())
	var verbErr error
	if anyFailed {
		verbErr = s.Abandon(ctx, claimID, scope, address)
	} else {
		verbErr = s.Commit(ctx, claimID, scope, address)
	}
	if verbErr != nil {
		return fmt.Errorf("CheckAndFireResolution: store verb: %w", verbErr)
	}

	if err := args.LockHolders.Delete(ctx, lockHolderID, args.SupervisorID, tx); err != nil {
		return fmt.Errorf("CheckAndFireResolution: Delete: %w", err)
	}
	return nil
}

// (lockLockHolderRow + scanLockHolderForResolution were retired when
// the persistence layer landed `LockHoldersStore.LockForUpdate`. The
// auto-terminal flow above calls that method directly.)
