// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// CheckAndFireResolution implements the spec §4.10 invariant 13 algorithm: lock
// the rimsky_claim_handles row, check whether all rimsky_claim_holders
// rows for the lock-holder are non-active, compute aggregate outcome
// (any 'failed' → Abandon; else → Commit), and delegate to the unified
// terminal-decision engine in terminal_decision.go.
//
// Runs inside the caller's tx so the producer verb + the claim_handle
// delete + the cascade-cleared claim-holder rows commit atomically
// with whatever else the caller is mutating.
//
// Returns nil when the subgraph is not yet complete (some active
// rows remain) — the next terminating member will re-check.
//
// Producer-verb / commit-failure leak path: the producer verb fires
// over the wire BEFORE the surrounding rimsky tx commits. If the
// verb succeeds but the rimsky tx then fails to commit (rare — Postgres
// connection drop between verb-return and Commit), the next sibling-
// node terminal re-enters this function with the claim_handle row
// still present and will fire the verb a second time. This is safe
// because of foundation contract §4.4 / spec §7.8 obligation #3:
// terminal verbs MUST be idempotent in `claim_id`. The second call
// is a no-op.
//
// Phase-6 unification: the body of this function is the held-terminal
// detection logic; the actual verb-fire + row-delete sequence
// delegates to ResolveClaimHandleTerminal.
func CheckAndFireResolution(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	claimHandleID shared.UUID,
) error {
	row, err := args.ClaimHandles.LockForUpdate(ctx, claimHandleID, tx)
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

	holders, err := args.Persist.ClaimHolders().ListByClaimHandleID(ctx, claimHandleID, tx)
	if err != nil {
		return fmt.Errorf("CheckAndFireResolution: ListByClaimHandleID: %w", err)
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

	producerName := ""
	if row.ProducerName != nil {
		producerName = *row.ProducerName
	}
	producer, ok := args.StoreRegistry.Get(producerName)
	if !ok {
		return fmt.Errorf("CheckAndFireResolution: unknown producer %q", producerName)
	}
	outcome := AggregateCommit
	if anyFailed {
		outcome = AggregateAbandon
	}
	if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID: claimHandleID,
		SupervisorID:  args.SupervisorID,
		Source:        HeldTerminal,
		Outcome:       outcome,
		Producer:      producer,
		Scope:         []byte(row.ScopeData),
		Address:       []byte(row.Address),
	}); err != nil {
		return fmt.Errorf("CheckAndFireResolution: %w", err)
	}
	return nil
}

// (lockClaimHandleRow + scanClaimHandleForResolution were retired when
// the persistence layer landed `ClaimHandleTable.LockForUpdate`. The
// auto-terminal flow above calls that method directly.)
