// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Auto-terminal mechanism (`@blessed-invariant 13`, as amended by
// docs/history/2026-04-30-stores-protocol-cleanup-design.md).
//
// At a held claim's holding-subgraph completion, the supervisor fires
// exactly one store verb based on aggregate outcome — Commit if
// every claim-holder reached `'completed'`, Abandon if any reached
// `'failed'` — then promotes the lock-holder row: its state flips to
// `'committed'`/`'failed'` and the row is preserved past terminal (a
// later retention sweep reaps it), rather than being deleted. The store
// decides what Commit / Abandon mean for its own state per its own
// configuration; rimsky carries only the success/failure binary.
// Race-safe via SELECT … FOR UPDATE on the lock-holder row plus a
// state='active' filter on the claim-holders rows: concurrent
// terminations on the same subgraph see the row already locked /
// already promoted and no-op.
//
// Post-stage-5 of the run-row lifecycle cutover, claim-holders rows
// are keyed by `holder_run_id` (a `rimsky_node_runs.id`). The acquirer's
// own holder row is inserted at acquire-time
// (`runner_acquire_holders.go::insertHeldClaimHoldersAtAcquire`); co-holder /
// inheritor rows are inserted at the inheritor's own acquire-time
// (`runner_acquire_holders.go::insertCoHolderClaimHoldersAtAcquire`).
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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// CheckAndFireResolution implements the spec §4.10 invariant 13 algorithm: lock
// the rimsky_claim_handles row, check whether all rimsky_claim_holders
// rows for the lock-holder are non-active, compute aggregate outcome
// (any 'failed' → Abandon; else → Commit), and delegate to the unified
// terminal-decision engine in terminal_decision.go.
//
// Runs inside the caller's tx so the producer verb + the claim_handle
// Promote (state flip to committed/abandoned; the row is preserved past
// terminal and reaped later by the retention sweep) + the cascade-cleared
// claim-holder rows commit atomically with whatever else the caller is
// mutating.
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
// detection logic; the actual verb-fire + Promote sequence
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
		// @constraint: row gone from the table means the retention sweep
		// reaped it after a prior termination promoted it to non-active
		// (race-safe per §4.10 invariant 13.2); the non-active-but-present
		// case falls through to the state guard below.
		return nil
	}
	if row.HolderSupervisorID == nil || *row.HolderSupervisorID != args.SupervisorID {
		// @constraint: claimant-guard first matches cycle-5 ordering — a
		// NULL holder (non-active row per the migration-009 CHECK pair) or
		// a UUID re-use both no-op here; the active-state guard immediately
		// below would also filter non-active rows but ordering matters.
		return nil
	}
	// @constraint: held-durable already-promoted skip — a previous
	// holding-subgraph completion promoted the row to state='committed' and
	// skipped the Delete (held-durable Promote contract per
	// @blessed-invariant 22; see ResolveClaimHandleTerminal); a late sibling
	// terminal re-entering CheckAndFireResolution would otherwise re-fire
	// Commit and emit a duplicate claim_terminal lineage row. The recursive
	// SettleChildren already treats committed-durable children as
	// resolved-and-deleted; this matches that posture.
	// @concept: claim-handle
	if row.State != spec.ClaimHandleStateActive {
		return nil
	}

	holders, err := args.Persist.ClaimHolders().ListByClaimHandleID(ctx, claimHandleID, tx)
	if err != nil {
		return fmt.Errorf("CheckAndFireResolution: ListByClaimHandleID: %w", err)
	}
	if len(holders) == 0 {
		// @deliberate: tolerate the non-held-claim case — callers should
		// not invoke this for non-held claims but a defensive no-op is
		// cheaper than a panic.
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
	// @constraint: premature-firing guard — inheritor / co-holder runs
	// insert holder rows at THEIR own acquire-tx, not the acquirer's, so
	// when the acquirer's row is the first to flip non-active, the holder
	// set is incomplete relative to the deploy-time subgraph. Consult the
	// template's holding subgraph and bail if any expected inheritor is
	// missing.
	// @deliberate: skip the guard on aggregate-failed — a failed holder
	// dooms the subgraph and downstream inheritors won't dispatch (the
	// cascade walker won't stale-mark them on a failed sender), so Abandon
	// must fire immediately rather than waiting for rows that will never
	// appear.
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
	// @deliberate: bare Get on the terminal-resolution path — the claim
	// was already bound at acquire time and no instance context is in
	// scope here, so dispatch-time alias resolution does not apply.
	producer, ok := args.StoreRegistry.Get(producerName)
	if !ok {
		return fmt.Errorf("CheckAndFireResolution: unknown producer %q", producerName)
	}
	outcome := AggregateCommit
	if anyFailed {
		outcome = AggregateAbandon
	}
	// @constraint: fan-out parent aggregation — when the row carries
	// expected_children_count > 0, combine the holders' aggregate with the
	// children's aggregate per the snapshotted policy. Holders contribute
	// the "this claim's own work" outcome; children contribute the
	// "fan-out work" outcome; the aggregation policy chooses Commit vs
	// Abandon over the children; a holder failure always implies Abandon
	// (parent's own subgraph failed). Pre-cycle-4 callers that never set
	// children get the historical anyFailed-only verdict.
	// @constraint: cycle-6 children-quorum guard — the
	// children-aggregation branch assumes every fan-out child has resolved
	// (and bumped its outcome counter via ResolveClaimHandleTerminal)
	// before the parent's CheckAndFireResolution runs. The run-tree
	// Aggregate orders parent terminal strictly after all children, but
	// this is defense-in-depth: a future caller firing on a fan-out parent
	// before all children have terminated would compute the wrong verdict
	// (best_effort could read committed_children_count == 0 mid-flight →
	// Abandon despite pending Commits). Defer when counters don't yet
	// cover expected children; the next child's terminal re-invokes
	// SettleChildren, which converges on the same Commit/Abandon decision.
	if row.ExpectedChildrenCount > 0 && !anyFailed {
		resolved := row.CommittedChildrenCount + row.AbandonedChildrenCount
		if resolved < row.ExpectedChildrenCount {
			return nil
		}
		outcome = aggregateParentOutcome(row, outcome)
	}
	// @deliberate: re-derive the lineage hint from the held claim-handle
	// row because the held-claim path carries no acquisition struct — the
	// per-claim instance / frame / node must come from the row itself.
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
	// @constraint: recursive claim-tree resolution rides through
	// ResolveClaimHandleTerminal — it promotes the row to terminal and
	// settles the parent through SettleChildren (counter bump + sibling
	// cancel + aggregation walk). Forwarding ParentClaimHandleID keeps
	// the held path firing the parent walk after the resolution commits.
	// @constraint: outcome propagation — a sub-claim that aggregated
	// Abandon signals "this partition's holding subgraph failed"; the
	// fan-out parent must Abandon to match because partial success leaks
	// bytes the caller's aggregator wasn't expecting. SettleChildren
	// honors seedOutcome to carry the aggregate-failed verdict up.
	// @deliberate: test-only seam fires BEFORE the verb + Promote — an
	// injection test interleaves a second contender here to prove the
	// SELECT … FOR UPDATE serialization + the state='active' guard make
	// the loser a no-op (the producer verb fires exactly once per
	// @blessed-invariant 13). Nil in production (no behavior change); see
	// RunArgs.CheckAndFireHook.
	if args.CheckAndFireHook != nil {
		args.CheckAndFireHook(ctx)
	}
	td := TerminalDecision{
		ClaimHandleID:       claimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              HeldTerminal,
		Outcome:             outcome,
		Producer:            producer,
		Scope:               []byte(row.ClaimScopeData),
		Address:             []byte(row.Address),
		Lifetime:            row.Lifetime,
		CandidateHandle:     row.ProducerCandidateHandle,
		ProducerName:        producerName,
		LineageHint:         hint,
		ParentClaimHandleID: row.ParentClaimHandleID,
	}
	if err := ResolveClaimHandleTerminal(ctx, args, tx, td); err != nil {
		// @constraint: a producer-verb fault carrying a rimsky error_class
		// (e.g. atomic-staging pg/swap_failed from the postgres store
		// Commit) is NOT tx-fatal — bubbling would roll back the holder's
		// terminal and wedge the run with no settled disposition (claim
		// never resolves, run retries forever). Instead route as a
		// claim-terminal error signal (terminal/error/<class> for the
		// holder node) co-committed in this same tx, and resolve the claim
		// handle so the held subgraph settles cleanly. This gives the
		// producer's declared pg/swap_failed class a real signal at the
		// subscriber surface.
		// @concept: error-policy
		if cls := producerErrorClassOf(err); cls != "" {
			if rerr := routeHeldClaimVerbError(ctx, args, tx, row, td, cls); rerr != nil {
				return fmt.Errorf("CheckAndFireResolution: route verb error: %w", rerr)
			}
			return nil
		}
		return fmt.Errorf("CheckAndFireResolution: %w", err)
	}
	return nil
}

// routeHeldClaimVerbError handles a held-claim auto-terminal whose
// producer Commit/Abandon RPC faulted with a rimsky error_class. It
// emits the canonical `terminal/error/<class>` signal for the holder
// node (co-committed in the caller's terminal tx so a subscriber on
// `terminal/error/<class>` — or the wildcard `terminal/error/*` — fires)
// and promotes the claim handle to a terminal state so the held subgraph
// settles rather than wedging on the faulted verb.
//
// The producer verb is NOT re-fired here: the store already attempted the
// cutover and left its own state consistent (a `pg/swap_failed` collision
// leaves the staging intact for the operator to inspect). Promoting the
// rimsky_claim_handles row to `abandoned` records that the held resolution
// did not commit, without driving a second producer RPC that would
// discard the store-side state the operator may want to examine.
//
//	@concept: error-policy
//	@concept: signal
func routeHeldClaimVerbError(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row *persistence.ClaimHandleRow, td TerminalDecision, errorClass string,
) error {
	// @deliberate: senderRunID / senderFrameID come from the claim-handle
	// row when present; a zero pair degrades to the audit-only edge — the
	// disposition row still lands on the event log, which is the
	// operator-visible surface a subscriber / error_types: chain routes to.
	var senderRunID, senderFrameID shared.UUID
	if row.NodeRunID != nil {
		senderRunID = *row.NodeRunID
	}
	if row.FrameID != nil {
		senderFrameID = *row.FrameID
	}
	holderNodeType := ""
	if nd, err := args.Persist.Nodes().Get(ctx, row.HolderNodeID, tx); err == nil && nd != nil {
		holderNodeType = nd.NodeType
	}
	sig := errorPolicySignal(errorClass, map[string]any{
		"source":          "claim_terminal_verb",
		"producer":        td.ProducerName,
		"claim_handle_id": td.ClaimHandleID.String(),
	}, nil, "give_up", 0, 0)
	if err := emitSignalInTxOnce(ctx, args, tx,
		row.HolderNodeID, holderNodeType, senderRunID, td.LineageHint.InstanceID,
		senderFrameID, sig); err != nil {
		return fmt.Errorf("emit claim-terminal error signal: %w", err)
	}
	// @constraint: promote to abandoned WITHOUT re-firing the producer
	// verb — the faulted cutover already ran store-side, and a
	// pg/swap_failed collision leaves the staging intact for the operator
	// to inspect; re-firing would discard that state. Reuse
	// promoteHandleState by flagging the decision as Abandon.
	abandonTD := td
	abandonTD.Outcome = AggregateAbandon
	if err := promoteHandleState(ctx, args, tx, abandonTD); err != nil {
		return fmt.Errorf("promote handle after verb error: %w", err)
	}
	return nil
}

// expectedInheritorsMissing reports whether the holding subgraph for
// this claim has expected co-holder / inheritor members that have not
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
//     first match — same fallback as the co-holder release path's
//     `pickAliasForClaimHandle`.)
//   - Subgraph members come from `node.HoldingSubgraphsForTemplate`
//     filtered to the resolved (acquirer-type, alias) pair. That set is
//     holds-aware: every node declaring `holds: {<alias>: {from:
//     <acquirer>}}` is a member, so the guard waits for every declared
//     co-holder.
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
		// @deliberate: best-effort fall-through when alias resolution
		// fails — don't defer auto-terminal on a missing alias, since the
		// expected-member set can't be computed without one.
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
		// @deliberate: a single-member subgraph is non-held — nothing to
		// wait for, don't defer.
		return false, nil
	}
	// @constraint: map each holder row's run id → node id → node type via
	// Queue.GetDispatchNode, which returns node_id regardless of phase
	// (works for both in-flight and terminal-completed run rows). Building
	// a (node-type, present?) view lets the loop below check expected
	// members without re-querying per-member.
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
