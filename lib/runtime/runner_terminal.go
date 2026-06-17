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
//                                 emit `terminal/success` signal,
//                                 cascade message-pass on dependents.
//   - Success{changed: false}  → as above; `terminal/success` payload
//                                 carries `changed: false`; no cascade
//                                 (receiver-side CEL gate).
//   - Error{error_class}        → policy chain (4-value action
//                                 vocabulary: pass | give_up | retry |
//                                 discard_claims_then_retry). All
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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
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
// @blessed-invariant: callback-determinism — Callback determinism) while the open-its-own-tx
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
// @blessed-invariant: callback-determinism — Callback determinism. The phase-check read +
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
	// @deliberate: Plan I2: record the terminal verdict by class + error_class.
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
		// @deliberate: Park does NOT end the dispatch — the parked run re-enters and
		// its eventual terminal flows back through this switch, where the
		// work_completed pairing below fires. Per
		// concept:terminal-resolution's kind table, Park retains claims
		// and the run row; the await-async-callback transient likewise
		// never reaches this switch (its callback's final terminal does).
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

// emitWorkCompleted appends the `work_completed` audit event that pairs
// the post-acquisition `work_started` append in
// runner_acquire.go::tryAcquire. Same identifying payload fields
// (supervisor_id, dispatch_id) plus the terminal kind, so durations and
// did-everything-finish audits are computable from the ledger by
// joining on dispatch_id. Best-effort in its own tx: the dispatch's
// state mutation has already committed, so a failed audit append must
// not abort the terminal — WARN-and-continue, mirroring the
// work_started append's loss-visibility discipline.
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
// @blessed-invariant: callback-determinism — Callback determinism — the phase-check read +
// terminal state mutation share one tx.
func runApplyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent,
	setup func(ctx context.Context, tx persistence.Tx) (skip bool, err error),
) error {
	// @deliberate: Per TD-collapse-named-event-to-tags the rimsky_node_events ledger
	// has retired; subscriber-visible discriminators ride as Tags on
	// the settling terminal verdict (concept:terminal-tag). The
	// pre-Pass-1 processNamedEvents step is gone.
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
	// @deliberate: defense in depth against canonicalizer + emit-node misuse — an
	// emit-node (EmitsMessage != "") with IsSubgraphEntryAbsorbed set
	// would short-circuit through applyTerminalCompleteSubgraphCaller
	// below and silently skip the EmitsMessage block. The template
	// validator (`template_validator.go::validateNodeKindCombination`)
	// rejects an emit-node from also declaring an executor or delegate,
	// which is the prerequisite for canonicalizer absorption, so this
	// combination should be unreachable. If the canonicalizer ever
	// starts producing it (a future feature or a bug), fail loud here
	// rather than no-op the emit silently.
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

	// @deliberate: E6 sub-graph caller routing. The canonicalizer flagged this node
	// with `IsSubgraphEntryAbsorbed: true` so the supervisor knows that
	// the executor that just terminated was the absorbed entry. On the
	// success branch the parent run stays `running` and the sub-graph's
	// non-entry internals dispatch as children of this run.
	if acq.NodeDef != nil && acq.NodeDef.IsSubgraphEntryAbsorbed {
		return applyTerminalCompleteSubgraphCaller(ctx, args, acq, merged, t, tx)
	}

	// @deliberate: exit-node carry-rule: when this run is a sub-graph exit, copy its
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
		// @deliberate: fall through to the standard release/cascade path below so
		// exit's own state transitions to `fresh` and the parent
		// aggregator picks up the child's terminal via
		// PropagateFromChildState — but skip the exit's own attribute
		// row write (handled by the isSubgraphExit guard around
		// upsertFinalAttributesTx).
	}

	// @deliberate: per-node quality-rule evaluation retired by the 2026-05-15
	// data-platform-extensions plan P1. The verifier-shape-checks /
	// verifier-http executors (Section I) replace inline quality rules;
	// failures surface as `executor_errored` with
	// `error_class: "verifier_failed"`.

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

	// @deliberate: compute the settling signal type-path. Post-2026-05-23 the
	// on_executor_complete handler's always_propagate / never_propagate
	// / by_changed resolves retired with concept:lifecycle-handler —
	// cascade-fire is purely subscriber-driven (signal-type-path match
	// + CEL `when:` predicate). The per-fire-on-changed selectivity
	// that always_propagate / never_propagate expressed is now
	// declared receiver-side via `when: payload.changed` on a
	// `terminal/success` subscription. Post-Pass 5 the run row carries
	// settling_signal_type instead of the retired last_outcome enum.
	successType := string(signalpkg.TypePath("terminal/success"))
	settlingSignalType := &successType

	// @blessed-invariant: callback-determinism — Callback determinism — phase-check read
	// and these primary state-mutation writes must share one tx; this work runs
	// inline in the caller's outer tx.
	if err := releaseLocksInTx(ctx, args, tx, acq, true, false); err != nil {
		return nil, err
	}
	// @deliberate: per spec §Sub-graphs / Writeback carry-rule for exit: the
	// exit's own attribute row stays empty because the exit is
	// internal to the subgraph and not externally addressable. The
	// parent run's row was already populated by
	// applyTerminalCompleteSubgraphExit above.
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
	// @deliberate: running → fresh under ReasonHandlerComplete (the pre-2026-05-23
	// on_executor_complete lifecycle-handler slot retired; this
	// transition is now driven directly by the terminal-handler).
	// Thread acq.RunScopeID so fan-out children's state-machine
	// update lands on the correct sibling row.
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
	// @deliberate: run-disposition signal: terminal/success — cascade AND
	// audit in the same tx via the single emit chokepoint
	// (signal_emit.go). Tags carry concept:terminal-tag's discriminator
	// (gate-2-validated against the emitter's declared_tags upstream in
	// readExecutorOutcome).
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
	// @deliberate: data signal attribute/<key>/changed (one per merged
	// attribute) cascades here at terminal so subscribers gate, sharing
	// the once-per-frame guard above so a receiver already seeded by
	// terminal/success is not re-seeded. NOT routed through
	// emitSignalInTx: its audit row is written on its own schedule
	// (post-commit closure below — keyed on the changed delta, not
	// every merged key), so the chokepoint would double-write.
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
	// @deliberate: Per TD-collapse-named-event-to-tags the per-event
	// cascade walk has retired — subscribers express tag interest via
	// CEL filters over `payload.tags` on the terminal/* signal, and
	// the terminal/* cascade emission above carries the verdict's tags
	// through.
	// @concept: wait-set
	// @deliberate: settled-state drain: the sender just reached `fresh`. Any
	// wait-set rows the sender was gating get removed in bulk so
	// downstream receivers can advance.
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, err
	}

	// @deliberate: post-commit work: signal-emit (canonical audit row), lineage emit,
	// fan-out recalculate, run-tree propagation. Each opens its own tx
	// (or runs further out-of-tx work like PropagateIfChildAfterTerminal
	// which walks the run-tree under its own transactions); they MUST
	// run after the outer tx commits to avoid nested-tx deadlock under
	// the SQLite single-conn pool.
	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		// @deliberate: the terminal/success cascade AND its canonical audit row landed
		// together in-tx above via emitSignalInTx — there is no separate
		// post-commit audit write for it here (that duplicate retired with
		// the single-emit-path refactor). This closure carries only the
		// work that genuinely must run after the outer tx commits.
		//
		// Per-key attribute/<key>/changed audit rows (Task 12). Emitted
		// best-effort post-commit, keyed on the changed delta (not every
		// merged key, which is what the in-tx cascade walks). Old-value
		// lookup is not available at this point; payload OldValue stays
		// nil per the spec's "optional, when known" note.
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
		// @deliberate: post-commit fan-out: route a RecalculateNode hint to every
		// subscriber of this sender's node-type. Under the subscriber-
		// driven cascade-fire model the sender-side `fresh_changed`
		// gate retired; the receiver's own subscriptions (with their
		// CEL when: predicates) determine whether the recalc actually
		// produces a dispatch.
		fanoutRecalculate(ctx, args, acq)
		// @deliberate: E8 emit leaf-run lineage record. Spec §Content lineage.
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
		// @deliberate: run-tree state propagation (E2): if this run is a child
		// (fan-out or sub-graph internal), aggregate up to the parent.
		// No-op on root runs.
		if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
			cascade.NodeStateFresh, settlingSignalType); err != nil {
			args.Logger.Warn("applyTerminalComplete: run-tree propagation failed",
				"run_id", dispatchID.String(), "error", err.Error())
		}
	}
	return post, nil
}

// cascadeSubscribersStaleInTx marks subscriber nodes stale + frame_id
// and inserts wait-set rows in the same tx as the sender's transition
// emit. Under the 2026-05-23 signal-taxonomy reshape, the cascade-fire
// gate is purely subscriber-driven: an edge fires iff its TypePattern
// matches the emitted signal's TypePath AND its compiled CEL when:
// predicate evaluates true against the signal payload. Sender-side
// `last_outcome` gates are gone; settled-color is informational.
//
// The walk is recursive over the subscription graph within the
// instance: each receiver R that is newly marked stale is itself an
// invalidation site, so the walk processes R's subscribers in turn
// (BFS over the inverse-edge map). A per-call visited set guards
// against subscription cycles. Receivers walk under their own self-
// emitted signal (a synthesized `terminal/*` shape) so deeper-level
// edges with CEL predicates over downstream payloads still get a
// chance to match.
//
// Receivers are resolved from the cached per-template subscription-edge
// inverse map. Every matching edge is processed in-tx, in-frame: the
// receiver is stale-marked and a wait-set row is inserted against the
// sender's frame. Cross-frame coupling is expressed by message-emitter
// nodes (concept:message-emitter-node), not by the cascade walker.
//
// The settled-state drain (drainWaitSetOnSettled) marks the rows
// (sets drained_at) when the sender reaches any settled state
// (fresh/failed/parked); drained rows stay queryable for the
// substitution-context builder.
//
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

// cascadeSubscribersStaleInTxWithVisited is the multi-signal variant
// shared across the per-signal loop at applyTerminalComplete. The
// `visited` set is shared across the loop's calls so receivers seeded
// by one signal don't get re-seeded (and re-dispatched) by a later
// signal in the same terminal's emission set.
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
	// @deliberate: resolve receiver node-types → node-IDs within the instance once.
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
	// @deliberate: detect non-main sender scope so the walker can refuse to
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
	// @deliberate: `visited` is retained for the upstream-refresh walk's cycle
	// guard. Under the 2026-05-23 signal-taxonomy reshape the BFS-
	// recursion over subscription edges is gone — each receiver's own
	// terminal fires its own cascadeSubscribersStaleInTx — but the
	// upstream-refresh walk still recurses synchronously and needs the
	// visited set to bound pathological soft+force-refresh topologies.
	visited := map[foundationshared.UUID]struct{}{senderID: {}}
	{
		for _, edge := range candidateEdges {
			if edge.WhenExpr != nil {
				// @deliberate: Eval surfaces CEL runtime errors as `(false, nil)`
				// with a slog warn — per the spec's safe-navigation
				// default. The error return is reserved for future
				// fatal-eval cases and stays unreachable today.
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

				// @deliberate: same-scope membership check. Non-main scopes
				// (sub-graph, fanout_partition) are closed contexts:
				// a receiver belongs to the sender's scope only if
				// it already has an in-flight row there. The
				// lazy-allocation discipline of AffirmNodeRunRow
				// only applies to main RunScopes; for non-main
				// scopes, allocating a new row for a cross-scope
				// receiver creates an orphan in the wrong scope
				// (which then gets stranded when the scope closes
				// during parent aggregation). The cross-scope
				// bridge in
				// state_propagation.PropagateIfChildAfterTerminal
				// handles the receiver via the parent's settlement
				// cascade.
				receiverRunScopeID := senderRunScopeID
				if !senderScopeIsMain {
					existingID, existingOK, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: probe receiver run %s: %w", r.ID, err)
					}
					if !existingOK {
						// @deliberate: cross-scope receiver — skip; the bridge
						// at the parent's terminal handles it.
						continue
					}
					_ = existingID
				}
				// @deliberate: once-per-frame affirm-guard. Two layers:
				//
				// (1) intra-terminal: `visitedReceivers` is shared
				// across the per-signal loop within one terminal's
				// applyTerminalComplete tx so multi-signal emissions
				// don't re-affirm the same receiver. We DO continue
				// inserting wait-set rows below — each matching
				// signal still gates the receiver on the sender's
				// run, so BuildAttributeDeps sees rows under the
				// expected topic_kind.
				//
				// (2) cross-terminal: when an upstream node further
				// up the chain already terminated and seeded this
				// receiver in this frame (in-flight pending row that
				// the receiver then drained and dispatched and
				// terminated), the receiver has no in-flight row
				// but has a terminated row in this frame.
				// Re-affirming would create a fresh pending run row
				// missing prior wait-set gates; HasRunForNodeInFrame
				// catches this. With no in-flight receiver row left
				// to gate, the skip-affirm branch below `continue`s
				// past the wait-set insert entirely — a settled
				// receiver is never re-gated on a late-settling
				// upstream in the same frame.
				//
				// @decision: wake-on-change-wait-set-only
				// @deliberate: wake_on_change: false skips the
				// affirm-and-mark-stale path. The wait-set insert
				// below still runs against an existing in-flight row
				// (if any) so the receiver's substitution context
				// picks up the sender's data when the receiver
				// eventually dispatches via some other edge. If no
				// in-flight row exists for the receiver, the
				// skipAffirm branch below `continue`s past the
				// wait-set insert (no receiver to gate).
				//
				// Ordering caveat: the wait-set row lands only when
				// the receiver already has an in-flight row in the
				// sender's RunScope at the time of the sender's
				// settle. If the sender settles before the receiver
				// has been pulled into the frame by any other edge,
				// no wait-set row is recorded for this (sender,
				// receiver) pair. When the receiver later wakes via
				// another subscription, its substitution ref for the
				// sender resolves to ErrMissingSource and the
				// existing fallback / lenient / optional routing
				// governs the dispatch outcome (see
				// decision:substitution-grammar-fallback-unchanged).
				// Authors who require deterministic carry-through
				// regardless of intra-frame ordering use
				// force_upstream_refresh: true on the receiving
				// subscription instead.
				//
				// Do NOT mark the receiver as visited when
				// wake_on_change: false — a later wake_on_change:
				// true edge in the same terminal must still be able
				// to consume the affirm-once slot and wake the
				// receiver. The visited set is the affirm-once
				// guard, not a "any matching edge" guard.
				skipAffirm := false
				if !edge.WakeOnChange {
					skipAffirm = true
				} else if _, seen := visitedReceivers[r.ID]; seen {
					skipAffirm = true
				} else {
					visitedReceivers[r.ID] = struct{}{}
					// @deliberate: skip the cross-terminal guard for self-
					// edges. A node subscribing to itself is the
					// canonical "drain my own queue" idiom; the
					// HasRunForNodeInFrame check would always match
					// (the sender IS the receiver, with a row in this
					// frame) and incorrectly suppress the
					// self-re-fire.
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
				// @blessed-invariant: affirm-node-run-row — AffirmNodeRunRow
				// no-return-value-dependency.
				// @deliberate: affirm-then-read — under RunScope-first, the
				// cascade walker is the lazy-allocation primitive
				// for the receiver's in-flight row. AffirmNodeRunRow
				// INSERTs a pending stale row keyed on
				// (receiver_node_id, sender_run_scope_id) when none
				// exists; no-op if one already does. The subsequent
				// GetInFlightRunForNode read returns the row id
				// under the same tx. When `skipAffirm` is true
				// (already affirmed this terminal or an upstream
				// cascade in this frame, or `wake_on_change: false`),
				// we read the existing row's id rather than creating
				// a fresh one. The wait-set insert below still runs
				// so the receiver accumulates a row per matching
				// signal, populating BuildAttributeDeps for
				// attribute-topic edges.
				var receiverRunID foundationshared.UUID
				if skipAffirm {
					existingID, hasInFlight, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver run (skip-affirm) %s: %w", r.ID, err)
					}
					if !hasInFlight {
						// @deliberate: receiver settled this frame and no
						// in-flight row to gate on. Skip the
						// wait-set insert — there's no receiver to
						// gate.
						continue
					}
					receiverRunID = existingID
				} else {
					if err := args.Persist.Nodes().AffirmNodeRunRow(ctx, r.ID, receiverRunScopeID, senderFrameID, tx); err != nil {
						// @deliberate: defensive — a closed RunScope means the
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
					resolvedID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverRunScopeID)
					if err != nil {
						return fmt.Errorf("cascadeSubscribersStaleInTx: resolve receiver run %s: %w", r.ID, err)
					}
					if !ok {
						// @deliberate: race-with-terminal — the receiver's row
						// just terminated between affirm and read.
						// Safe to skip; its terminal handler will
						// drive its own cascade walk.
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
				// @deliberate: upstream-refresh pull — for each receiver-
				// declared `force_upstream_refresh: true` subscription,
				// ensure the upstream has an in-flight run in this frame
				// and a wait-set blocker on the receiver. The outer
				// walker's `visited` set is threaded down so the
				// upstream-refresh walk skips upstreams already covered
				// by the subscription walk — pathological mixed
				// soft+refresh topologies stay bounded. The upstream
				// lives in the same RunScope as the receiver (upstream-
				// refresh is intra-scope; cross-scope refresh is not
				// expressible). No deeper recursion here — under the
				// 2026-05-23 signal-taxonomy reshape, each receiver's
				// own terminal fires its own cascadeSubscribersStaleInTx
				// with its real signal, gating downstream subscribers
				// one signal at a time.
				if err := pullForceRefreshUpstreams(ctx, args, tx, r, byType, receiverRunID, receiverRunScopeID, senderFrameID, inst.TemplateHash, visited); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// pullForceRefreshUpstreams consults the per-template upstream-refresh
// edge map (subscribes: entries with `force_upstream_refresh: true`)
// for receiver `r` and, for each declared upstream X, ensures X has an
// in-flight run in this frame and a wait-set blocker installed on the
// receiver. When X has no current-frame run, the helper proactively
// stale-marks + cascade-walks X within the same tx via
// `stalemarkAndEnqueueInFrame`. All work happens inline so the call
// stays inside the caller's outer tx.
//
// The `visited` set is the outer BFS's cycle-guard (in
// `cascadeSubscribersStaleInTx`). Upstreams already visited by that BFS
// are skipped to bound work in pathological mixed soft+force-refresh
// topologies. Upstreams newly pulled by this helper are added to
// `visited` so the outer BFS sees them as already-processed.
//
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
		// @constraint: the template validator rejects templates with
		// hard-edge upstream types that have no instantiated node, so
		// reaching this branch means a stale spec; skip defensively
		// rather than fault.
		if len(upstreamNodes) == 0 {
			continue
		}
		// @constraint: one node per type per instance is a template
		// invariant; the [0] index is total under that invariant.
		upstreamNode := upstreamNodes[0]

		// @deliberate: outer-BFS visited-set check — if the subscription BFS already
		// processed this upstream, skip the upstream-refresh pull. The
		// wait-set row that the outer walk inserted (or skipped) is
		// already the gate for this frame; redoing the wake / stale-mark
		// would duplicate work and could surface as repeated audit
		// events.
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
			// @deliberate: `wakeParkedReceiverInTx` rebinds the run's frame_id
			// internally — no separate RebindRunFrameInTx call here.
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
			// @deliberate: defensive — a closed RunScope means the upstream's scope
			// rendezvous has fired. Upstream-refresh upstreams in closed
			// scopes cannot be reactivated — skip; the receiver's
			// wait-set is not populated for this upstream, and the
			// receiver re-evaluates substitutions when it next dispatches.
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
			// @deliberate: pass the just-affirmed upstreamRunScopeID through —
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

		// @deliberate: mark this upstream visited so the outer BFS (and a subsequent
		// upstream-refresh pull during the same walk) doesn't re-process
		// it.
		visited[upstreamNode.ID] = struct{}{}

		// @deliberate: insert wait-set blocker for the receiver on this upstream's run.
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
		if err != nil || edges == nil {
			return err
		}
		// @deliberate: fanout-recalc is a post-commit hint — it routes a
		// RecalculateNode event to every receiver subscribed to this
		// sender's node-type (without per-edge CEL filtering, since
		// recalculation re-evaluates from drained wait-set state).
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

// orEmptyMap returns m, or an empty map if m is nil. Used by signal
// payload construction sites where the schema requires a present (but
// possibly empty) map.
func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// waitSetTopicKindFor maps a signal.TypePath to the
// rimsky_wait_set.topic_kind discriminator. The DB CHECK admits the
// three canonical signal taxonomy values plus a 'state' fallback;
// the mapping is a faithful projection of the signal top-level kind,
// with no two distinct signal classes collapsed onto a shared
// bucket.
//
//   - terminal/*              → "terminal"
//   - transient/*             → "transient"
//   - attribute/<key>/changed → "attribute"
//   - empty/unrecognized      → "state" (fallback only)
//
// "state" survives solely as the empty/unrecognized fallback (and
// for back-compat with any legacy 'state' rows / conformance
// fixtures); the canonical kinds no longer fold onto it. The mapping
// is a runtime detail for the wait-set ledger only; the audit-log
// `kind` field stays the full canonical signal path.
//
// @deliberate: the pre-2026-06-14 'message' bucket retired alongside
// the signal taxonomy's `message/*` kind, and the pre-Pass-1 'event'
// bucket retired alongside `event/<name>` (TD-collapse-named-event-
// to-tags): a settling terminal's `payload.tags` field carries the
// observable discriminator that used to ride as a NamedEvent. Both
// strings are now rejected by the migration-013 CHECK on
// rimsky_wait_set.topic_kind.
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
