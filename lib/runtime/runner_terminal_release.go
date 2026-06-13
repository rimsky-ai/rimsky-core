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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
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
// `retainLinkedSubClaims` MUST be true when the caller is about to
// re-dispatch this run into the same RunScope (retry / infra-reenqueue
// dispositions). The linked sub-claim rows are parent-owned (opened at
// `AcquireSubClaims`, not by this leaf), so they must survive the
// round-trip: resolving them at a non-final terminal Abandons the
// child's partition, lets `SettleChildren` settle the parent and close
// the partition RunScope, and the retry enqueue then dead-ends on the
// closed scope — the retry never runs (regression pin:
// test/scenarios/fanout_child_error_retry_e2e_test.go). Property
// protected: a retry-flavored disposition is not a final terminal for
// the leaf's linked sub-claims.
func releaseLocksInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition,
	success bool, retainLinkedSubClaims bool,
) error {
	for _, lk := range acq.Locks {
		if err := releaseAcquiredLock(ctx, args, tx, acq, lk, success); err != nil {
			return err
		}
	}
	if !retainLinkedSubClaims {
		if err := resolveLinkedSubClaimsInTx(ctx, args, tx, acq, success); err != nil {
			return err
		}
	}
	return releaseInheritedClaimsInTx(ctx, args, tx, acq, success)
}

// resolveLinkedSubClaimsInTx resolves the fan-out sub-claim rows linked
// to this run. A fan-out leaf's sub-claim row was INSERTed at the
// PARENT's acquisition (`AcquireSubClaims`) and repointed to this leaf
// run by `DispatchChildren` — it is NOT in `acq.Locks` (the leaf's own
// freshly-Open'd claims), so the per-lock release walk above never
// sees it. Without this walk a non-held leaf's sub-claim row stays
// 'active' past the leaf terminal: the parent's children-settlement
// never fires, the parent claim handle is never Commit/Abandon'd per
// its aggregate, and both eventually fall to the orphan reaper as
// spurious Abandons — the claim chain's contract ("at the parent run's
// aggregated terminal it fires ClaimProducer.Commit(parent_handle_id)")
// silently never holds.
//
// Each linked sub-claim resolves through the unified engine with the
// leaf's own success/failure binary; the engine fires the producer's
// per-child verb (Commit on success — whose response body's
// producer_metadata then surfaces in the parent's writeback — or
// Abandon on failure), promotes the row, and recurses into
// `SettleChildren` so the parent settles once every sibling has
// resolved (load-bearing: the chain walk is what delivers the parent's
// aggregate-outcome producer verb at all).
//
// Held sub-claims (is_held=true, inherited from a held parent claim)
// are skipped: they persist past the leaf's active terminal until the
// holding subgraph completes, per the held-claim lifecycle.
// Already-resolved rows (state != active) are skipped for idempotency
// under terminal retry.
//
// @concept: claim-tree
// @concept: fan-out
func resolveLinkedSubClaimsInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
) error {
	if args.ClaimHandles == nil {
		return nil
	}
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, acq.DispatchID, tx)
	if err != nil {
		return fmt.Errorf("resolveLinkedSubClaims: ListByNodeRun: %w", err)
	}
	// The leaf's own freshly-Open'd claims were already released by the
	// acq.Locks walk; exclude them by id so a future row shape change
	// cannot double-resolve.
	released := make(map[shared.UUID]bool, len(acq.Locks))
	for _, lk := range acq.Locks {
		released[lk.ClaimHandleID] = true
	}
	for i := range rows {
		row := rows[i]
		if row.ParentClaimHandleID == nil || released[row.ID] {
			continue
		}
		if row.IsHeld || row.State != spec.ClaimHandleStateActive {
			continue
		}
		producerName := ""
		if row.ProducerName != nil {
			producerName = *row.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
		if !ok {
			return fmt.Errorf("resolveLinkedSubClaims: unknown producer %q for sub-claim %s", producerName, row.ID)
		}
		outcome := AggregateCommit
		if !success {
			outcome = AggregateAbandon
		}
		hint := ClaimLineageHint{
			InstanceID:   acq.InstanceID,
			FrameID:      acq.FrameID,
			RunID:        acq.DispatchID,
			NodeID:       acq.NodeID,
			ProducerName: producerName,
			VersionID:    row.VersionID,
		}
		if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
			ClaimHandleID:       row.ID,
			SupervisorID:        args.SupervisorID,
			Source:              ActiveTerminal,
			Outcome:             outcome,
			Producer:            producer,
			Scope:               []byte(row.ClaimScopeData),
			Address:             []byte(row.Address),
			Lifetime:            row.Lifetime,
			CandidateHandle:     row.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: row.ParentClaimHandleID,
		}); err != nil {
			return fmt.Errorf("resolveLinkedSubClaims: sub-claim %s: %w", row.ID, err)
		}
	}
	return nil
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
		Kind: events.KindLockReleased(), Payload: payload,
	}, tx); err != nil {
		return fmt.Errorf("emitLockReleased: %w", err)
	}
	return nil
}
