// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// terminal_decision_cancel.go — strict-cancel sibling + descendant
// walkers split out of `runner_acquire.go` pattern; companion to
// `terminal_decision.go`. Contains the two recursive force-Abandon
// helpers invoked from the terminal-decision engine.
//
// Both helpers preserve `@blessed-invariant 4` (claimant-guarded
// release): mismatched-supervisor rows are skipped because a
// force-Abandon on someone else's claim would corrupt the producer's
// claim_id-keyed state.

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
)

// cancelInFlightSiblings implements the `strict.cancel_siblings: true`
// proactive cancellation walk. Reads the parent's snapshotted
// aggregation policy; if it declares `strict` + `cancel_siblings: true`,
// walks the parent's other sub-claim children and force-Abandons each
// in-flight sibling via a recursive `ResolveClaimHandleTerminal` call.
//
// Filters applied to each sibling row:
//
//   - triggering child (`triggerID`) is skipped — it's already resolving.
//   - non-active siblings (`State != ClaimHandleStateActive`) are
//     skipped: a row that already promoted to Committed (durable
//     surface) or Abandoned must not be re-resolved — that would
//     violate the durable-Commit contract and double-fire the
//     producer verb against claim_id idempotency.
//   - mismatched-supervisor siblings are skipped: a force-Abandon on
//     someone else's claim would violate `invariant:4` (claimant-guarded
//     release).
//
// The function is a no-op when:
//   - the parent's row is already gone (`Get` returns nil),
//   - the policy is missing, malformed, or not `strict` + `cancel_siblings`,
//   - `ListChildClaimHandles` returns no remaining siblings.
//
// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelInFlightSiblings(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	parentID shared.UUID, triggerID shared.UUID,
) error {
	parent, err := args.ClaimHandles.Get(ctx, parentID, tx)
	if err != nil {
		return fmt.Errorf("cancelInFlightSiblings: Get parent: %w", err)
	}
	if parent == nil {
		// Parent already resolved (and deleted). Nothing to cancel.
		return nil
	}
	if parent.State != spec.ClaimHandleStateActive {
		// Parent already resolved (committed via durable promotion, or
		// abandoned). Other auto-terminal paths (`CheckAndFireResolution`,
		// `resolveParentClaimChain`) both guard on `State != active`
		// and return nil; mirror the symmetry here so cancel_siblings
		// doesn't retroactively force-Abandon children whose parent
		// already resolved.
		return nil
	}
	policy, err := persistence.UnmarshalAggregationPolicy(parent.AggregationPolicy)
	if err != nil {
		// Malformed `aggregation_policy` JSONB. Surface the misconfiguration
		// via a Warn line citing the parent's id so the operator can repair
		// the row, then return nil so the surrounding terminal-decision tx
		// still commits — the parent's `aggregateParentOutcome` walker
		// applies the safe default at the post-resolution aggregator and
		// the runtime stays consistent. Without the log line the operator
		// would never learn that the policy is unparseable.
		if args.Logger != nil {
			args.Logger.Warn("cancelInFlightSiblings: malformed aggregation_policy on parent claim_handle; treating as no cancel_siblings",
				"parent_claim_handle_id", parentID.String(),
				"error", err.Error())
		}
		return nil
	}
	if policy.Kind != spec.AggregationKindStrict || !policy.CancelSiblings {
		return nil
	}
	siblings, err := args.ClaimHandles.ListChildClaimHandles(ctx, parentID, tx)
	if err != nil {
		return fmt.Errorf("cancelInFlightSiblings: ListChildClaimHandles: %w", err)
	}
	for _, sib := range siblings {
		if sib.ID == triggerID {
			// Skip the just-resolving child itself.
			continue
		}
		if sib.State != spec.ClaimHandleStateActive {
			// Non-active siblings (committed-durable promotions,
			// abandoned, or committed-subgraph rows the retention sweep
			// hasn't reaped yet) are skipped. Abandoning a durable-
			// Commit would violate the contract that durable claims
			// persist past auto-terminal until explicit release;
			// double-abandoning an already-abandoned row would race the
			// producer's claim_id idempotency.
			continue
		}
		if sib.HolderSupervisorID == nil || *sib.HolderSupervisorID != args.SupervisorID {
			// Claimant-guard: we cannot force-resolve someone else's
			// claim, and a non-active row (NULL holder) is not eligible
			// for force-Abandon either. The original supervisor's
			// terminal path will handle live siblings through its own
			// resolution.
			continue
		}
		// LockForUpdate the sibling row before the recursive force-Abandon.
		// `ResolveClaimHandleTerminal`'s documented locking precondition
		// (the contract on this function's signature) requires callers to
		// serialize concurrent terminations via `SELECT … FOR UPDATE` for
		// held-phase resolutions. Without this lock, a parallel worker on
		// the same supervisor could be terminating the sibling natively
		// (Commit/Abandon via the executor path) while our cancel walker
		// fires a force-Abandon for the same `claim_id` — the producer
		// would see two distinct verbs (Commit and Abandon) for the same
		// claim and claim_id idempotency cannot reconcile them. The lock
		// is held for the duration of the recursive `ResolveClaimHandleTerminal`
		// call below; concurrent terminators block on the row until our
		// recursive Delete commits the tx.
		current, err := args.ClaimHandles.LockForUpdate(ctx, sib.ID, tx)
		if err != nil {
			return fmt.Errorf("cancelInFlightSiblings: LockForUpdate sibling %s: %w",
				sib.ID, err)
		}
		if current == nil || current.State != spec.ClaimHandleStateActive {
			// Re-check the locked row. The recursive walker (via the
			// sibling's own descendant-cancellation walk) may have
			// already deleted later siblings in the original
			// `ListChildClaimHandles` snapshot; durable promotion on
			// the same row could also have raced ahead. Skip so the
			// producer doesn't see a duplicate Abandon for the same
			// claim_id.
			continue
		}
		producerName := ""
		if sib.ProducerName != nil {
			producerName = *sib.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
		if !ok {
			return fmt.Errorf("cancelInFlightSiblings: unknown producer %q for sibling %s",
				producerName, sib.ID)
		}
		// Build the lineage hint for the sibling's force-Abandon. The
		// resolution emits a `claim_terminal` row with
		// `outcome: force_cancelled` (the Cause field below promotes it
		// from natural Abandon) and a matching `claim_resolution.abandon`
		// event. The hint shape matches the regular resolution.
		hint := ClaimLineageHint{
			ProducerName: producerName,
			VersionID:    sib.VersionID,
			NodeID:       sib.HolderNodeID,
		}
		if sib.FrameID != nil {
			hint.FrameID = *sib.FrameID
		}
		if sib.NodeRunID != nil {
			hint.RunID = *sib.NodeRunID
		}
		if acquirer, aErr := args.Persist.Nodes().Get(ctx, sib.HolderNodeID, tx); aErr == nil && acquirer != nil {
			hint.InstanceID = acquirer.InstanceID
		}
		// Recurse: the sibling's own children (if any) cascade-cancel
		// through the same path inside this recursive call. Forwarding
		// `ParentClaimHandleID` keeps the parent counter bumping +
		// `resolveParentClaimChain` walking under the sibling's own
		// resolution. `Cause` propagates `sibling_cancel` to the lineage
		// + events projections so post-mortem queries can distinguish a
		// sibling-driven force-Abandon from a natural exhaustion.
		if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
			ClaimHandleID:       sib.ID,
			SupervisorID:        args.SupervisorID,
			Source:              HeldTerminal,
			Outcome:             AggregateAbandon,
			Producer:            producer,
			Scope:               []byte(sib.ClaimScopeData),
			Address:             []byte(sib.Address),
			Lifetime:            sib.Lifetime,
			CandidateHandle:     sib.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: sib.ParentClaimHandleID,
			Cause:               TerminalCauseSiblingCancel,
		}); err != nil {
			return fmt.Errorf("cancelInFlightSiblings: force-Abandon sibling %s: %w",
				sib.ID, err)
		}
	}
	return nil
}

// cancelDescendantClaims implements the spec §435 recursive-descent
// requirement for `strict.cancel_siblings: true`. When a row is being
// resolved as `AggregateAbandon` AND that row has in-flight descendants
// (i.e. it is itself a fan-out parent — fan-out of fan-out), each
// descendant must receive its own `Producer.Abandon` and its
// claim_handle row must be Deleted BEFORE the parent's own Delete fires.
//
// Why-before-Delete: `col:rimsky_claim_handles.parent_claim_handle_id`
// has `ON DELETE SET NULL`. Deleting the parent row first would orphan
// the descendants (parent_claim_handle_id becomes NULL) — they'd
// survive in-flight without their parent's auto-terminal ever firing
// their `Producer.Abandon`, and their running holders would never
// transition to `failed{error_class: "sibling_failed"}`. Cancelling the
// descendants first ensures the FK chain stays intact through the
// recursive walk.
//
// Filters applied to each descendant row:
//
//   - non-active rows (`State != ClaimHandleStateActive`) are skipped:
//     a row that already promoted to Committed (durable surface) or
//     Abandoned must not be re-resolved — that would violate the
//     durable-Commit contract and double-fire the producer verb against
//     claim_id idempotency (`@blessed-invariant`-class symmetry with
//     `cancelInFlightSiblings`).
//   - mismatched-supervisor rows are skipped: a force-Abandon on someone
//     else's claim would violate `invariant:4` (claimant-guarded
//     release).
//
// Each remaining descendant is force-Abandoned via a recursive
// `ResolveClaimHandleTerminal` call. That recursion runs THIS helper
// on its own descendants, so the walk handles arbitrary claim-tree
// depth (bounded by the tree itself).
//
// Re-check semantics: when the row has been deleted between the
// `ListChildClaimHandles` snapshot and the `LockForUpdate` (e.g.
// because the recursive walker has already reached it via another
// path), `LockForUpdate` returns nil and the row is skipped.
//
// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelDescendantClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	rowID shared.UUID,
) error {
	descendants, err := args.ClaimHandles.ListChildClaimHandles(ctx, rowID, tx)
	if err != nil {
		return fmt.Errorf("cancelDescendantClaims: ListChildClaimHandles: %w", err)
	}
	for _, d := range descendants {
		if d.State != spec.ClaimHandleStateActive {
			continue
		}
		if d.HolderSupervisorID == nil || *d.HolderSupervisorID != args.SupervisorID {
			continue
		}
		// LockForUpdate the descendant row before the recursive force-
		// Abandon. Same locking precondition as `cancelInFlightSiblings`:
		// the contract on `ResolveClaimHandleTerminal` requires the
		// caller to serialize concurrent terminations via
		// `SELECT … FOR UPDATE` for held-phase resolutions, so we hold
		// the row lock for the duration of the recursive call below.
		current, err := args.ClaimHandles.LockForUpdate(ctx, d.ID, tx)
		if err != nil {
			return fmt.Errorf("cancelDescendantClaims: LockForUpdate descendant %s: %w",
				d.ID, err)
		}
		if current == nil || current.State != spec.ClaimHandleStateActive {
			continue
		}
		producerName := ""
		if d.ProducerName != nil {
			producerName = *d.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
		if !ok {
			return fmt.Errorf("cancelDescendantClaims: unknown producer %q for descendant %s",
				producerName, d.ID)
		}
		hint := ClaimLineageHint{
			ProducerName: producerName,
			VersionID:    d.VersionID,
			NodeID:       d.HolderNodeID,
		}
		if d.FrameID != nil {
			hint.FrameID = *d.FrameID
		}
		if d.NodeRunID != nil {
			hint.RunID = *d.NodeRunID
		}
		if acquirer, aErr := args.Persist.Nodes().Get(ctx, d.HolderNodeID, tx); aErr == nil && acquirer != nil {
			hint.InstanceID = acquirer.InstanceID
		}
		// Recurse. The descendant's own descendants are walked inside
		// `ResolveClaimHandleTerminal`'s pre-Delete cancellation step
		// (depth-first). `ParentClaimHandleID` is intentionally `nil`
		// here: the descendant's parent is the row being resolved by
		// the OUTER `ResolveClaimHandleTerminal` frame above us, which
		// will Delete that row after this descendant walk returns.
		// Forwarding `d.ParentClaimHandleID` would re-enter the parent's
		// counter-bump + `resolveParentClaimChain` walk on a row that
		// is mid-resolution, risking a re-entrant Delete / duplicate
		// `Producer.Abandon` on the parent. Skipping is safe because
		// the parent's own resolution drives its grandparent counter
		// after this descendant cancellation completes.
		if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
			ClaimHandleID:       d.ID,
			SupervisorID:        args.SupervisorID,
			Source:              HeldTerminal,
			Outcome:             AggregateAbandon,
			Producer:            producer,
			Scope:               []byte(d.ClaimScopeData),
			Address:             []byte(d.Address),
			Lifetime:            d.Lifetime,
			CandidateHandle:     d.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: nil,
			Cause:               TerminalCauseDescendantCancel,
		}); err != nil {
			return fmt.Errorf("cancelDescendantClaims: force-Abandon descendant %s: %w",
				d.ID, err)
		}
	}
	return nil
}
