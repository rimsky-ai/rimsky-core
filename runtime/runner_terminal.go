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
	"fmt"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	foundationshared "github.com/fallguy/rimsky/foundation/shared"
	attributes "github.com/fallguy/rimsky/graph/attribute"
	"github.com/fallguy/rimsky/graph/frame"
	"github.com/fallguy/rimsky/graph/node"
)

// applyTerminal is the omnibus runner's terminal-event entry point.
func applyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent,
) error {
	// Persist any NamedEvent emissions captured during the dispatch's
	// gRPC stream BEFORE applying the terminal verdict, per plan H1.
	// Failures here are best-effort and logged — events that fail to
	// persist do not block the terminal verdict.
	if len(t.NamedEvents) > 0 {
		processNamedEvents(ctx, args, acq, t.NamedEvents)
	}
	// Plan I2: record the terminal verdict by class + error_class.
	metricsOf(args).IncTerminal(string(terminalClassFor(t.Kind)), t.ErrorClass)
	switch t.Kind {
	case terminalKindComplete:
		return applyTerminalComplete(ctx, args, acq, resolvedAttrs, schema, t)
	case terminalKindErrored:
		return applyTerminalError(ctx, args, acq, t.ErrorClass, t.Payload)
	case terminalKindInfra:
		return applyTerminalInfraError(ctx, args, acq, t.ErrorClass, t.Payload)
	case terminalKindPark:
		return applyTerminalPark(ctx, args, acq, t)
	}
	return fmt.Errorf("applyTerminal: unhandled terminal kind %v", t.Kind)
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
	t terminalEvent,
) error {
	merged := mergeAttributesDelta(resolvedAttrs, t.AttributesDel)
	if t.Changed && len(t.AttributesDel) > 0 && schema != nil {
		if err := attributes.Validate(schema, merged, attributes.PhaseCommit); err != nil {
			if appendErr := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
					NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
					Kind: "attributes_schema_failed",
					Payload: map[string]any{
						"errors": []map[string]any{{"message": err.Error()}},
					},
				}, tx)
			}); appendErr != nil && args.Logger != nil {
				args.Logger.Warn("runner_terminal: append attributes_schema_failed event failed",
					"node_id", acq.NodeID.String(),
					"error", appendErr.Error())
			}
			return applyErrorPolicy(ctx, args, acq, "attributes_schema_failed",
				map[string]any{"error": err.Error()})
		}
	}

	// E6 sub-graph caller routing. The canonicalizer flagged this node
	// with `IsSubgraphEntryAbsorbed: true` so the supervisor knows that
	// the executor that just terminated was the absorbed entry. On the
	// success branch the parent run stays `running` and the sub-graph's
	// non-entry internals dispatch as children of this run.
	if acq.NodeDef != nil && acq.NodeDef.IsSubgraphEntryAbsorbed {
		return applyTerminalCompleteSubgraphCaller(ctx, args, acq, merged, t)
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
		if err := applyTerminalCompleteSubgraphExit(ctx, args, acq, merged); err != nil {
			return err
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

	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := releaseLocksInTx(ctx, args, tx, acq, true); err != nil {
			return err
		}
		// Per spec §Sub-graphs / Writeback carry-rule for exit: the
		// exit's own attribute row stays empty because the exit is
		// internal to the subgraph and not externally addressable. The
		// parent run's row was already populated by
		// applyTerminalCompleteSubgraphExit above.
		if !isSubgraphExit {
			if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
				return fmt.Errorf("applyTerminalComplete: upsert attributes: %w", err)
			}
		}
		if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{}, tx); err != nil {
			return fmt.Errorf("applyTerminalComplete: clear error state: %w", err)
		}
		// running → fresh via the on_executor_complete handler.
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
			cascade.NodeStateFresh, cascade.ReasonHandlerComplete, lastOutcome, tx); err != nil {
			return err
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
				return err
			}
		}
		// Settled-state drain: the sender just reached `fresh`. Any
		// wait-set rows the sender was gating get removed in bulk so
		// downstream receivers can advance.
		//
		//	@concept: wait-set
		if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	commitKind := "attributes_committed"
	if !t.Changed {
		commitKind = "no_op_commit"
	}
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
	// E8: emit leaf-run lineage record. Spec §Content lineage. Bytes
	// are inert in rimsky (@blessed-invariant 20/21); the lineage row
	// carries hashes + run identifiers + last_outcome, not raw bytes.
	// MergedAttributes is the post-resolution + post-override shape —
	// the hash reflects what shipped to the executor (not the
	// pre-merge override blob).
	EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
		InstanceID:       acq.InstanceID,
		FrameID:          acq.FrameID,
		RunID:            acq.DispatchID,
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
		ParentRunID:      acq.ParentRunID,
		SubstitutionRefs: CollectSubstitutionRefsForEmit(ctx, args, acq),
	})
	// Run-tree state propagation (E2): if this run is a child (fan-out
	// or sub-graph internal), aggregate up to the parent. No-op on root
	// runs.
	if _, err := PropagateIfChildAfterTerminal(ctx, args, acq.DispatchID,
		cascade.NodeStateFresh, lastOutcome); err != nil {
		args.Logger.Warn("applyTerminalComplete: run-tree propagation failed",
			"run_id", acq.DispatchID.String(), "error", err.Error())
	}
	// Per the 2026-05-14 subscription-cascade resolution, the
	// invalidate-emit slot retired; cascade coupling is declared
	// receiver-side via Subscribes. cascadeSubscribersStaleInTx
	// (called above when last_outcome == fresh_changed) handles the
	// recursive subscription walk + wait-set inserts for downstream
	// receivers.
	_ = completeHandler
	return nil
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
				if r.ID == cur.nodeID {
					continue
				}
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
					if r.State == cascade.NodeStateParked {
						if err := wakeParkedReceiverInTx(ctx, args, tx, r, senderFrameID); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: wake parked %s: %w", r.ID, err)
						}
					} else {
						if _, err := args.Persist.Nodes().MarkStaleForCascade(ctx, r.ID, senderFrameID, tx); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: mark stale %s: %w", r.ID, err)
						}
					}
					// Resolve the receiver's in-flight run id (just
					// inserted/refreshed by MarkStaleForCascade or
					// wakeParkedReceiverInTx) so the wait-set row keys on
					// per-run identity post-stage-5.
					receiverRunID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, senderFrameID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver run %s: %w", r.ID, err)
					}
					if !ok {
						// No in-flight run row — either the receiver is
						// already terminal in this frame (race with a
						// concurrent dispatcher) or the stale-mark path
						// elected not to enqueue. Skip the wait-set INSERT;
						// without a run row there's no gate to install.
						continue
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
					if err := pullHardDepUpstreams(ctx, args, tx, r, byType, receiverRunID, senderFrameID, inst.TemplateHash, visited); err != nil {
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

		upstreamRunID, hasRun, err := args.Queue.GetInFlightRunForNode(
			ctx, tx, upstreamNode.ID, senderFrameID,
		)
		if err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: get in-flight upstream %s: %w",
				upstreamType, err)
		}

		// Parked-upstream handling.
		//
		// `GetInFlightRunForNode` filters to (frame=senderFrameID,
		// phase IN (pending,active,held)); it does NOT return parked
		// runs. So `hasRun=true` here means the upstream is already
		// pending/active/held in this frame — the standard wait-set
		// insert below gates the receiver correctly and no wake is
		// needed. We only probe `GetParkedByNode` when `hasRun=false`:
		//
		//  1. The upstream is parked IN THIS FRAME (parked run pinned
		//     to senderFrameID). Its phase stays 'parked' indefinitely
		//     without a wake; the receiver's wait-set blocker would
		//     point at a never-draining run.
		//  2. The upstream is parked in an EARLIER frame (parked row
		//     pinned to some prior frame). `MarkStaleForCascade`'s
		//     NOT EXISTS guard would skip the new-frame insert because
		//     the parked row counts as in-flight; the receiver-side
		//     re-fetch would then return hasRun=false and we'd error
		//     out. The wake primitive transitions the run pending and
		//     `wakeParkedReceiverInTx` itself rebinds the run's
		//     frame_id to senderFrameID so the resolver finds it.
		//
		// Probe is `GetParkedByNode` (frame-agnostic) which catches
		// both cases without trusting the ListByInstance snapshot's
		// State field (snapshots can lag a concurrent park terminal).
		//
		//	@concept: parked-state
		//	@concept: cascade
		//	@concept: attribute
		if !hasRun {
			parked, err := args.Queue.GetParkedByNode(ctx, upstreamNode.ID)
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
				upstreamRunID, hasRun, err = args.Queue.GetInFlightRunForNode(
					ctx, tx, upstreamNode.ID, senderFrameID,
				)
				if err != nil {
					return fmt.Errorf("cascadeSubscribersStaleInTx: re-fetch in-flight upstream %s after parked-wake: %w",
						upstreamType, err)
				}
				if !hasRun {
					return fmt.Errorf("cascadeSubscribersStaleInTx: parked upstream %s lost in-flight row after wake",
						upstreamType)
				}
			}
		}

		if !hasRun {
			if err := stalemarkAndEnqueueInFrame(
				ctx, args, tx, &upstreamNode, senderFrameID,
			); err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: stale-mark upstream %s: %w",
					upstreamType, err)
			}
			upstreamRunID, hasRun, err = args.Queue.GetInFlightRunForNode(
				ctx, tx, upstreamNode.ID, senderFrameID,
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
