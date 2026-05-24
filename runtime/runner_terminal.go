// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Terminal-event handling under the stores redesign — release path
// (§7.6 / §4.10 invariant 13 auto-terminal).
//
// Branches per terminal StreamClose outcome (post-E.2 wire shape):
//
//   - Success{changed: true}   → validate attributes,
//                                 fire per-claim release path (held vs.
//                                 non-held branches per §7.6),
//                                 persist final attributes, state→fresh,
//                                 emit `attributes_committed`,
//                                 cascade message-pass on dependents.
//   - Success{changed: false}  → as above; emit `no_op_commit`; no
//                                 cascade.
//   - Error{error_class}        → policy chain: discard_then_retry |
//                                 give_up | invalidate(targets). All
//                                 release through the failure branch
//                                 (Abandon for non-held; mark
//                                 'failed' + auto-terminal for held).
//                                 The reserved class "executor_blocked"
//                                 replaces the pre-E.2 Blocked variant.
//   - Infra error              → infra_reenqueue: state→stale, failure-
//                                 branch release, re-enqueue without
//                                 retry bump.

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	foundationshared "github.com/fallguy/rimsky/foundation/shared"
	attributes "github.com/fallguy/rimsky/graph/attribute"
	"github.com/fallguy/rimsky/graph/frame"
	"github.com/fallguy/rimsky/graph/node"
)

// postCommitFn is the deferred-side-effect closure returned by every
// applyTerminal* handler. Callers run it AFTER the outer state-mutation
// tx commits; it covers observability emits (lineage, audit events
// appended in best-effort txns), run-tree state propagation, and
// post-commit cascade fan-out. Returning nil is permitted and means
// "no post-commit work."
//
// The split exists so the callback-determinism phase-check and the
// terminal's primary state-mutation share one tx (per
// @blessed-invariant: Callback determinism) while the open-its-own-tx
// observability work continues to run after commit (which it must,
// since SQLite uses a single-conn pool and would self-deadlock on a
// nested Transaction call).
type postCommitFn func(ctx context.Context)

// applyTerminal is the omnibus runner's terminal-event entry point.
//
// Threading discipline: the caller passes the outer state-mutation tx
// (`tx`); every handler runs its primary state-mutation work inside
// that tx (lock release, attribute upsert, state-machine write, queue
// mutation, wait-set drain). Post-commit work (best-effort audit-log
// appends, leaf-run lineage emit, run-tree propagation, fan-out
// recalculate) is returned as a `postCommitFn` the caller invokes
// AFTER the outer tx commits.
//
// @blessed-invariant: Callback determinism. The phase-check read +
// terminal state mutation share one tx; the structural enforcement is
// at the two call sites (driveTerminal in callback.go, runner.go in
// the sync path) that open the outer tx and pass it through. Per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Callback determinism".
func applyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	// Plan I2: record the terminal verdict by class + error_class.
	metricsOf(args).IncTerminal(string(terminalClassFor(t.Kind)), t.ErrorClass)
	switch t.Kind {
	case terminalKindComplete:
		return applyTerminalComplete(ctx, args, acq, resolvedAttrs, schema, t, tx)
	case terminalKindErrored:
		return applyTerminalError(ctx, args, acq, t.ErrorClass, t.Payload, tx)
	case terminalKindInfra:
		return applyTerminalInfraError(ctx, args, acq, t.ErrorClass, t.Payload, tx)
	case terminalKindPark:
		return applyTerminalPark(ctx, args, acq, t, tx)
	}
	return nil, fmt.Errorf("applyTerminal: unhandled terminal kind %v", t.Kind)
}

// runApplyTerminal opens the outer state-mutation tx, threads it
// through applyTerminal, and runs the returned postCommit closure
// after the tx commits. Both the synchronous runner path
// (runner.go::RunNode) and the async-callback path
// (callback.go::driveTerminal) wrap their phase-check + apply-terminal
// chain in this helper so the determinism invariant is structurally
// enforced at every call site.
//
// `setup` is an optional hook the caller runs INSIDE the outer tx
// before applyTerminal — used by driveTerminal to perform the
// FOR-UPDATE phase check + populate acq.RunScopeID from the run row.
// Returning a non-nil error from `setup` skips applyTerminal entirely
// (the determinism path's ack-but-noop branch).
//
// @blessed-invariant: Callback determinism — the phase-check read +
// terminal state mutation share one tx.
func runApplyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent,
	setup func(ctx context.Context, tx persistence.Tx) (skip bool, err error),
) error {
	// Persist any NamedEvent emissions captured during the dispatch's
	// gRPC stream BEFORE applying the terminal verdict, per plan H1.
	// Each event opens its own short tx so per-row emitted_at
	// timestamps land in source order — under postgres NOW() is
	// constant for a tx, so threading these into the determinism tx
	// would collapse multi-emission ordering and break
	// `LatestByName` (see TestOnEventMultipleEmissionsLatestWins).
	// Named-event persistence is observability data and is not part of
	// the callback-determinism invariant. Failures are best-effort and
	// logged.
	if len(t.NamedEvents) > 0 {
		processNamedEvents(ctx, args, acq, t.NamedEvents)
	}
	var postCommit postCommitFn
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if setup != nil {
			skip, err := setup(ctx, tx)
			if err != nil {
				return err
			}
			if skip {
				return nil
			}
		}
		pc, err := applyTerminal(ctx, args, acq, resolvedAttrs, schema, t, tx)
		if err != nil {
			return err
		}
		postCommit = pc
		return nil
	}); err != nil {
		return err
	}
	if postCommit != nil {
		postCommit(ctx)
	}
	return nil
}

// terminalClassFor returns the metric label for a terminal kind. Kept
// in one place so additions to the kind enum don't drift between the
// metric labeling and the dispatch switch.
func terminalClassFor(k terminalKind) string {
	switch k {
	case terminalKindComplete:
		return "complete"
	case terminalKindErrored:
		return "errored"
	case terminalKindInfra:
		return "infra"
	case terminalKindPark:
		return "park"
	}
	return "unknown"
}

// applyTerminalError / applyTerminalPass live in
// runner_terminal_handlers.go (split out for cold-read 500-line file
// guideline compliance).

// @concept: last-outcome
//
// Writes the cascade-firing gate enum on every terminal. Sibling to
// `transition_reason` (see `.ok-planner/design/concepts/last-outcome.md`).
//
// applyTerminalComplete runs the §7.6 success-branch release tx
// alongside the state→fresh transition, final attribute upsert, and
// cascade message-pass to dependents.
//
// Sub-graph caller routing (E6): when this run is a sub-graph caller
// (the canonicalizer-emitted `IsSubgraphEntryAbsorbed` marker is set
// on the node-def), the success branch routes through
// `applyTerminalCompleteSubgraphCaller` instead. The sub-graph caller
// holds its locks across the internal-cascade fire and only releases
// at the parent run's aggregated terminal (driven by
// `state_propagation.go::PropagateFromChildState` on the last internal
// child's terminal). Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs / Invocation semantics + §Identity and absorption.
//
//	@concept: sub-graph
//	@concept: delegation
func applyTerminalComplete(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	merged := mergeAttributesDelta(resolvedAttrs, t.AttributesDel)
	if t.Changed && len(t.AttributesDel) > 0 && schema != nil {
		if err := attributes.Validate(schema, merged, attributes.PhaseCommit); err != nil {
			if appendErr := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "attributes_schema_failed",
				Payload: map[string]any{
					"errors": []map[string]any{{"message": err.Error()}},
				},
			}, tx); appendErr != nil && args.Logger != nil {
				args.Logger.Warn("runner_terminal: append attributes_schema_failed event failed",
					"node_id", acq.NodeID.String(),
					"error", appendErr.Error())
			}
			return applyErrorPolicy(ctx, args, acq, "attributes_schema_failed",
				map[string]any{"error": err.Error()}, tx)
		}
	}

	// E6 sub-graph caller routing. The canonicalizer flagged this node
	// with `IsSubgraphEntryAbsorbed: true` so the supervisor knows that
	// the executor that just terminated was the absorbed entry. On the
	// success branch the parent run stays `running` and the sub-graph's
	// non-entry internals dispatch as children of this run.
	if acq.NodeDef != nil && acq.NodeDef.IsSubgraphEntryAbsorbed {
		return applyTerminalCompleteSubgraphCaller(ctx, args, acq, merged, t, tx)
	}

	// Exit-node carry-rule: when this run is a sub-graph exit, copy its
	// writeback bytes onto the parent run's writeback row in the same tx
	// that records exit's terminal. Per spec §Sub-graphs / Writeback
	// carry-rule for exit.
	//
	// `isSubgraphExit` short-circuits the exit's own-attribute-row write
	// below: per spec, the exit is internal to the subgraph and not
	// externally addressable, so its row stays empty — only the parent's
	// row carries the bytes via applyTerminalCompleteSubgraphExit.
	isSubgraphExit := isSubgraphExitNode(acq)
	if isSubgraphExit {
		if err := applyTerminalCompleteSubgraphExit(ctx, args, acq, merged, tx); err != nil {
			return nil, err
		}
		// Fall through to the standard release/cascade path below so
		// exit's own state transitions to `fresh` and the parent
		// aggregator picks up the child's terminal via
		// PropagateFromChildState — but skip the exit's own attribute
		// row write (handled by the isSubgraphExit guard around
		// upsertFinalAttributesTx).
	}

	// Per-node quality-rule evaluation retired by the 2026-05-15
	// data-platform-extensions plan P1. The verifier-shape-checks /
	// verifier-http executors (Section I) replace inline quality rules;
	// failures surface as `executor_errored` with
	// `error_class: "verifier_failed"`.

	// Resolve the on_executor_complete handler. Default = by_changed
	// (today's behavior).
	resolve := node.ResolveByChanged
	var completeHandler *node.OnExecutorCompleteHandler
	if acq.NodeDef != nil && acq.NodeDef.OnExecutorComplete != nil {
		completeHandler = acq.NodeDef.OnExecutorComplete
		if completeHandler.Resolve != "" {
			resolve = completeHandler.Resolve
		}
	}
	var lastOutcome cascade.LastOutcome
	switch resolve {
	case node.ResolveByChanged:
		if t.Changed {
			lastOutcome = cascade.LastOutcomeFreshChanged
		} else {
			lastOutcome = cascade.LastOutcomeFreshUnchanged
		}
	case node.ResolveAlwaysPropagate:
		lastOutcome = cascade.LastOutcomeFreshChanged
	case node.ResolveNeverPropagate:
		lastOutcome = cascade.LastOutcomeFreshUnchanged
	default:
		// Validator should have caught this, but defensive fallback.
		if t.Changed {
			lastOutcome = cascade.LastOutcomeFreshChanged
		} else {
			lastOutcome = cascade.LastOutcomeFreshUnchanged
		}
	}

	// Primary state-mutation work runs inline in the caller's outer tx.
	// Per @blessed-invariant: Callback determinism — phase-check read
	// and these writes must share one tx.
	if err := releaseLocksInTx(ctx, args, tx, acq, true); err != nil {
		return nil, err
	}
	// Per spec §Sub-graphs / Writeback carry-rule for exit: the
	// exit's own attribute row stays empty because the exit is
	// internal to the subgraph and not externally addressable. The
	// parent run's row was already populated by
	// applyTerminalCompleteSubgraphExit above.
	if !isSubgraphExit {
		if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
			return nil, fmt.Errorf("applyTerminalComplete: upsert attributes: %w", err)
		}
	}
	if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{}, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: clear error state: %w", err)
	}
	// running → fresh via the on_executor_complete handler.
	// Thread acq.RunScopeID so fan-out children's state-machine
	// update lands on the correct sibling row.
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
		cascade.NodeStateFresh, cascade.ReasonHandlerComplete, lastOutcome, tx); err != nil {
		return nil, err
	}
	// Flip the just-completed run row to a terminal phase BEFORE the
	// cascade walk fires. Without this the row stays in
	// phase='active' until the outer supervisor.go / callback.go
	// post-apply `Queue.Complete` call, which means
	// `MarkStaleForCascade`'s `NOT EXISTS (phase IN
	// pending/active/held/parked)` guard rejects self-edges during
	// the walk — `frame: in` self-subscriptions can't insert their
	// new pending run because runOld is still active. Mirrors the
	// in-tx phase flip every other terminal already does
	// (`applyTerminalPass` at runner_terminal_handlers.go:109;
	// `applyErrorPolicy` / `applyTerminalInfraError` at
	// runner_error_policy.go:217/239/283; `applyTerminalPark` via
	// `ParkActiveInTx`). Outer `Queue.Complete` calls in
	// `supervisor.go` and `callback.go` become idempotent no-ops on
	// every known happy path (their WHERE clauses filter on active
	// phase set); kept as belt-and-suspenders cleanup.
	//
	//	@concept: node-run
	//	@concept: cascade
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: remove for node: %w", err)
	}
	// Cascade-on-fresh_changed propagation: when this sender settles
	// fresh_changed, the recursive subscription walk marks
	// downstream subscribers stale-with-frame_id and gates the
	// downstream subgraph (R=C, S=B; etc.). The same tx then drains
	// rows where this sender is the gating sender (R=B, S=A) so
	// direct subscribers can advance — the immediate inserts at
	// the first-level (R=B, S=A) are intentionally transient
	// (insert-then-drain in same tx is benign; the deeper levels
	// gate properly). Cascade fires iff last_outcome == fresh_changed
	// per `concept:cascade`'s firing gate invariant; diverges under
	// always_propagate / never_propagate (lifecycle-handler.go).
	//
	// This walk is complementary to the cascade-on-invalidation
	// walks at invalidateInFrame / applyResolvedAction / etc. The
	// invalidation-side walks gate receivers across multiple
	// in-flight senders (multi-invalidator); the settlement-side
	// walk gates the initial-instance case (non-root subscribers
	// that never went through an explicit invalidation transition
	// from a settled state but still need frame_id stamping +
	// wait-set rows seeded under the recursive deeper levels).
	//
	//	@concept: cascade
	//	@concept: wait-set
	if lastOutcome == cascade.LastOutcomeFreshChanged {
		if err := cascadeSubscribersStaleInTx(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID); err != nil {
			return nil, err
		}
	}
	// Settled-state drain: the sender just reached `fresh`. Any
	// wait-set rows the sender was gating get removed in bulk so
	// downstream receivers can advance.
	//
	//	@concept: wait-set
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, err
	}

	commitKind := "attributes_committed"
	if !t.Changed {
		commitKind = "no_op_commit"
	}
	_ = completeHandler

	// Post-commit work: best-effort audit-log appends, lineage emit,
	// fan-out recalculate, run-tree propagation. Each opens its own tx
	// (or runs further out-of-tx work like PropagateIfChildAfterTerminal
	// which walks the run-tree under its own transactions); they MUST
	// run after the outer tx commits to avoid nested-tx deadlock under
	// the SQLite single-conn pool.
	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: commitKind,
				Payload: map[string]any{
					"changed":        t.Changed,
					"updated_fields": fieldNames(t.AttributesDel),
					"change_summary": t.ChangeSummary,
					"last_outcome":   string(lastOutcome),
				},
			}, tx); err != nil {
				return err
			}
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "work_completed",
				Payload: map[string]any{
					"outcome":        outcomeForChanged(t.Changed),
					"change_summary": t.ChangeSummary,
					"node_type":      acq.NodeType,
					"last_outcome":   string(lastOutcome),
				},
			}, tx)
		}); err != nil && args.Logger != nil {
			args.Logger.Warn("runner_terminal: append commit/work_completed events failed",
				"node_id", acq.NodeID.String(),
				"commit_kind", commitKind,
				"error", err.Error())
		}
		if lastOutcome == cascade.LastOutcomeFreshChanged {
			fanoutRecalculate(ctx, args, acq)
		}
		// E8: emit leaf-run lineage record. Spec §Content lineage.
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:       acq.InstanceID,
			FrameID:          acq.FrameID,
			RunID:            dispatchID,
			NodeID:           acq.NodeID,
			State:            string(cascade.NodeStateFresh),
			LastOutcome:      string(lastOutcome),
			Changed:          t.Changed,
			TerminalKind:     "complete",
			NodeAlias:        acq.NodeType,
			ExecutorName:     acq.Executor,
			TemplateHash:     acq.TemplateHash,
			Params:           acq.InstanceParams,
			AttributesMerged: acq.MergedAttributes,
			HeldClaims:       HeldClaimsForLineage(acq),
			ParentRunID:      scope.ParentRunID,
			ChildKey:         scope.PartitionKey,
			SubstitutionRefs: CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
		// Run-tree state propagation (E2): if this run is a child
		// (fan-out or sub-graph internal), aggregate up to the parent.
		// No-op on root runs.
		if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
			cascade.NodeStateFresh, lastOutcome); err != nil {
			args.Logger.Warn("applyTerminalComplete: run-tree propagation failed",
				"run_id", dispatchID.String(), "error", err.Error())
		}
	}
	return post, nil
}

// cascadeSubscribersStaleInTx marks subscriber nodes stale + frame_id
// and inserts wait-set rows in the same tx as the sender's INVALIDATION
// transition (settled → stale/running). Per the pessimistic-invalidate
// rule (spec Piece 1), the walk fires at sender invalidation; receivers
// are gated by their wait-set until every upstream sender resolves and
// the settled-state drain releases the rows. The walk also fires at
// the sender's fresh_changed settlement to cover the initial-instance
// case (non-root subscribers that never went through an explicit
// invalidation transition); the BFS recursion creates wait-set rows at
// deeper levels (R=C, S=B) that DON'T immediately drain when only the
// first-level sender's drain (sender=A) fires.
//
// The walk is recursive over the subscription graph within the
// instance: each receiver R that is newly marked stale is itself an
// invalidation site, so the walk processes R's subscribers in turn
// (BFS over the inverse-edge map). A per-call visited set guards
// against subscription cycles.
//
// Receivers are resolved from the cached per-template subscription-edge
// inverse map. Edges with frame:in are processed in-tx (stale-mark +
// wait-set insert against the sender's frame). Edges with frame:next
// open a new frame via frame.EnqueueOrCoalesce; the receiver becomes
// a frame source for the new frame and is stamped by
// MarkSourceNodeStale at frame-open. No wait-set row is inserted for
// frame:next at insertion time — by the time the new frame opens the
// sender has already settled, so a gate keyed on the current sender
// would never drain. The new frame's own cascade walks cover deeper
// gating on the receiver's own subscribers.
//
// The settled-state drain (drainWaitSetOnSettled) marks the rows
// (sets drained_at) when the sender reaches any settled state
// (fresh/failed/parked); drained rows stay queryable for the
// substitution-context builder. The pessimistic-invalidate rule
// inserts a wait-set row for every subscription edge regardless of
// filter compatibility; idempotent re-fire handles filter mismatch.
//
//	@concept: cascade
//	@concept: wait-set
func cascadeSubscribersStaleInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
) error {
	inst, err := args.Persist.Instances().Get(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: get instance: %w", err)
	}
	if inst == nil {
		return nil
	}
	edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: edges: %w", err)
	}
	if len(edges) == 0 {
		return nil
	}
	// Resolve receiver node-types → node-IDs within the instance once.
	instNodes, err := args.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: list instance nodes: %w", err)
	}
	byType := make(map[string][]persistence.NodeRow, len(instNodes))
	for _, n := range instNodes {
		byType[n.NodeType] = append(byType[n.NodeType], n)
	}
	// Resolve the sender's RunScope: same-scope cascade is the common
	// case — the receiver inherits the sender's RunScope. Cross-scope
	// propagation is bridged by the caller:
	//
	//   - Sub-graph entry-success cascading into sub-graph internal
	//     nodes: handled by the entry-absorbed marker path in
	//     code:runtime/subgraph_dispatch.go.
	//   - Fan-out / sub-graph parent settlement cascading to the parent's
	//     downstream subscribers: handled by
	//     code:runtime/state_propagation.go::PropagateIfChildAfterTerminal,
	//     which fires a fresh cascadeSubscribersStaleInTx rooted at the
	//     parent run's main-scope id when the propagation walker settles
	//     a parent at a terminal state.
	//
	// Non-main scopes (fanout_partition, sub-graph) are CLOSED contexts:
	// only nodes that have been explicitly dispatched into them belong.
	// When the sender lives in a non-main scope and a receiver does NOT
	// already have an in-flight row in that scope, the receiver is not
	// a member of the scope — it lives in some ancestor scope (typically
	// main). The walker MUST NOT lazy-allocate a new row for that
	// receiver in the sender's scope: doing so creates an orphan row in
	// the wrong scope (which then never gets dispatched cleanly because
	// the scope closes during parent aggregation) and bypasses the
	// cross-scope bridge. Per concept:run-scope §"Lifecycle / RunScope
	// closure" + the F1/strict-cascade scenario invariants.
	//
	// @concept: run-scope
	senderRun, err := args.Persist.RunTree().GetByID(ctx, tx, senderRunID)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: load sender run: %w", err)
	}
	if senderRun == nil {
		return nil
	}
	senderRunScopeID := senderRun.RunScopeID
	// Detect non-main sender scope so the walker can refuse to
	// lazy-allocate run rows for cross-scope receivers. Main RunScopes
	// have ParentRunID == nil; non-main scopes (sub-graph,
	// fanout_partition) carry a ParentRunID.
	senderRunScope, err := args.Persist.RunScopes().GetByID(ctx, tx, senderRunScopeID)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: load sender run scope: %w", err)
	}
	if senderRunScope == nil {
		return nil
	}
	senderScopeIsMain := senderRunScope.ParentRunID == nil
	// BFS over the subscription graph rooted at the sender. Each
	// receiver newly marked stale joins the queue so its own subscribers
	// are processed in turn (cycle-guarded by visited). `runID` carries
	// the in-flight run id for each node visited so wait-set INSERTs
	// (post-stage-5 keyed by run id) bind to the right run.
	type walkItem struct {
		nodeID   foundationshared.UUID
		nodeType string
		runID    foundationshared.UUID
	}
	queue := []walkItem{{nodeID: senderID, nodeType: senderNodeType, runID: senderRunID}}
	visited := map[foundationshared.UUID]struct{}{senderID: {}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		candidateEdges := append([]node.SubscriptionEdge{}, edges[cur.nodeType]...)
		candidateEdges = append(candidateEdges, edges[""]...)
		if len(candidateEdges) == 0 {
			continue
		}
		for _, edge := range candidateEdges {
			receivers := byType[edge.ReceiverNodeType]
			for _, r := range receivers {
				switch edge.Frame {
				case node.FrameNext:
					// Open a new frame for the receiver's instance.
					// Per spec Piece 1 "frame: next wait-set
					// placement," frame:next subscriptions fire the
					// receiver in the NEXT frame. EnqueueOrCoalesce
					// writes to rimsky_frames in the caller's tx; the
					// receiver becomes a frame source for the new
					// frame and is stamped with the new frame's id by
					// MarkSourceNodeStale at frame-open. We do NOT
					// insert a wait-set row keyed on the current sender
					// here — by the time the new frame opens, the
					// sender has already settled, so a gate keyed on
					// the current sender would never drain. The new
					// frame's own cascade walks (firing on the
					// receiver's own invalidation as a frame source)
					// gate the receiver's downstream subscribers.
					if _, fErr := frame.EnqueueOrCoalesce(ctx, args.Persist, tx, instanceID, r.ID); fErr != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: enqueue next-frame for %s: %w", r.ID, fErr)
					}
					// If the receiver is parked, the next-frame open
					// alone won't wake it (MarkSourceNodeStale skips
					// parked rows because parked is settled-with-
					// frame_id and the predicate gates fresh /
					// stale-with-nil-frame). Drive the parked → stale
					// wake here so the new frame can pick up the
					// receiver as a source.
					//
					//	@concept: parked-state
					//	@concept: cascade
					if r.State == cascade.NodeStateParked {
						if err := wakeParkedReceiverInTx(ctx, args, tx, r, senderFrameID); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: wake parked next-frame %s: %w", r.ID, err)
						}
					}
					continue
				default: // FrameIn or empty (in-tx)
					// Self-edges are permitted in both FrameIn and
					// FrameNext branches as first-class drain-my-own-
					// queue idioms. The two spellings differ in framing:
					// FrameIn keeps iteration inside the current frame
					// (one long-running frame, supervisor picks up each
					// new pending run as it lands); FrameNext opens a
					// fresh frame per iteration (one frame per queue
					// item, cleaner frame.start/frame.end markers per
					// operator's eye).
					//
					// Insert-then-drain-in-same-tx makes FrameIn safe:
					// the wait-set row this branch inserts (gating the
					// new pending run on this commit's run) gets cleared
					// by drainWaitSetOnSettled at the end of
					// applyTerminalComplete in the same tx, before the
					// supervisor sees it. The BFS `visited` set blocks
					// indirect cycles. MarkStaleForCascade does NOT
					// touch rimsky_nodes.state (only inserts a new run
					// row + re-stamps frame_id), so the just-committed
					// state=fresh, last_outcome=fresh_changed survives
					// intact for downstream consumers.
					//
					//	@concept: cascade
					//	@concept: node-subscription
					// Parked receivers need their parked node-run row
					// resumed alongside the stale stamp; without that
					// the queue still carries phase='parked' and the
					// supervisor never picks the row up. Cascade walks
					// previously skipped parked receivers (the unified
					// InvalidateNode wake path was the only resumption
					// route); per spec Piece 1 the cascade walk must
					// cover parked receivers too.
					//
					//	@concept: parked-state
					//	@concept: cascade
					// Same-scope membership check. Non-main scopes
					// (sub-graph, fanout_partition) are closed
					// contexts: a receiver belongs to the sender's
					// scope only if it already has an in-flight row
					// there. The lazy-allocation discipline of
					// AffirmNodeRunRow only applies to main RunScopes;
					// for non-main scopes, allocating a new row for a
					// cross-scope receiver creates an orphan in the
					// wrong scope (which then gets stranded when the
					// scope closes during parent aggregation). The
					// cross-scope bridge in
					// state_propagation.PropagateIfChildAfterTerminal
					// handles the receiver via the parent's
					// settlement cascade.
					//
					// @concept: run-scope
					receiverRunScopeID := senderRunScopeID
					if !senderScopeIsMain {
						existingID, existingOK, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
						if err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: probe receiver run %s: %w", r.ID, err)
						}
						if !existingOK {
							// Cross-scope receiver — skip; the bridge
							// at the parent's terminal handles it.
							continue
						}
						_ = existingID
					}
					// Affirm-then-read: under RunScope-first, the
					// cascade walker is the lazy-allocation primitive
					// for the receiver's in-flight row. AffirmNodeRunRow
					// INSERTs a pending stale row keyed on
					// (receiver_node_id, sender_run_scope_id) when
					// none exists; no-op if one already does. The
					// subsequent GetInFlightRunForNode read returns
					// the row id under the same tx. Per
					// concept:run-scope §"Persistence primitives /
					// AffirmNodeRunRow" — every cascade match within
					// the same RunScope produces an in-flight row.
					//
					// @concept: run-scope
					// @blessed-invariant: AffirmNodeRunRow
					// no-return-value-dependency.
					if err := args.Persist.Nodes().AffirmNodeRunRow(ctx, r.ID, receiverRunScopeID, senderFrameID, tx); err != nil {
						// Defensive: a closed RunScope means the
						// receiver's scope rendezvous has fired and
						// is no longer accepting new in-flight rows.
						// The cascade walker MUST NOT cross into
						// closed RunScopes per concept:run-scope;
						// skip this receiver and continue the walk.
						if errors.Is(err, persistence.ErrRunScopeClosed) {
							continue
						}
						return fmt.Errorf("cascadeSubscribersStaleInTx: affirm receiver run %s: %w", r.ID, err)
					}
					receiverRunID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver run %s: %w", r.ID, err)
					}
					if !ok {
						// Race-with-terminal: the receiver's row
						// just terminated between affirm and read.
						// Safe to skip — its terminal handler will
						// drive its own cascade walk.
						continue
					}
					if r.State == cascade.NodeStateParked {
						if err := wakeParkedReceiverInTx(ctx, args, tx, r, senderFrameID); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: wake parked %s: %w", r.ID, err)
						}
					} else {
						if err := args.Persist.Nodes().MarkStaleForCascade(ctx, receiverRunID, senderFrameID, tx); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: mark stale %s: %w", r.ID, err)
						}
					}
					if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
						FrameID:           senderFrameID,
						ReceiverRunID:     receiverRunID,
						SenderRunID:       cur.runID,
						TopicKind:         edge.TopicKind,
						SubscriptionScope: edge.SubscriptionScope,
					}, tx); err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: wait-set insert: %w", err)
					}
					// Hard-dep pull: for each hard_dep attribute read the
					// receiver declares, ensure the upstream has an
					// in-flight run in this frame and a wait-set blocker
					// on the receiver. The outer BFS's `visited` set is
					// threaded down so the hard-dep walk skips upstreams
					// already covered by the subscription BFS — pathological
					// mixed soft+hard topologies stay bounded.
					// The upstream lives in the same RunScope as the
					// receiver (hard-dep is intra-scope; cross-scope
					// hard-deps are not expressible).
					if err := pullHardDepUpstreams(ctx, args, tx, r, byType, receiverRunID, receiverRunScopeID, senderFrameID, inst.TemplateHash, visited); err != nil {
						return err
					}
					// Recurse via BFS: the receiver R is itself newly
					// invalidated, so its subscribers must also be
					// marked stale + gated. The visited set guards
					// subscription cycles.
					if _, seen := visited[r.ID]; !seen {
						visited[r.ID] = struct{}{}
						queue = append(queue, walkItem{nodeID: r.ID, nodeType: r.NodeType, runID: receiverRunID})
					}
				}
			}
		}
	}
	return nil
}

// pullHardDepUpstreams consults the per-template hard-dep edge map for
// receiver `r` and, for each declared upstream X, ensures X has an
// in-flight run in this frame and a wait-set blocker installed on the
// receiver. When X has no current-frame run, the helper proactively
// stale-marks + cascade-walks X within the same tx. All work happens
// inline — NOT via InvalidateNode (which opens its own tx and would
// self-deadlock with the caller's tx).
//
// The `visited` set is the outer BFS's cycle-guard (in
// `cascadeSubscribersStaleInTx`). Upstreams already visited by that BFS
// are skipped to bound work in pathological mixed soft+hard topologies.
// Upstreams newly pulled by this helper are added to `visited` so the
// outer BFS sees them as already-processed.
//
//	@concept: cascade
//	@concept: attribute
func pullHardDepUpstreams(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	receiver persistence.NodeRow,
	byType map[string][]persistence.NodeRow,
	receiverRunID foundationshared.UUID,
	targetRunScopeID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	templateHash string,
	visited map[foundationshared.UUID]struct{},
) error {
	hardEdges, err := hardDepEdgesForTemplate(ctx, args, templateHash, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: hard-dep edges: %w", err)
	}
	if len(hardEdges) == 0 {
		return nil
	}
	if len(hardEdges[receiver.NodeType]) == 0 {
		return nil
	}
	for _, upstreamType := range hardEdges[receiver.NodeType] {
		upstreamNodes := byType[upstreamType]
		if len(upstreamNodes) == 0 {
			continue // defensive: template validator should have caught
		}
		upstreamNode := upstreamNodes[0] // one node per type per instance

		// Outer-BFS visited-set check: if the subscription BFS already
		// processed this upstream, skip the hard-dep pull. The wait-set
		// row that the outer walk inserted (or skipped) is already the
		// gate for this frame; redoing the wake / stale-mark would
		// duplicate work and could surface as repeated audit events.
		if _, seen := visited[upstreamNode.ID]; seen {
			continue
		}

		// Parked-upstream handling (BEFORE AffirmNodeRunRow).
		//
		// Under RunScope-first GetInFlightRunForNode includes phase=
		// 'parked' rows (the unique-per-RunScope in-flight predicate
		// covers the four in-flight phases). So we can't rely on
		// hasRun=false to detect parked upstreams. Probe explicitly via
		// GetParkedByNode (frame-agnostic) first, wake the parked run
		// if any, and only then fall through to the affirm-and-read
		// path. The wake transitions parked → pending in-place at the
		// new frame so AffirmNodeRunRow's NOT EXISTS guard correctly
		// no-ops and the subsequent GetInFlightRunForNode resolves the
		// resumed row.
		//
		//	@concept: parked-state
		//	@concept: run-scope
		//	@concept: cascade
		upstreamRunScopeID := targetRunScopeID
		parked, err := args.Queue.GetParkedByNode(ctx, upstreamNode.ID, upstreamRunScopeID)
		if err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: get parked upstream %s: %w",
				upstreamType, err)
		}
		if parked != nil {
			// `wakeParkedReceiverInTx` rebinds the run's frame_id
			// internally — no separate RebindRunFrameInTx call here.
			if err := wakeParkedReceiverInTx(ctx, args, tx, upstreamNode, senderFrameID); err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: wake parked hard-dep upstream %s: %w",
					upstreamType, err)
			}
		}

		// Affirm-then-read: the upstream lives in the same RunScope as
		// the receiver (hard-dep is intra-scope by construction —
		// cross-scope hard-deps are not expressible). AffirmNodeRunRow
		// INSERTs a pending row keyed on (upstream_node_id,
		// target_run_scope_id) when none exists.
		//
		// @concept: run-scope
		// @blessed-invariant: AffirmNodeRunRow no-return-value-dependency.
		if err := args.Persist.Nodes().AffirmNodeRunRow(ctx, upstreamNode.ID, upstreamRunScopeID, senderFrameID, tx); err != nil {
			// Defensive: a closed RunScope means the upstream's scope
			// rendezvous has fired. Hard-dep upstreams in closed scopes
			// cannot be reactivated — skip; the receiver's wait-set is
			// not populated for this upstream, and the receiver
			// re-evaluates substitutions when it next dispatches.
			if errors.Is(err, persistence.ErrRunScopeClosed) {
				continue
			}
			return fmt.Errorf("cascadeSubscribersStaleInTx: affirm upstream %s: %w", upstreamType, err)
		}
		upstreamRunID, hasRun, err := args.Queue.GetInFlightRunForNode(
			ctx, tx, upstreamNode.ID, upstreamRunScopeID,
		)
		if err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: get in-flight upstream %s: %w",
				upstreamType, err)
		}

		if !hasRun {
			// Pass the just-affirmed upstreamRunScopeID through —
			// upstreamNode.RunScopeID on the NodeRow projection is stale
			// (loaded before this AffirmNodeRunRow call); the affirm may
			// have just attached a new in-flight row to upstreamRunScopeID.
			if err := stalemarkAndEnqueueInFrame(
				ctx, args, tx, &upstreamNode, upstreamRunScopeID, senderFrameID,
			); err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: stale-mark upstream %s: %w",
					upstreamType, err)
			}
			upstreamRunID, hasRun, err = args.Queue.GetInFlightRunForNode(
				ctx, tx, upstreamNode.ID, upstreamRunScopeID,
			)
			if err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: re-fetch in-flight upstream %s after stale-mark: %w",
					upstreamType, err)
			}
			if !hasRun {
				return fmt.Errorf("cascadeSubscribersStaleInTx: upstream %s not in-flight after stale-mark",
					upstreamType)
			}
		}

		// Mark this upstream visited so the outer BFS (and a subsequent
		// hard-dep pull during the same walk) doesn't re-process it.
		visited[upstreamNode.ID] = struct{}{}

		// Insert wait-set blocker for the receiver on this upstream's run.
		if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           senderFrameID,
			ReceiverRunID:     receiverRunID,
			SenderRunID:       upstreamRunID,
			TopicKind:         "attribute",
			SubscriptionScope: "direct",
		}, tx); err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: insert hard-dep wait-set: %w", err)
		}
	}
	return nil
}

// drainWaitSetOnSettled marks every wait-set row in the current frame
// where this sender's run appears as drained (sets drained_at = NOW()),
// in bulk. Called wherever the sender reaches any settled state
// (fresh/failed/parked). Idempotent: a re-drain leaves the prior
// drained_at intact. Post-2026-05-20 keying, drain marks rather than
// deletes — drained rows stay queryable for the substitution-context
// builder (see runtime/substitution_context.go).
//
//	@concept: wait-set
func drainWaitSetOnSettled(
	ctx context.Context, args RunArgs, tx persistence.Tx, frameID, senderRunID foundationshared.UUID,
) error {
	return args.Persist.WaitSet().MarkDrainedBySender(ctx, frameID, senderRunID, tx)
}

// fanoutRecalculate routes RecalculateNode at each subscribed receiver
// post-commit. Resolves the receiver set from the per-template
// subscription-edge inverse map (the same map cascadeSubscribersStaleInTx
// walks in-tx); this post-commit walk routes the recalculate event so
// the receiver re-evaluates its wait-set and may enqueue dispatch.
func fanoutRecalculate(ctx context.Context, args RunArgs, acq *acquisition) {
	var receivers []persistence.NodeRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := args.Persist.Instances().Get(ctx, acq.InstanceID, tx)
		if err != nil || inst == nil {
			return err
		}
		edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
		if err != nil || len(edges) == 0 {
			return err
		}
		candidate := append([]node.SubscriptionEdge{}, edges[acq.NodeType]...)
		candidate = append(candidate, edges[""]...)
		if len(candidate) == 0 {
			return nil
		}
		// Resolve receiver node-types → node IDs.
		receiverTypes := make(map[string]struct{}, len(candidate))
		for _, e := range candidate {
			receiverTypes[e.ReceiverNodeType] = struct{}{}
		}
		instNodes, err := args.Persist.Nodes().ListByInstance(ctx, acq.InstanceID, tx)
		if err != nil {
			return err
		}
		for _, n := range instNodes {
			if n.ID == acq.NodeID {
				continue
			}
			if _, ok := receiverTypes[n.NodeType]; ok {
				receivers = append(receivers, n)
			}
		}
		return nil
	}); err != nil {
		return
	}
	src := acq.NodeID
	for _, r := range receivers {
		_ = RecalculateNode(ctx, RecalculateArgs{
			Persist:      args.Persist,
			Queue:        args.Queue,
			Clock:        args.Clock,
			Logger:       args.Logger,
			SourceNodeID: &src,
			TargetNodeID: r.ID,
		})
	}
}

// Error-resolution branch functions (applyErrorPolicy,
// applyResolvedAction, applyTerminalInfraError, lookupPolicyForNode,
// requiredStoresForAcq, invalidateTargets) live in
// runner_terminal_errors.go. Release-path functions
// (releaseLocksInTx, releaseAcquiredLock, releaseClaim,
// releaseInheritedClaimsInTx, releaseActionString,
// emitLockReleased) live in runner_terminal_release.go. Both files
// were split out of runner_terminal.go to keep that file under the
// cold-read 500-line guideline.

// upsertFinalAttributesTx writes the merged-and-validated attribute
// object back inside the supplied tx. Per spec §5.7.2 the executor
// may have written incremental fields via the §12.5 callback; the
// final row is `prior.Data + merged` so those incremental writes are
// preserved.
func upsertFinalAttributesTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, merged map[string]any,
) error {
	prior, _ := args.Persist.NodeAttributes().GetByRun(ctx, acq.DispatchID, tx)
	final := merged
	if prior != nil && len(prior.Data) > 0 {
		combined := make(map[string]any, len(prior.Data)+len(merged))
		for k, v := range prior.Data {
			combined[k] = v
		}
		for k, v := range merged {
			combined[k] = v
		}
		final = combined
	}
	if final == nil {
		final = map[string]any{}
	}
	return args.Persist.NodeAttributes().Upsert(ctx, acq.DispatchID, acq.NodeID, final, tx)
}

// mergeAttributesDelta shallow-merges the executor's attributes_delta
// into the substituted attribute object.
func mergeAttributesDelta(base, delta map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(delta))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range delta {
		out[k] = v
	}
	return out
}

func outcomeForChanged(changed bool) string {
	if changed {
		return "committed"
	}
	return "no_op"
}
