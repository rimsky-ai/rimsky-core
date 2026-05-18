// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// auto_terminal_chain.go — recursive parent-claim resolution walk
// (the fan-out / sub-claim chain). Split out of `auto_terminal.go`
// per the 2026-05-17 cold-read paydown (Item 4 / Tier 2).
//
// @concept: claim-tree
// @concept: auto-terminal
// @concept: fan-out

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// resolveParentClaimChain walks upward from a sub-claim's parent. At
// each level, the parent's resolution fires only when:
//
//  1. The parent's own holding subgraph (if any) has settled — every
//     `rimsky_claim_holders` row for the parent_claim_handle_id is
//     non-active. When the parent is itself held with co-holders still
//     working, the parent's normal `CheckAndFireResolution` path will
//     re-drive this walk later (cycle 4 issue D).
//  2. Every sub-claim row beneath it has resolved — either via the
//     standard Promote branch (state flips to `committed` / `abandoned`)
//     or via the held-durable preservation (rows linger with
//     `state = 'committed' AND lifetime = 'durable'` but do not block
//     the parent).
//
// Once those preconditions hold, the parent's aggregate Commit/Abandon
// decision is computed across ALL children's outcomes per the
// snapshotted `aggregation_policy` — not just the seedOutcome of the
// just-resolved child (cycle 4 issue C). The aggregation rules mirror
// `runtime/run_tree.go::Aggregate` for run-state aggregation, mapped
// onto the Commit/Abandon binary the claim layer carries:
//
//	strict (default)        — any abandoned → Abandon; else Commit
//	threshold(max_failures) — abandoned > max_failures → Abandon; else Commit
//	best_effort             — committed > 0 → Commit; else Abandon
//	first                   — committed > 0 → Commit; else Abandon
//
// Counters (`expected_children_count`, `committed_children_count`,
// `abandoned_children_count`) are bumped atomically inside the same tx
// as each child's terminal Promote (`ResolveClaimHandleTerminal`), so
// the read here under SELECT … FOR UPDATE sees a consistent view.
func resolveParentClaimChain(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	parentClaimHandleID shared.UUID, seedOutcome AggregateOutcome,
) error {
	parent, err := args.ClaimHandles.LockForUpdate(ctx, parentClaimHandleID, tx)
	if err != nil {
		return err
	}
	if parent == nil {
		// Already resolved.
		return nil
	}
	if parent.HolderSupervisorID == nil || *parent.HolderSupervisorID != args.SupervisorID {
		// Non-active parent (NULL holder) or claimant mismatch — both
		// no-op for the same reason as `CheckAndFireResolution`'s guard.
		return nil
	}
	if parent.State != spec.ClaimHandleStateActive {
		// Already resolved — auto-terminal already fired on this row
		// (committed via durable promotion, or abandoned). Mirrors the
		// CheckAndFireResolution guard.
		return nil
	}
	// Issue D guard: if the parent is itself a held claim with active
	// co-holders, defer parent resolution. The parent's normal
	// `CheckAndFireResolution` path will re-enter this walk after the
	// last holder transitions to non-active.
	holders, err := args.Persist.ClaimHolders().ListByClaimHandleID(ctx, parentClaimHandleID, tx)
	if err != nil {
		return fmt.Errorf("resolveParentClaimChain: ListByClaimHandleID: %w", err)
	}
	for _, h := range holders {
		if h.State == persistence.ClaimHolderStateActive {
			// Parent's holding subgraph not yet complete; the
			// CheckAndFireResolution path will re-drive when the last
			// holder transitions. Skip parent resolution this round.
			return nil
		}
	}
	children, err := args.ClaimHandles.ListChildClaimHandles(ctx, parentClaimHandleID, tx)
	if err != nil {
		return fmt.Errorf("resolveParentClaimChain: ListChildClaimHandles: %w", err)
	}
	// If any sub-claim is still active, the parent isn't ready to
	// resolve yet. Non-active children (committed / abandoned) are
	// treated as resolved and don't block the parent.
	//
	// @blessed-invariant 22: held-durable claim handles persist across
	// instance dispatches. A claim handle with `state = 'committed'
	// AND lifetime = 'durable'` is not deleted by holding-subgraph
	// auto-terminal; only by explicit operator action
	// (`DELETE /instances/{id}/assets/{alias}`) or instance termination
	// (`ReleaseHeldDurableClaims`). The orphan-claim reaper skips
	// non-active rows. Recursive parent-claim resolution treats a
	// committed-durable child the same as a resolved-and-deleted
	// child: it does not block the parent from firing its own
	// auto-terminal. The child stays available for future
	// co-holdership via `holds:` until explicit release.
	for _, c := range children {
		if c.State == spec.ClaimHandleStateActive {
			return nil
		}
	}
	// Issue C: aggregate across ALL children using the snapshotted
	// policy + per-outcome counters. `expected_children_count` reflects
	// the total fan-out width set at AcquireSubClaims time; committed +
	// abandoned reflect resolved children (terminal Delete bumped each
	// counter in ResolveClaimHandleTerminal). If for some reason the
	// counters are uninitialized (e.g. a non-fan-out leaf that happened
	// to set ParentClaimHandleID), fall back to seedOutcome — the
	// pre-cycle-4 posture — so we don't strand the parent.
	outcome := aggregateParentOutcome(parent, seedOutcome)
	producerName := ""
	if parent.ProducerName != nil {
		producerName = *parent.ProducerName
	}
	producer, ok := args.StoreRegistry.Get(producerName)
	if !ok {
		return fmt.Errorf("resolveParentClaimChain: unknown producer %q", producerName)
	}
	// Lineage hint for the parent claim resolution. Same shape as the
	// held-claim path in CheckAndFireResolution above.
	parentHint := ClaimLineageHint{
		ProducerName: producerName,
		VersionID:    parent.VersionID,
		NodeID:       parent.HolderNodeID,
	}
	if parent.FrameID != nil {
		parentHint.FrameID = *parent.FrameID
	}
	if parent.NodeRunID != nil {
		parentHint.RunID = *parent.NodeRunID
	}
	if acquirer, aErr := args.Persist.Nodes().Get(ctx, parent.HolderNodeID, tx); aErr == nil && acquirer != nil {
		parentHint.InstanceID = acquirer.InstanceID
	}
	// Recurse upward by forwarding ParentClaimHandleID through
	// ResolveClaimHandleTerminal: the engine's Promote path invokes
	// `resolveParentClaimChain` on a non-nil parent so the chain walks
	// the entire claim tree without an explicit recursive call here.
	if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID:       parentClaimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              HeldTerminal,
		Outcome:             outcome,
		Producer:            producer,
		Scope:               []byte(parent.ScopeData),
		Address:             []byte(parent.Address),
		Lifetime:            parent.Lifetime,
		CandidateHandle:     parent.ProducerCandidateHandle,
		ProducerName:        producerName,
		LineageHint:         parentHint,
		ParentClaimHandleID: parent.ParentClaimHandleID,
	}); err != nil {
		return err
	}
	return nil
}

// aggregateParentOutcome computes the parent's Commit/Abandon verdict
// from the snapshotted aggregation policy + the per-outcome counters on
// the parent claim_handle row. Falls back to `seedOutcome` when the
// counters indicate "no fan-out children expected" (legacy callers that
// set ParentClaimHandleID on a non-fan-out leaf) so we never strand
// the parent.
//
// Rule table (mapped from spec §State aggregation rules onto the
// Commit/Abandon binary):
//
//	strict (default)        — any abandoned → Abandon; else Commit
//	threshold(max_failures) — abandoned > max_failures → Abandon; else Commit
//	best_effort             — committed > 0 → Commit; else Abandon
//	first                   — committed > 0 → Commit; else Abandon
//	(unknown kind)          — defaults to strict for safety
func aggregateParentOutcome(parent *persistence.ClaimHandleRow, seedOutcome AggregateOutcome) AggregateOutcome {
	if parent == nil {
		return seedOutcome
	}
	if parent.ExpectedChildrenCount == 0 {
		// Non-fan-out parent on this row — no aggregation needed; carry
		// the seed.
		return seedOutcome
	}
	policy, err := persistence.UnmarshalAggregationPolicy(parent.AggregationPolicy)
	if err != nil || policy.Kind == "" {
		// Missing / malformed policy → default to strict.
		policy = spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	}
	committed := parent.CommittedChildrenCount
	abandoned := parent.AbandonedChildrenCount
	switch policy.Kind {
	case spec.AggregationKindStrict:
		if abandoned > 0 {
			return AggregateAbandon
		}
		return AggregateCommit
	case spec.AggregationKindThreshold:
		if abandoned > policy.MaxFailures {
			return AggregateAbandon
		}
		return AggregateCommit
	case spec.AggregationKindBestEffort, spec.AggregationKindFirst:
		if committed > 0 {
			return AggregateCommit
		}
		return AggregateAbandon
	default:
		// Unknown kind: safest is strict semantics.
		if abandoned > 0 {
			return AggregateAbandon
		}
		return AggregateCommit
	}
}
