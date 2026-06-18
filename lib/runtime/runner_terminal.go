// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// @blessed-invariant: callback-determinism
type postCommitFn func(ctx context.Context)

// @blessed-invariant: callback-determinism
func applyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	metricsOf(args).IncTerminal(string(terminalClassFor(t.Kind)), t.ErrorClass)
	var pc postCommitFn
	var err error
	switch t.Kind {
	case terminalKindComplete:
		pc, err = applyTerminalComplete(ctx, args, acq, resolvedAttrs, schema, t, tx)
	case terminalKindErrored:
		pc, err = applyTerminalError(ctx, args, acq, resolvedAttrs, t.ErrorClass, t.Payload, t.Tags, t.AttributesDel, t.Scratch, tx)
	case terminalKindInfra:
		pc, err = applyTerminalInfraError(ctx, args, acq, t.ErrorClass, t.Payload, t.Scratch, tx)
	case terminalKindPark:
		return applyTerminalPark(ctx, args, acq, resolvedAttrs, t, tx)
	default:
		return nil, fmt.Errorf("applyTerminal: unhandled terminal kind %v", t.Kind)
	}
	if err != nil {
		return nil, err
	}
	// @story: work-completed-emitted
	// @deliberate: pair the post-acquisition `work_started` append
	// (runner_acquire.go::tryAcquire) with a `work_completed` append on
	// every terminal kind that ends the dispatch (Complete / Errored /
	// Infra — Errored covers all four policy dispositions: a retry ends
	// THIS dispatch and the re-enqueued successor emits its own
	// work_started, so per-dispatch pairing holds). Wrapped around the
	// handler's postCommit so the append runs after the outer
	// state-mutation tx commits, mirroring work_started's best-effort
	// audit-tx placement.
	inner := pc
	kind := t.Kind
	return func(ctx context.Context) {
		if inner != nil {
			inner(ctx)
		}
		emitWorkCompleted(ctx, args, acq, kind)
	}, nil
}

func emitWorkCompleted(ctx context.Context, args RunArgs, acq *acquisition, kind terminalKind) {
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: events.KindWorkCompleted(), Payload: map[string]any{
				"supervisor_id": args.SupervisorID,
				"dispatch_id":   acq.DispatchID.String(),
				"terminal_kind": string(terminalClassFor(kind)),
			},
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("emitWorkCompleted: work_completed event append failed; pairing event lost",
			"node_id", acq.NodeID.String(),
			"dispatch_id", acq.DispatchID.String(),
			"terminal_kind", string(terminalClassFor(kind)),
			"error", err.Error())
	}
}

// @blessed-invariant: callback-determinism
func runApplyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent,
	setup func(ctx context.Context, tx persistence.Tx) (skip bool, err error),
) error {
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

// @concept: signal
//
// Writes the cascade-firing gate enum on every terminal. The historical
// last_outcome / transition_reason surfaces were collapsed into the
// unified signal type-path taxonomy (see concept:signal).
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
//	@concept: terminal-tag
//
// @blessed-invariant: terminal-atomic-commit — the settling verdict
// (run-state mutation), `attributes_delta` writeback, and `tags`
// persistence all ride the caller-provided tx and commit together.
// A crash between the verdict and either side-effect would corrupt
// the cascade — subscribers would fire on a verdict whose tags
// hadn't landed, or carry-forward attributes would diverge from the
// dispatch they originated in. The tx is the unit of recovery here;
// none of these writes are deferred to a separate Persist.Transaction.
func applyTerminalComplete(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	if acq.NodeDef != nil && acq.NodeDef.EmitsMessage != "" && acq.NodeDef.IsSubgraphEntryAbsorbed {
		panic(fmt.Sprintf("applyTerminalComplete: emit-node %q has IsSubgraphEntryAbsorbed=true (canonicalizer-on-emit-node bug; the EmitsMessage block would never fire)", acq.NodeDef.Type))
	}
	merged := mergeAttributesDelta(resolvedAttrs, t.AttributesDel)
	if t.Changed && len(t.AttributesDel) > 0 && schema != nil {
		if err := attributes.Validate(schema, merged, attributes.PhaseCommit); err != nil {
			if appendErr := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: events.KindAttributesSchemaFailed(),
				Payload: map[string]any{
					"errors": []map[string]any{{"message": err.Error()}},
				},
			}, tx); appendErr != nil && args.Logger != nil {
				args.Logger.Warn("runner_terminal: append attributes_schema_failed event failed",
					"node_id", acq.NodeID.String(),
					"error", appendErr.Error())
			}
			// @concept: executor
			// @deliberate: route through the scratch-aware policy entry so the
			// executor's terminal-attached scratch lands on the dispatch
			// row BEFORE the retry branch reads it for carry-forward into
			// the successor's InitialScratch* enqueue. Schema-validation
			// rejection of a Success terminal is a recovery class (the
			// dispatch is retried with policy intervention); using the
			// non-scratch entry here would drop the executor's scratch on
			// every reject, violating STORY-opaque-executor-scratch's
			// round-trip contract.
			return applyErrorPolicyWithScratch(ctx, args, acq, "attributes_schema_failed",
				map[string]any{"error": err.Error()}, t.Tags, t.Scratch, tx)
		}
	}

	if acq.NodeDef != nil && acq.NodeDef.IsSubgraphEntryAbsorbed {
		return applyTerminalCompleteSubgraphCaller(ctx, args, acq, merged, t, tx)
	}

	isSubgraphExit := isSubgraphExitNode(acq)
	if isSubgraphExit {
		if err := applyTerminalCompleteSubgraphExit(ctx, args, acq, merged, tx); err != nil {
			return nil, err
		}
	}

	// @concept: message-emitter-node
	// @deliberate: message-emitter node-kind. Construct the envelope from
	// the resolved attribute set and insert into the message ledger inside
	// THIS tx. Two load-bearing properties:
	//
	//   - Envelope insert is atomic with the sender's terminal-resolution
	//     tx. The insert goes through the caller's outer `tx` — the same
	//     one releaseLocksInTx / upsertFinalAttributesTx / UpdateState
	//     below also use. A subsequent error (or a forced tx-rollback
	//     test) rolls the envelope back atomically. There is no separate
	//     tx, no post-commit closure, no async dispatch.
	//
	//   - Idempotency on cascade-emit is deterministic on
	//     `(node_id, frame_id)`. `emitCascadeMessageInTx` derives the
	//     Idempotency-Key as `cascade-emit:<node_id>:<frame_id>`; the
	//     MessageIdempotencies table dedups so any retry against the same
	//     (node, frame) pair produces exactly one envelope row. Keying on
	//     the dispatch_id (the run-row's id) was unsafe: every supervisor-
	//     side hard-failure re-enqueue mints a fresh run id, so the dedup
	//     row would not collide and the retry would duplicate envelopes.
	//
	// `merged` is the source-of-truth attribute bag because it folds in
	// any `t.AttributesDel` carried in the terminal verdict — under the
	// emits_message path that delta is normally empty (the dispatch stub
	// does not return a delta), but the merged path is the canonical
	// attribute view at commit, and using it here keeps the emit shape
	// consistent with what the standard terminal-resolution code writes
	// into the attribute ledger.
	if acq.NodeDef != nil && acq.NodeDef.EmitsMessage != "" {
		if _, _, err := emitCascadeMessageInTx(ctx, args.Persist, tx,
			acq.InstanceID, acq.NodeID, acq.FrameID, acq.NodeDef.EmitsMessage, merged); err != nil {
			return nil, fmt.Errorf("applyTerminalComplete: emit cascade message: %w", err)
		}
	}

	successType := string(signalpkg.TypePath("terminal/success"))
	settlingSignalType := &successType

	// @blessed-invariant: callback-determinism
	if err := releaseLocksInTx(ctx, args, tx, acq, true, false); err != nil {
		return nil, err
	}
	if !isSubgraphExit {
		if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
			return nil, fmt.Errorf("applyTerminalComplete: upsert attributes: %w", err)
		}
	}
	// @concept: executor
	// @deliberate: persist executor-attached scratch onto the dispatch row inside
	// the terminal tx. Inline vs. spilled-handle picked via the same
	// threshold as the parked-payload site. Empty scratch short-
	// circuits before the UPDATE — see applyTerminalScratchInTx for
	// the rationale; the row's existing scratch (none, a mid-dispatch
	// callback write, or recovery-copied prior bytes) is preserved.
	// Per STORY-opaque-executor-scratch the scratch round-trips across
	// the executor's Success terminal under any of the three recovery
	// dispositions that stamp prior_dispatch_id.
	//
	// The sub-graph exit carve-out lives inside applyTerminalScratchInTx
	// (centralized so Success / Error / Infra terminals stay in sync on
	// the "exit's row stays empty" rule).
	// @concept: executor
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, t.Scratch); err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: %w", err)
	}
	if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{}, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: clear error state: %w", err)
	}
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
		cascade.NodeStateFresh, cascade.ReasonHandlerComplete, settlingSignalType, tx); err != nil {
		return nil, err
	}
	// @concept: node-run
	// @concept: cascade
	// @deliberate: flip the just-completed run row to a terminal phase BEFORE the
	// cascade walk fires. Without this the row stays in
	// phase='active' until the outer supervisor.go / callback.go
	// post-apply `Queue.Complete` call, which means
	// `MarkStaleForCascade`'s `NOT EXISTS (phase IN
	// pending/active/held/parked)` guard rejects self-edges during
	// the walk — `frame: in` self-subscriptions can't insert their
	// new pending run because runOld is still active. Mirrors the
	// in-tx phase flip every other terminal already does
	// (`applyErrorPolicy` / `applyTerminalInfraError` at
	// runner_error_policy.go:217/239/283; `applyTerminalPark` via
	// `ParkActiveInTx`). Outer `Queue.Complete` calls in
	// `supervisor.go` and `callback.go` become idempotent no-ops on
	// every known happy path (their WHERE clauses filter on active
	// phase set); kept as belt-and-suspenders cleanup.
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalComplete: remove for node: %w", err)
	}
	// @concept: cascade
	// @concept: signal
	// @concept: wait-set
	// @deliberate: cascade walk on settlement. Under the 2026-05-23 signal-taxonomy
	// reshape, the cascade-fire gate is purely subscriber-driven: a
	// subscription edge fires iff its TypePattern matches the emitted
	// signal AND its CEL when: predicate evaluates true. The
	// pre-reshape `last_outcome == fresh_changed` sender-side gate
	// retired with this spec; settled-color is informational, not a
	// fire condition. Subscribers that want to react only to
	// `payload.changed` set `when: payload.changed` on their
	// terminal/success subscription.
	//
	// This walk is complementary to the cascade-on-invalidation
	// walks at `walkCascadeForInvalidatedNode` (heartbeat-loss
	// recovery, parked-resume wake) / applyResolvedAction / etc.: the
	// invalidation-side walks gate receivers across multiple in-flight
	// senders (multi-invalidator); the settlement-side walk gates the
	// initial-instance case + the deeper-level pessimistic seed.
	//
	// @constraint: consolidate every signal this terminal emits — the
	// success envelope and one attribute/<key>/changed per merged
	// attribute — into a single cascade walk. One walk visits each
	// (receiver, frame) at most once across the full signal set,
	// preserving the once-per-frame dispatch invariant. Per
	// concept:signal each signal matches the subscriber edge map
	// independently; a shared visited set across the per-signal loop
	// ensures receivers seeded by an earlier signal don't get re-seeded
	// by a later one. Per TD-collapse-named-event-to-tags the historic
	// event/<name> signal is gone — its observable discriminator now
	// rides as payload.tags on the success envelope below.
	visited := map[foundationshared.UUID]struct{}{}
	successSig := signalpkg.Signal{
		Type: "terminal/success",
		Payload: map[string]any{
			"changed":          t.Changed,
			"attributes_delta": orEmptyMap(t.AttributesDel),
			"change_summary":   t.ChangeSummary,
			"tags":             t.Tags,
		},
	}
	if err := emitSignalInTx(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, successSig, visited); err != nil {
		return nil, err
	}
	for key, value := range merged {
		attrSig := signalpkg.Signal{
			Type: signalpkg.TypePath(fmt.Sprintf("attribute/%s/changed", key)),
			Payload: map[string]any{
				"key":   key,
				"value": value,
			},
		}
		if err := cascadeSubscribersStaleInTxWithVisited(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, attrSig, visited); err != nil {
			return nil, err
		}
	}
	// @concept: wait-set
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, err
	}

	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		if len(t.AttributesDel) > 0 {
			if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				for key, value := range t.AttributesDel {
					attrSig := signalpkg.Signal{
						Type: signalpkg.TypePath(fmt.Sprintf("attribute/%s/changed", key)),
						Payload: map[string]any{
							"key":   key,
							"value": value,
						},
					}
					if err := signalaudit.EmitSignal(ctx, args.Persist.Events(),
						acq.InstanceID, acq.NodeID, attrSig, args.Clock.Now(), tx); err != nil {
						return err
					}
				}
				return nil
			}); err != nil && args.Logger != nil {
				args.Logger.Warn("runner_terminal: append attribute signal rows failed",
					"node_id", acq.NodeID.String(),
					"error", err.Error())
			}
		}
		fanoutRecalculate(ctx, args, acq)
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			RunID:              dispatchID,
			NodeID:             acq.NodeID,
			State:              string(cascade.NodeStateFresh),
			SettlingSignalType: *settlingSignalType,
			Changed:            t.Changed,
			TerminalKind:       "complete",
			NodeAlias:          acq.NodeType,
			ExecutorName:       acq.Executor,
			TemplateHash:       acq.TemplateHash,
			Params:             acq.InstanceParams,
			AttributesMerged:   acq.MergedAttributes,
			HeldClaims:         HeldClaimsForLineage(acq),
			ParentRunID:        scope.ParentRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
		if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
			cascade.NodeStateFresh, settlingSignalType); err != nil {
			args.Logger.Warn("applyTerminalComplete: run-tree propagation failed",
				"run_id", dispatchID.String(), "error", err.Error())
		}
	}
	return post, nil
}

//	@concept: cascade
//	@concept: signal
//	@concept: wait-set
func cascadeSubscribersStaleInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
) error {
	return cascadeSubscribersStaleInTxWithVisited(ctx, args, tx,
		senderID, senderNodeType, senderRunID, instanceID, senderFrameID, sig,
		map[foundationshared.UUID]struct{}{})
}

func cascadeSubscribersStaleInTxWithVisited(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
	visitedReceivers map[foundationshared.UUID]struct{},
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
	if edges == nil {
		return nil
	}
	instNodes, err := args.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: list instance nodes: %w", err)
	}
	byType := make(map[string][]persistence.NodeRow, len(instNodes))
	for _, n := range instNodes {
		byType[n.NodeType] = append(byType[n.NodeType], n)
	}
	// @concept: run-scope
	// @deliberate: resolve the sender's RunScope: same-scope cascade is the common
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
	// @deliberate: non-main scopes (fanout_partition, sub-graph) are CLOSED contexts:
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
	senderRun, err := args.Persist.RunTree().GetByID(ctx, tx, senderRunID)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: load sender run: %w", err)
	}
	if senderRun == nil {
		return nil
	}
	senderRunScopeID := senderRun.RunScopeID
	senderRunScope, err := args.Persist.RunScopes().GetByID(ctx, tx, senderRunScopeID)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: load sender run scope: %w", err)
	}
	if senderRunScope == nil {
		return nil
	}
	senderScopeIsMain := senderRunScope.ParentRunID == nil
	// @concept: signal
	// @deliberate: subscriber-driven gate: an edge fires iff its
	// TypePattern matches the emitted signal AND its CEL when:
	// predicate evaluates true. No deeper BFS — each receiver's own
	// terminal eventually fires its own cascade walk with the
	// receiver's real signal, propagating gates one level at a time.
	candidateEdges := edges.Match(senderNodeType, sig.Type)
	if len(candidateEdges) == 0 {
		return nil
	}
	type walkItem struct {
		nodeID   foundationshared.UUID
		nodeType string
		runID    foundationshared.UUID
	}
	cur := walkItem{nodeID: senderID, nodeType: senderNodeType, runID: senderRunID}
	visited := map[foundationshared.UUID]struct{}{senderID: {}}
	{
		for _, edge := range candidateEdges {
			if edge.WhenExpr != nil {
				ok, _ := edge.WhenExpr.Eval(sig)
				if !ok {
					continue
				}
			}
			receivers := byType[edge.ReceiverNodeType]
			for _, r := range receivers {
				// @concept: cascade
				// @concept: node-subscription
				// @concept: parked-state
				// @concept: run-scope
				// @deliberate: the cascade walker has one path under the
				// message-schema-layer redesign: in-tx, in-frame. Every
				// matching subscription stale-marks the receiver inside
				// the sender's frame in the sender's settlement tx.
				// Cross-frame coupling is expressed by message-emitter
				// nodes (concept:message-emitter-node), not by a
				// per-subscription `frame:` modifier.
				//
				// Self-edges are first-class "drain my own queue".
				// Insert-then-drain-in-same-tx makes the in-frame
				// self-edge safe: the wait-set row inserted below
				// (gating the new pending run on this commit's run)
				// gets cleared by drainWaitSetOnSettled at the end of
				// applyTerminalComplete in the same tx, before the
				// supervisor sees it. MarkStaleForCascade does NOT
				// touch rimsky_nodes.state (only inserts a new run
				// row + re-stamps frame_id), so the just-committed
				// state=fresh, settling_signal_type=terminal/success
				// survives intact for downstream consumers. The
				// visited set blocks indirect re-seeding.
				//
				// Parked receivers need their parked node-run row
				// resumed alongside the stale stamp; without that
				// the queue still carries phase='parked' and the
				// supervisor never picks the row up.

				receiverRunScopeID := senderRunScopeID
				if !senderScopeIsMain {
					existingID, existingOK, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: probe receiver run %s: %w", r.ID, err)
					}
					if !existingOK {
						continue
					}
					_ = existingID
				}
				// @decision: wake-on-change-wait-set-only
				skipAffirm := false
				if !edge.WakeOnChange {
					skipAffirm = true
				} else if _, seen := visitedReceivers[r.ID]; seen {
					skipAffirm = true
				} else {
					visitedReceivers[r.ID] = struct{}{}
					if r.ID != senderID {
						settled, err := args.Persist.Nodes().HasRunForNodeInFrame(ctx, r.ID, senderFrameID, tx)
						if err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: probe receiver frame %s: %w", r.ID, err)
						}
						if settled {
							skipAffirm = true
						}
					}
				}
				// @blessed-invariant: affirm-node-run-row
				var receiverRunID foundationshared.UUID
				if skipAffirm {
					existingID, hasInFlight, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver run (skip-affirm) %s: %w", r.ID, err)
					}
					if !hasInFlight {
						continue
					}
					receiverRunID = existingID
				} else {
					if err := args.Persist.Nodes().AffirmNodeRunRow(ctx, r.ID, receiverRunScopeID, senderFrameID, tx); err != nil {
						if errors.Is(err, persistence.ErrRunScopeClosed) {
							continue
						}
						return fmt.Errorf("cascadeSubscribersStaleInTx: affirm receiver run %s: %w", r.ID, err)
					}
					resolvedID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver run %s: %w", r.ID, err)
					}
					if !ok {
						continue
					}
					receiverRunID = resolvedID
					if r.State == cascade.NodeStateParked {
						if err := wakeParkedReceiverInTx(ctx, args, tx, r, senderFrameID); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: wake parked %s: %w", r.ID, err)
						}
					} else {
						if err := args.Persist.Nodes().MarkStaleForCascade(ctx, receiverRunID, senderFrameID, tx); err != nil {
							return fmt.Errorf("cascadeSubscribersStaleInTx: mark stale %s: %w", r.ID, err)
						}
					}
				}
				if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
					FrameID:           senderFrameID,
					ReceiverRunID:     receiverRunID,
					SenderRunID:       cur.runID,
					TopicKind:         waitSetTopicKindFor(edge.TypePattern),
					SubscriptionScope: edge.SubscriptionScope,
				}, tx); err != nil {
					return fmt.Errorf("cascadeSubscribersStaleInTx: wait-set insert: %w", err)
				}
				if err := pullForceRefreshUpstreams(ctx, args, tx, r, byType, receiverRunID, receiverRunScopeID, senderFrameID, inst.TemplateHash, visited); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

//	@concept: cascade
//	@concept: attribute
func pullForceRefreshUpstreams(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	receiver persistence.NodeRow,
	byType map[string][]persistence.NodeRow,
	receiverRunID foundationshared.UUID,
	targetRunScopeID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	templateHash string,
	visited map[foundationshared.UUID]struct{},
) error {
	refreshEdges, err := hardDepEdgesForTemplate(ctx, args, templateHash, tx)
	if err != nil {
		return fmt.Errorf("cascadeSubscribersStaleInTx: upstream-refresh edges: %w", err)
	}
	if len(refreshEdges) == 0 {
		return nil
	}
	if len(refreshEdges[receiver.NodeType]) == 0 {
		return nil
	}
	for _, upstreamType := range refreshEdges[receiver.NodeType] {
		upstreamNodes := byType[upstreamType]
		if len(upstreamNodes) == 0 {
			continue
		}
		upstreamNode := upstreamNodes[0]

		if _, seen := visited[upstreamNode.ID]; seen {
			continue
		}

		// @concept: parked-state
		// @concept: run-scope
		// @concept: cascade
		// @deliberate: parked-upstream handling (BEFORE AffirmNodeRunRow).
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
		upstreamRunScopeID := targetRunScopeID
		parked, err := args.Queue.GetParkedByNode(ctx, upstreamNode.ID, upstreamRunScopeID)
		if err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: get parked upstream %s: %w",
				upstreamType, err)
		}
		if parked != nil {
			if err := wakeParkedReceiverInTx(ctx, args, tx, upstreamNode, senderFrameID); err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: wake parked upstream-refresh upstream %s: %w",
					upstreamType, err)
			}
		}

		// @concept: cascade
		// @deliberate: settled-this-frame guard — with two or more upstream-refresh
		// upstreams settling independently in one frame, the later
		// settler's own cascade walk re-visits the receiver, and this
		// pull would otherwise re-affirm the EARLIER upstream — which
		// already settled this frame and so has no in-flight row. The
		// re-affirm creates a fresh pending run; that re-run settles,
		// walks back to the receiver, and re-affirms the OTHER settled
		// upstream: mutual re-seeding, the frame never terminates
		// (regression pin:
		// test/scenarios/multi_hard_dep_test.go). An upstream that
		// already has a run row in this frame but NO in-flight row is
		// settled-this-frame: in the common path its value is already in
		// the receiver's drained wait-set (inserted when it was first
		// pulled into the frame by `pullForceRefreshUpstreams`, or by its
		// own settle walk via the matching explicit `subscribes:` entry
		// when the receiver was already in-flight at that settle). Skip
		// in either case — there is nothing to gate on and nothing to
		// re-run. The wait-set row may be absent when the upstream
		// settled BEFORE the receiver entered the frame on a
		// `wake_on_change: false` edge — `BuildAttributeDeps` then
		// returns ErrMissingSource and the substitution grammar's
		// fallback / lenient / optional routing governs the dispatch
		// outcome (see decision:substitution-grammar-fallback-unchanged).
		// The in-flight probe comes first so a still-running (or just-
		// woken parked) upstream in this frame falls through to the
		// normal gate-insert path — the guard protects frame termination
		// without weakening the rendezvous.
		_, hasInFlightRun, err := args.Queue.GetInFlightRunForNode(
			ctx, tx, upstreamNode.ID, upstreamRunScopeID,
		)
		if err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: probe in-flight upstream-refresh upstream %s: %w",
				upstreamType, err)
		}
		if !hasInFlightRun {
			settledThisFrame, err := args.Persist.Nodes().HasRunForNodeInFrame(
				ctx, upstreamNode.ID, senderFrameID, tx,
			)
			if err != nil {
				return fmt.Errorf("cascadeSubscribersStaleInTx: probe settled upstream-refresh upstream %s: %w",
					upstreamType, err)
			}
			if settledThisFrame {
				visited[upstreamNode.ID] = struct{}{}
				continue
			}
		}

		// @concept: run-scope
		// @blessed-invariant: affirm-node-run-row — AffirmNodeRunRow no-return-value-dependency.
		// @deliberate: affirm-then-read — the upstream lives in the same RunScope as
		// the receiver (upstream-refresh is intra-scope by construction
		// — cross-scope upstream-refresh is not expressible).
		// AffirmNodeRunRow INSERTs a pending row keyed on
		// (upstream_node_id, target_run_scope_id) when none exists.
		if err := args.Persist.Nodes().AffirmNodeRunRow(ctx, upstreamNode.ID, upstreamRunScopeID, senderFrameID, tx); err != nil {
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

		visited[upstreamNode.ID] = struct{}{}

		if err := args.Persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           senderFrameID,
			ReceiverRunID:     receiverRunID,
			SenderRunID:       upstreamRunID,
			TopicKind:         "attribute",
			SubscriptionScope: "direct",
		}, tx); err != nil {
			return fmt.Errorf("cascadeSubscribersStaleInTx: insert upstream-refresh wait-set: %w", err)
		}
	}
	return nil
}

//	@concept: wait-set
func drainWaitSetOnSettled(
	ctx context.Context, args RunArgs, tx persistence.Tx, frameID, senderRunID foundationshared.UUID,
) error {
	return args.Persist.WaitSet().MarkDrainedBySender(ctx, frameID, senderRunID, tx)
}

func fanoutRecalculate(ctx context.Context, args RunArgs, acq *acquisition) {
	var receivers []persistence.NodeRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := args.Persist.Instances().Get(ctx, acq.InstanceID, tx)
		if err != nil || inst == nil {
			return err
		}
		edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
		if err != nil || edges == nil {
			return err
		}
		receiverTypeList := edges.ReceiverNodeTypesForSender(acq.NodeType)
		if len(receiverTypeList) == 0 {
			return nil
		}
		receiverTypes := make(map[string]struct{}, len(receiverTypeList))
		for _, t := range receiverTypeList {
			receiverTypes[t] = struct{}{}
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

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func waitSetTopicKindFor(pattern signalpkg.TypePath) string {
	switch pattern.TopLevel() {
	case signalpkg.KindTerminal:
		return "terminal"
	case signalpkg.KindTransient:
		return "transient"
	case signalpkg.KindAttribute:
		return "attribute"
	default:
		return "state"
	}
}
