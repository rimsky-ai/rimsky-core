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

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/protocols/claimproducer"
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
		if err := args.ClaimHandles.Delete(ctx, lk.ClaimHandleID, args.SupervisorID, tx); err != nil {
			return fmt.Errorf("releaseAcquiredLock: named Delete: %w", err)
		}
		return emitLockReleased(ctx, args, tx, acq, lk, releaseActionString(success))
	case claimproducer.ClaimSpec:
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
	acq *acquisition, lk AcquiredLock, claimSpec claimproducer.ClaimSpec, success bool,
) error {
	held := isAliasHeld(acq.HeldSubgraphs, acq.NodeType, claimSpec.Alias)
	if held {
		if err := markClaimHolderForRun(ctx, args, tx, lk.ClaimHandleID, acq.DispatchID, success); err != nil {
			return err
		}
		// Held-claim acquirer-failure semantics: when the acquirer
		// terminates with !success (resolve=pass / give_up / policy
		// failure), inheritor rows would otherwise stay 'active'
		// indefinitely because their nodes never get dispatched (no
		// prior frame leaves them stale). Fail every still-active row
		// so auto-terminal fires immediately and the rimsky_claim_handles
		// row is released. Spec §4.10 invariant 13 (auto-terminal,
		// single, aggregate-outcome-driven) requires the terminal to
		// fire at the first natural completion point; the acquirer's
		// failure IS that point for the entire held subgraph.
		if !success {
			if err := args.Persist.ClaimHolders().FailAllActiveByClaimHandle(ctx, lk.ClaimHandleID, args.SupervisorID, tx); err != nil {
				return fmt.Errorf("releaseClaim: fail inheritors: %w", err)
			}
		}
		if err := CheckAndFireResolution(ctx, args, tx, lk.ClaimHandleID); err != nil {
			return err
		}
		return emitLockReleased(ctx, args, tx, acq, lk, "held_marked")
	}
	row, err := args.ClaimHandles.Get(ctx, lk.ClaimHandleID, tx)
	if err != nil {
		return fmt.Errorf("releaseClaim: load scope/address: %w", err)
	}
	var (
		scope   []byte
		address []byte
	)
	if row != nil {
		scope = []byte(row.ClaimScopeData)
		address = []byte(row.Address)
	}
	verbAction := releaseActionString(success)
	outcome := AggregateCommit
	if !success {
		outcome = AggregateAbandon
	}
	// Build the lineage hint from the claim-handle row + the active
	// acquisition context. Used by the terminal-decision engine to
	// record the `claim_terminal` lineage row + claim_resolution event
	// per spec §Content lineage + the 2026-05-16 forensics extension.
	hint := ClaimLineageHint{
		InstanceID: acq.InstanceID,
		FrameID:    acq.FrameID,
		RunID:      acq.DispatchID,
		NodeID:     acq.NodeID,
	}
	if row != nil && row.ProducerName != nil {
		hint.ProducerName = *row.ProducerName
	}
	if row != nil {
		hint.VersionID = row.VersionID
	}
	var lifetime spec.ClaimLifetime
	var candidateHandle []byte
	producerName := ""
	var parentClaimHandleID *shared.UUID
	if row != nil {
		lifetime = row.Lifetime
		candidateHandle = row.ProducerCandidateHandle
		if row.ProducerName != nil {
			producerName = *row.ProducerName
		}
		parentClaimHandleID = row.ParentClaimHandleID
	}
	if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID:       lk.ClaimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              ActiveTerminal,
		Outcome:             outcome,
		Producer:            lk.Producer,
		Scope:               scope,
		Address:             address,
		Lifetime:            lifetime,
		CandidateHandle:     candidateHandle,
		ProducerName:        producerName,
		LineageHint:         hint,
		ParentClaimHandleID: parentClaimHandleID,
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
	inherited, err := findInheritedAliasesForRun(ctx, args, tx, acq.HeldSubgraphs, acq.NodeType, acq.DispatchID, acq.InstanceID)
	if err != nil {
		return err
	}
	for _, ia := range inherited {
		if err := markClaimHolderForRun(ctx, args, tx, ia.ClaimHandleID, acq.DispatchID, success); err != nil {
			return err
		}
		if err := CheckAndFireResolution(ctx, args, tx, ia.ClaimHandleID); err != nil {
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
		"holder_id":     lk.ClaimHandleID.String(),
		"supervisor_id": args.SupervisorID,
		"action":        action,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload["lock_kind"] = string(persistence.LockKindNamed)
		payload["lock_name"] = sp.Name
	case claimproducer.ClaimSpec:
		payload["lock_kind"] = string(persistence.LockKindScope)
		payload["producer_name"] = sp.ProducerName
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
