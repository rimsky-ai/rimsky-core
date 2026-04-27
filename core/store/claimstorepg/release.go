package claimstorepg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/store"
)

// releaseToHeadShift is the deterministic interval used to push a row to
// the front of the FIFO order on `release_to_head`. We move enqueued_at
// one year into the past, well clear of any wall-clock time the operator
// would naturally use; this guarantees the row sorts ahead of all other
// available rows on the next AcquireLock.
const releaseToHeadShift = "1 year"

// ReleaseLock honours the action by mapping it to a claim-store policy:
//
//   - ReleaseCommit / ReleaseDiscard: not the supervisor's normal release
//     path for claim stores — the supervisor calls the §5.6.4 resolution
//     algorithm via ResolveOnTerminal (see holders.go) for held claims, and
//     calls ReleaseClaimItem directly for non-held claim-and-forget.
//     Calling ReleaseLock here is a no-op for claim stores: the lock-holder
//     row is what bracketed the running window, and the supervisor's outer
//     tx already deletes it; there is nothing else to do at the
//     ReleaseAction granularity. Items-table mutations always go through
//     ReleaseClaimItem with an explicit action picked by the resolution
//     algorithm or the claim-and-forget defaults.
//
//   - ReleaseGiveUp: same as above when called from the normal commit path.
//     The §13.5 step-2 orphan-reaper also calls ReleaseLock(ReleaseGiveUp)
//     on a claim-kind lock-holder row whose `expires_at < now()`; in that
//     case the supervisor follows up with ResolveOnTerminal to flip the
//     items-table row. We keep ReleaseLock itself a no-op so the orphan
//     reaper doesn't double-flip when it then runs the resolution
//     algorithm.
//
//   - ReleasePreserveResume: no-op; the supervisor leaves the
//     rimsky_lock_holders row in place via the outer tx logic in §13.6.
//
// In short: ReleaseLock for claim stores is a no-op for all actions. The
// items-table mutation is owned by ReleaseClaimItem (claim-and-forget or
// last-released-wins resolution) and ResolveOnTerminal (the §5.6.4
// resolution algorithm). This split matches the spec's interface contract
// in §8.5.1: ClaimableStore.ReleaseClaimItem is the canonical items-table
// reposition entrypoint, separate from Store.ReleaseLock.
func (s *Store) ReleaseLock(_ context.Context, _ store.LockHandle, _ store.ReleaseAction) error {
	return nil
}

// ReleaseClaimItem performs the items-table reposition for the given claim
// ID per the supplied action. Action vocabulary per spec §5.6.4:
//
//   - 'release_to_back': set state='available', claim_token=NULL,
//     enqueued_at=now(). The row sorts to the back of the FIFO order.
//   - 'release_to_head': set state='available', claim_token=NULL,
//     enqueued_at=now() - 1 year. The row sorts to the front of the FIFO
//     order on the next AcquireLock.
//   - 'delete' / 'delete_won': the items-table row is gone; this method
//     should not be called with delete actions — the §5.6.4 algorithm
//     issues DELETE FROM <items_table> directly via ResolveOnTerminal.
//     We treat 'delete' / 'delete_won' here as an error to fail loudly
//     in case a caller routes them incorrectly.
//
// Reads the open tx via store.TxFromContext. Per spec §13.6 the items-
// table mutation runs inside the same tx as the lock-holder release and
// the §5.6.4 algorithm.
func (s *Store) ReleaseClaimItem(ctx context.Context, claimID string, action string) error {
	tx, ok := store.TxFromContext(ctx)
	if !ok {
		return fmt.Errorf("claim_store %q: ReleaseClaimItem requires an open pgx.Tx via store.WithTx", s.name)
	}

	switch action {
	case "release_to_back":
		return s.repositionToBack(ctx, tx, claimID)
	case "release_to_head":
		return s.repositionToHead(ctx, tx, claimID)
	case "delete", "delete_won":
		return fmt.Errorf("claim_store %q: ReleaseClaimItem called with action %q — delete is owned by the §5.6.4 resolution algorithm, not this method", s.name, action)
	default:
		return fmt.Errorf("claim_store %q: ReleaseClaimItem: unknown action %q", s.name, action)
	}
}

// repositionToBack flips the items-table row back to 'available' and
// stamps enqueued_at=now() so it sorts to the tail of the FIFO order.
func (s *Store) repositionToBack(ctx context.Context, tx pgx.Tx, claimID string) error {
	q := fmt.Sprintf(`UPDATE %s
		   SET state = 'available',
		       claim_token = NULL,
		       claimed_at = NULL,
		       enqueued_at = now()
		 WHERE item_id = $1`,
		s.itemsTable,
	)
	tag, err := tx.Exec(ctx, q, claimID)
	if err != nil {
		return fmt.Errorf("claim_store %q: release_to_back: %w", s.name, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("claim_store %q: release_to_back: no items-table row for claim_id=%s", s.name, claimID)
	}
	return nil
}

// repositionToHead flips the items-table row back to 'available' and
// pushes enqueued_at into the deep past so it sorts to the head of the
// FIFO order on the next AcquireLock.
func (s *Store) repositionToHead(ctx context.Context, tx pgx.Tx, claimID string) error {
	q := fmt.Sprintf(`UPDATE %s
		   SET state = 'available',
		       claim_token = NULL,
		       claimed_at = NULL,
		       enqueued_at = now() - INTERVAL '%s'
		 WHERE item_id = $1`,
		s.itemsTable, releaseToHeadShift,
	)
	tag, err := tx.Exec(ctx, q, claimID)
	if err != nil {
		return fmt.Errorf("claim_store %q: release_to_head: %w", s.name, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("claim_store %q: release_to_head: no items-table row for claim_id=%s", s.name, claimID)
	}
	return nil
}
