// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Auto-terminal mechanism (`@blessed-invariant 13`, as amended by
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
//
// Post-stage-5 of the run-row lifecycle cutover, claim-holders rows
// are keyed by `holder_run_id` (a `rimsky_node_runs.id`). The acquirer's
// own holder row is inserted at acquire-time
// (`runner_acquire.go::insertHeldClaimHoldersAtAcquire`); co-holder /
// inheritor rows are inserted at the inheritor's own acquire-time
// (`runner_acquire.go::insertCoHolderClaimHoldersAtAcquire`).
//
// **Pre-dispatch deferral.** Inheritor / co-holder rows are inserted at
// the inheritor's own acquire, not at the acquirer's acquire. When the
// acquirer terminates first, the only `rimsky_claim_holders` row
// present is the acquirer's own — auto-terminal would prematurely fire
// against an incomplete holding subgraph. This function avoids that by
// consulting the deploy-time subgraph (`HoldingSubgraphsForTemplate`)
// to determine the expected member set, then refusing to fire while
// any expected member's run hasn't yet been observed in the holders
// table (i.e., the inheritor has not yet dispatched its acquire-tx
// INSERT).

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
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
	// Held-durable promotion already fired. A previous holding-subgraph
	// completion flipped held_durable=TRUE and skipped the Delete (see
	// `ResolveClaimHandleTerminal`); a late sibling terminal re-entering
	// CheckAndFireResolution would otherwise re-fire Commit and emit a
	// duplicate `claim_terminal` lineage row. The recursive
	// `resolveParentClaimChain` already treats held-durable children as
	// resolved-and-deleted; this matches that posture. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Held-durable claim lifecycle.
	if row.HeldDurable {
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
	// Premature-firing guard. Inheritor / co-holder runs insert their
	// holder rows at THEIR own acquire-tx, not at the acquirer's acquire.
	// When the acquirer's row is the first to flip non-active, the
	// holder set is incomplete relative to the deploy-time subgraph.
	// Consult the template's holding subgraph and bail if any expected
	// inheritor is missing from the current row set.
	//
	// Skip the guard on aggregate-failed: a failed holder means the
	// subgraph is doomed and downstream inheritors won't dispatch
	// (the cascade walker won't stale-mark them on a failed sender),
	// so Abandon must fire immediately rather than waiting for rows
	// that will never appear.
	if !anyFailed {
		expectedMissing, err := expectedInheritorsMissing(ctx, args, tx, row, holders)
		if err != nil {
			return fmt.Errorf("CheckAndFireResolution: expected-inheritor check: %w", err)
		}
		if expectedMissing {
			return nil
		}
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
	// When the row is itself a fan-out parent (expected_children_count > 0),
	// combine the holders' aggregate with the children's aggregate per
	// the snapshotted policy. The holders contribute the "this claim's
	// own work" outcome; the children contribute the "fan-out work"
	// outcome. The aggregation policy chooses Commit vs Abandon over
	// the children; a holder failure always implies Abandon (the parent's
	// own subgraph failed). Pre-cycle-4 callers that never set children
	// (`expected_children_count = 0`) get the historical anyFailed-only
	// verdict (cycle 4 issue C tail).
	//
	// Cycle-6 children-quorum guard. The children-aggregation branch
	// assumes every fan-out child has already resolved (and bumped its
	// outcome counter via `ResolveClaimHandleTerminal`) before the
	// parent's `CheckAndFireResolution` runs. This holds in normal
	// operation because the run-tree `Aggregate` orders parent terminal
	// strictly after all children — but the assumption isn't enforced
	// inside this function. A future caller that fires
	// `CheckAndFireResolution` on a fan-out parent before all children
	// have terminated would see incomplete counters and compute the
	// wrong verdict (e.g. `best_effort` could read
	// `committed_children_count == 0` mid-flight → Abandon despite
	// pending Commits). Defer when the counters don't yet cover the
	// expected children; the next child's terminal will re-invoke
	// `resolveParentClaimChain`, which performs the same children-
	// completeness check via `ListChildClaimHandles` row presence and
	// re-evaluates the parent's verdict through the same counters via
	// `aggregateParentOutcome`. The two paths converge on the same
	// Commit/Abandon decision. Defense-in-depth — redundant under the
	// current call graph but makes the ordering assumption explicit.
	if row.ExpectedChildrenCount > 0 && !anyFailed {
		resolved := row.CommittedChildrenCount + row.AbandonedChildrenCount
		if resolved < row.ExpectedChildrenCount {
			return nil
		}
		outcome = aggregateParentOutcome(row, outcome)
	}
	// Build the lineage hint from the held claim-handle row. The
	// held-claim path doesn't carry an acquisition struct, so we
	// re-derive the per-claim instance / frame / node from the row.
	hint := ClaimLineageHint{
		ProducerName: producerName,
		VersionID:    row.VersionID,
		NodeID:       row.HolderNodeID,
	}
	if row.FrameID != nil {
		hint.FrameID = *row.FrameID
	}
	if row.NodeRunID != nil {
		hint.RunID = *row.NodeRunID
	}
	if acquirer, aErr := args.Persist.Nodes().Get(ctx, row.HolderNodeID, tx); aErr == nil && acquirer != nil {
		hint.InstanceID = acquirer.InstanceID
	}
	// Recursive claim-tree resolution is now driven by
	// `ResolveClaimHandleTerminal` itself when it walks the non-durable
	// Delete branch (sub-claim rows always release via that branch
	// because the durable promotion happens only at the parent
	// resolution). Forwarding `ParentClaimHandleID` here keeps the held
	// path firing the parent walk after the Delete commits. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Recursive claim-tree resolution + §Fan-out template DSL.
	//
	// Outcome propagation: a sub-claim that aggregated Abandon
	// signals "this partition's holding subgraph failed." The fan-out
	// parent must Abandon to match — partial success leaks bytes the
	// caller's aggregator wasn't expecting. resolveParentClaimChain
	// honors `seedOutcome` to carry the aggregate-failed verdict up.
	if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID:       claimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              HeldTerminal,
		Outcome:             outcome,
		Producer:            producer,
		Scope:               []byte(row.ScopeData),
		Address:             []byte(row.Address),
		Lifetime:            row.Lifetime,
		CandidateHandle:     row.ProducerCandidateHandle,
		ProducerName:        producerName,
		LineageHint:         hint,
		ParentClaimHandleID: row.ParentClaimHandleID,
	}); err != nil {
		return fmt.Errorf("CheckAndFireResolution: %w", err)
	}
	return nil
}

// expectedInheritorsMissing reports whether the holding subgraph for
// this claim has expected inheritor / co-holder members that have not
// yet inserted their `rimsky_claim_holders` row. Used by
// `CheckAndFireResolution` to defer auto-terminal until every expected
// member has acquired (and then terminated).
//
// Member resolution:
//   - Acquirer node-type comes from the `rimsky_nodes` row referenced
//     by `rimsky_claim_handles.holder_node_id`.
//   - Alias is resolved by matching the claim's `producer_name` against
//     the acquirer's `claims:` declarations. (When the acquirer declares
//     multiple aliases against the same producer, this defaults to the
//     first match — same fallback as the inheritor's release path's
//     `pickAliasForClaimHandle`.)
//   - Subgraph members come from `node.HoldingSubgraphsForTemplate`
//     filtered to the resolved (acquirer-type, alias) pair.
//
// For each expected member node-type, the function checks whether ANY
// row in `holders` corresponds to a run for a node of that type within
// the same instance. If every expected member is represented, returns
// false (auto-terminal may fire); otherwise true (defer).
func expectedInheritorsMissing(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row *persistence.ClaimHandleRow, holders []persistence.ClaimHolderRow,
) (bool, error) {
	if row == nil {
		return false, nil
	}
	acquirer, err := args.Persist.Nodes().Get(ctx, row.HolderNodeID, tx)
	if err != nil {
		return false, fmt.Errorf("nodes.Get acquirer: %w", err)
	}
	if acquirer == nil {
		return false, nil
	}
	inst, err := args.Persist.Instances().Get(ctx, acquirer.InstanceID, tx)
	if err != nil || inst == nil {
		return false, nil
	}
	tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
	if err != nil || tmpl == nil {
		return false, nil
	}
	acqDef := lookupNodeDef(&tmpl.Spec, acquirer.NodeType)
	if acqDef == nil {
		return false, nil
	}
	producerName := ""
	if row.ProducerName != nil {
		producerName = *row.ProducerName
	}
	var alias string
	for _, sref := range acqDef.Stores {
		if sref.Name == producerName {
			alias = sref.AliasOf()
			break
		}
	}
	if alias == "" {
		// No alias resolution path. Best-effort: don't defer.
		return false, nil
	}
	subgraphs := node.HoldingSubgraphsForTemplate(&tmpl.Spec)
	var members []string
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirer.NodeType && sg.Alias == alias {
			members = sg.Members
			break
		}
	}
	if len(members) <= 1 {
		// Non-held subgraph; nothing to wait for.
		return false, nil
	}
	// Build a (node-type, present?) view from the current holder rows.
	// Map each holder row's run id → node id → node type via
	// Queue.GetDispatchNode (returns node_id regardless of phase, so it
	// works for both in-flight and terminal-completed run rows).
	holderTypes := make(map[string]struct{}, len(holders))
	for _, h := range holders {
		nodeID, _, err := args.Queue.GetDispatchNode(ctx, h.HolderRunID)
		if err != nil || nodeID == (shared.UUID{}) {
			continue
		}
		nodeRow, err := args.Persist.Nodes().Get(ctx, nodeID, tx)
		if err != nil || nodeRow == nil {
			continue
		}
		holderTypes[nodeRow.NodeType] = struct{}{}
	}
	for _, m := range members {
		if _, present := holderTypes[m]; !present {
			return true, nil
		}
	}
	return false, nil
}

// resolveParentClaimChain walks upward from a sub-claim's parent. At
// each level, the parent's resolution fires only when:
//
//  1. The parent's own holding subgraph (if any) has settled — every
//     `rimsky_claim_holders` row for the parent_claim_handle_id is
//     non-active. When the parent is itself held with co-holders still
//     working, the parent's normal `CheckAndFireResolution` path will
//     re-drive this walk later (cycle 4 issue D).
//  2. Every sub-claim row beneath it has resolved — either via the
//     non-durable Delete branch (rows disappear) or via the held-durable
//     promotion (rows linger with `held_durable=TRUE` but do not block
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
// as each child's terminal Delete (`ResolveClaimHandleTerminal`), so
// the read here under SELECT … FOR UPDATE sees a consistent view.
//
// @concept: claim-tree
// @concept: fan-out
// @concept: auto-terminal
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
	if parent.HolderSupervisorID != args.SupervisorID {
		return nil
	}
	if parent.HeldDurable {
		// Already held-durable — auto-terminal already fired on this row.
		// Mirrors the CheckAndFireResolution guard.
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
	// If any sub-claim is still present AND not held-durable, the
	// parent isn't ready to resolve yet.
	//
	// @blessed-invariant: held-durable claim handles persist across
	// instance dispatches. A claim handle with `held_durable = true` is
	// not deleted by holding-subgraph auto-terminal; only by explicit
	// operator action (`DELETE /instances/{id}/assets/{alias}`) or
	// instance termination (`ReleaseHeldDurableClaims`). The orphan-
	// claim reaper skips `held_durable = true` rows. Recursive parent-
	// claim resolution treats a held-durable child the same as a
	// resolved-and-deleted child: it does not block the parent from
	// firing its own auto-terminal. The child stays available for
	// future co-holdership via `holds:` until explicit release.
	for _, c := range children {
		if !c.HeldDurable {
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
	// ResolveClaimHandleTerminal: the engine's non-durable Delete path
	// invokes `resolveParentClaimChain` on a non-nil parent so the chain
	// walks the entire claim tree without an explicit recursive call here.
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

// (lockClaimHandleRow + scanClaimHandleForResolution were retired when
// the persistence layer landed `ClaimHandleTable.LockForUpdate`. The
// auto-terminal flow above calls that method directly.)
