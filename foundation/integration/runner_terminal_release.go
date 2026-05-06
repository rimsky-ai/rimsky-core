// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Per-acquired-lock release dispatch invoked from the terminal handlers.
// Split out of runner_terminal.go to keep that file under the cold-read
// 500-line guideline. The release branch has three shapes:
//
//   - NamedLockSpec → claimant-guarded delete.
//   - ClaimSpec acquirer + held → mark this node's claim_holders row,
//     fail still-active inheritor rows on !success, fire CheckAndFire-
//     Resolution.
//   - ClaimSpec acquirer + non-held → call ResolveClaimHandleTerminal
//     (Commit on success, Abandon on failure) and delete the lock-
//     holder row.
//
// The inheritor branch (releaseInheritedClaimsInTx) marks claim_holders
// rows for non-acquirer members of a holding subgraph and lets auto-
// terminal fire the store verb.

package integration

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
)

// releaseLocksInTx is the release-tx body. Walks the acquired-locks
// slice in sort order. For each lock:
//
//   - NamedLockSpec → claimant-guarded delete.
//   - ClaimSpec acquirer + held → mark this node's claim_holders row
//     'completed'/'failed', call CheckAndFireResolution.
//   - ClaimSpec acquirer + non-held → call the store verb directly
//     (success → Commit; failure → Abandon), delete the lock-holder
//     row.
//
// Per spec §7.3 the store's verb runs in its own (store-side)
// transaction; rimsky's bookkeeping tx commits the lock-holder DELETE
// independently. At-least-once delivery + claim_id idempotency on the
// store side handles transient failures (per spec §7.8 obligation
// #3).
//
// The inheritor branch is handled by releaseInheritedClaimsInTx, run
// from the same tx.
func releaseLocksInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
) error {
	for _, lk := range acq.Locks {
		if err := releaseAcquiredLock(ctx, args, tx, acq, lk, success); err != nil {
			return err
		}
	}
	return releaseInheritedClaimsInTx(ctx, args, tx, acq, success)
}

// releaseAcquiredLock dispatches one acquired lock to the right
// release branch. Reuses the caller's `tx` for the lock_released event
// append — `releaseAcquiredLock` is called from inside an open
// `Persist.Transaction(...)` and opening a fresh inner tx would self-
// deadlock under the SQLite single-conn pool (and tie up two pool
// connections under postgres).
func releaseAcquiredLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, success bool,
) error {
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		_ = sp
		if err := args.LockHolders.Delete(ctx, lk.LockHolderID, args.SupervisorID, tx); err != nil {
			return fmt.Errorf("releaseAcquiredLock: named Delete: %w", err)
		}
		return emitLockReleased(ctx, args, tx, acq, lk, releaseActionString(success))
	case locks.ClaimSpec:
		return releaseClaim(ctx, args, tx, acq, lk, sp, success)
	}
	return fmt.Errorf("releaseAcquiredLock: unknown spec %T", lk.Spec)
}

// releaseClaim handles the per-ClaimSpec release-path branching
// (held vs. non-held). For non-held claims, scope and address are
// read from the lock-holder row so the store verb receives the
// canonical bytes regardless of whether `lk.ClaimResult` survived an
// async-callback round-trip. Store disposition (what Commit /
// Abandon mean for the store's own state) is governed entirely
// by per-store config; rimsky carries only the success/failure
// binary.
func releaseClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, spec locks.ClaimSpec, success bool,
) error {
	held := isAliasHeld(acq.HeldSubgraphs, acq.NodeType, spec.Alias)
	if held {
		if err := markClaimHolderForNode(ctx, args, tx, lk.LockHolderID, acq.NodeID, success); err != nil {
			return err
		}
		// Held-claim acquirer-failure semantics: when the acquirer
		// terminates with !success (resolve=pass / give_up / policy
		// failure), inheritor rows would otherwise stay 'active'
		// indefinitely because their nodes never get dispatched (no
		// prior frame leaves them stale). Fail every still-active row
		// so auto-terminal fires immediately and the rimsky_claim_handle
		// row is released. Spec §4.10 invariant 13 (auto-terminal,
		// single, aggregate-outcome-driven) requires the terminal to
		// fire at the first natural completion point; the acquirer's
		// failure IS that point for the entire held subgraph.
		if !success {
			if err := args.Persist.ClaimHolders().FailAllActiveByLockHolder(ctx, lk.LockHolderID, args.SupervisorID, tx); err != nil {
				return fmt.Errorf("releaseClaim: fail inheritors: %w", err)
			}
		}
		if err := CheckAndFireResolution(ctx, args, tx, lk.LockHolderID); err != nil {
			return err
		}
		return emitLockReleased(ctx, args, tx, acq, lk, "held_marked")
	}
	row, err := args.LockHolders.Get(ctx, lk.LockHolderID, tx)
	if err != nil {
		return fmt.Errorf("releaseClaim: load scope/address: %w", err)
	}
	var (
		scope   []byte
		address []byte
	)
	if row != nil {
		scope = []byte(row.ScopeData)
		address = []byte(row.Address)
	}
	verbAction := releaseActionString(success)
	outcome := AggregateCommit
	if !success {
		outcome = AggregateAbandon
	}
	if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID: lk.LockHolderID,
		SupervisorID:  args.SupervisorID,
		Source:        ActiveTerminal,
		Outcome:       outcome,
		Producer:      lk.Store,
		Scope:         scope,
		Address:       address,
	}); err != nil {
		return fmt.Errorf("releaseClaim: %w", err)
	}
	return emitLockReleased(ctx, args, tx, acq, lk, verbAction)
}

// releaseInheritedClaimsInTx walks the precomputed holding-subgraph
// metadata and, for each subgraph this node is a non-acquirer
// member of, marks the inheritor's claim_holders row and calls
// CheckAndFireResolution. The auto-terminal mechanism handles the
// store verb.
func releaseInheritedClaimsInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
) error {
	inherited, err := findInheritedAliasesForNode(ctx, args, tx, acq.HeldSubgraphs, acq.NodeType, acq.NodeID, acq.InstanceID)
	if err != nil {
		return err
	}
	for _, ia := range inherited {
		if err := markClaimHolderForNode(ctx, args, tx, ia.LockHolderID, acq.NodeID, success); err != nil {
			return err
		}
		if err := CheckAndFireResolution(ctx, args, tx, ia.LockHolderID); err != nil {
			return err
		}
	}
	return nil
}

// releaseActionString maps success bool → event payload string.
// Named locks have no store verb so we synthesize "release" /
// "release_failed" labels for the audit trail.
func releaseActionString(success bool) string {
	if success {
		return "release"
	}
	return "release_failed"
}

// emitLockReleased emits the per-spec lock_released event using the
// caller's already-open `tx`. Required to avoid nested transactions:
// every callsite of emitLockReleased runs inside an open
// `Persist.Transaction(...)` (the §7.6 release tx) — opening a fresh
// inner tx here would self-deadlock under the SQLite single-conn pool
// (and tie up two pool connections under postgres). Returns the
// Append error so the caller can roll back the release tx if event
// persistence fails.
func emitLockReleased(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, action string,
) error {
	payload := map[string]any{
		"holder_id":     lk.LockHolderID.String(),
		"supervisor_id": args.SupervisorID,
		"action":        action,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload["lock_kind"] = string(persistence.LockKindNamed)
		payload["lock_name"] = sp.Name
	case locks.ClaimSpec:
		payload["lock_kind"] = string(persistence.LockKindScope)
		payload["store_name"] = sp.StoreName
		payload["alias"] = sp.Alias
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "lock_released", Payload: payload,
	}, tx); err != nil {
		return fmt.Errorf("emitLockReleased: %w", err)
	}
	return nil
}
