// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Spec §4.2 (on_error) + §7.3 (policy chain). Consults the node's
// error_types policy chain, evaluates an occurrence, persists the resolved
// EvaluatorState, emits the canonical `terminal/error/<class>` (or
// `transient/retry/<n>/<class>`) signal via signalaudit.EmitSignal, and
// applies the resolved action (pass | give_up | retry |
// discard_claims_then_retry).
//
// Routes every error class through one path. The attribute-pipeline
// classes (`template_resolution_failed`, `template_validation_failed`,
// `executor_schema_unavailable`, and `attributes_schema_failed`) have no
// special-case handlers here: when a template declares a policy override
// for them it flows through `lookupPolicy` → `node.Evaluate`; absent an
// override, `node.Evaluate` (policy == nil) defaults to
// give_up("unknown_error_class"), which is exactly the `[ {give_up} ]`
// default §10.4 calls for. Templates may override any class via the
// standard `error_types` block.
//
// Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Error handling" the three-class split lets operators set different
// policies for strict-directive misses (retry-after-cascade) vs.
// validation failures (give-up — the template's broken) vs. schema-
// visibility issues (retry-after-handshake-completes).
//
// Concurrency-tag plumbing is gone — the redesign expresses concurrency
// through named locks declared on the node and configured in
// `named_locks:`; the runner builds and acquires those during dispatch
// (§7.3) and the queue row carries `required_stores` rather than tags.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	signalaudit "github.com/rimsky-ai/rimsky-core/foundation/signal/audit"
	"github.com/rimsky-ai/rimsky-core/graph/node"
)

// OnErrorArgs is the payload for OnError.
type OnErrorArgs struct {
	// Persist is the unified persistence.Tables handle (rimsky_* tables).
	Persist persistence.Tables
	// Queue is the dispatch-queue accessor.
	Queue      persistence.Queue
	Clock      shared.Clock
	Logger     shared.Logger
	NodeID     shared.UUID
	InstanceID shared.UUID
	// RunScopeID identifies the RunScope this error pertains to.
	// Required: every in-flight run belongs to some RunScope (main /
	// subgraph / fanout_partition); the (node_id, run_scope_id) pair
	// resolves the specific in-flight rimsky_node_runs row. Per
	// concept:run-scope.
	//
	// @concept: run-scope
	RunScopeID shared.UUID
	// SupervisorID identifies the supervisor handling the current run. When
	// non-empty, queue deletes (RemoveForNode) are claimant-guarded so a stale
	// sweep from a different supervisor can't accidentally drop our row.
	SupervisorID string
	ErrorClass   string
	Payload      map[string]any
	// Metrics is the dispatch/terminal/claim instrumentation hook
	// (plan I3). Retained for symmetry with the wider RunArgs/
	// MetricsHook plumbing; the historical policy_invalidate fan-out
	// it covered retired alongside the `invalidate` ErrorPolicy action.
	// Nil → no-op.
	Metrics MetricsHook
}

// OnError evaluates the node's policy for ErrorClass and applies the
// resolved action. See spec §4.2 (on_error), §7.3 (policy chain).
//
// retry     → persist advanced EvaluatorState, transition running→stale
//
//	(reason policy_retry), re-enqueue dispatch with future enqueued_at
//	reflecting the backoff delay.
//
// pass      → settle the run as fresh (reason acquire_pass when
//
//	transitioning from stale; reason handler_pass when transitioning
//	from running). The chain advances so a subsequent same-class error
//	doesn't pass again.
//
// give_up   → transition → failed (reason policy_give_up). This is also
//
//	the §10.4 / §9.4 default for `template_resolution_failed`,
//	`template_validation_failed`, `executor_schema_unavailable`, and
//	`attributes_schema_failed` when the template declares no override
//	(via the policy == nil branch of node.Evaluate).
//
// @blessed-invariant: State-machine writes for a single run must be
// tx-atomic. Any operation that reads a run's current state to
// decide what state to write must perform the read and the write
// in the same transaction. Per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
func OnError(ctx context.Context, args OnErrorArgs) error {
	sb, log := args.Persist, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// Outer read fetches only the immutable fields used outside the
	// mutating tx — NodeType / InstanceID feed lookupPolicy and
	// requiredStoresForNode; Executor is needed for the retry-branch
	// enqueue. The mutable fields (State, FrameID, InFlightRunID) are
	// re-read INSIDE each mutating tx below so the state-machine
	// tx-atomicity invariant holds even if a concurrent sweep rotated
	// the in-flight run between the outer read and the inner mutation.
	var nd *persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		n, err := sb.Nodes().Get(ctx, args.NodeID, tx)
		nd = n
		return err
	}); err != nil {
		return err
	}
	if nd == nil {
		return nil
	}

	// Resolve the per-error-class policy from the template spec.
	policy, err := lookupPolicy(ctx, sb, nd, args.ErrorClass)
	if err != nil {
		return err
	}

	// Compute required stores BEFORE entering any outer state-mutation tx.
	// requiredStoresForNode internally opens its own sb.Transaction —
	// if called inside the outer tx, the nested Transaction blocks
	// forever on the SQLite single-conn pool (MaxOpenConns=1) and ties
	// up two pool connections concurrently under postgres. Capture the
	// result here; pass into the closure via the captured variable.
	requiredStores := requiredStoresForNode(ctx, sb, nd)

	// Bundle the EvaluatorState read with the policy-state advance so
	// they land in a single tx (state-machine tx atomicity invariant):
	// the row's ActionIndex/RetryCounter/CurrentErrorClass that feed
	// node.Evaluate are re-read here from the same tx that writes the
	// advanced state, closing the race window where another writer
	// could have advanced the row between read and write.
	//
	// The canonical signal emission does NOT happen here. The signal
	// describes the run-row's terminal disposition (retry / give-up /
	// pass), which is committed in the per-branch tx below; emitting
	// the signal in this tx would let a tx#1-commit / tx#2-fail window
	// land a `terminal/error/<class>` audit row on `rimsky_events`
	// while the rimsky_nodes row still reads `running`, contradicting
	// the signal. The per-branch tx below co-commits the signal with
	// the matching state transition so subscribers never observe an
	// audit row whose state column contradicts the disposition. The
	// `runner_error_policy.go::applyErrorPolicy` path uses the same
	// pattern: signal-emit lives in the outer state-mutation tx, not
	// in a post-commit closure. Per
	// `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`.
	var resolved node.ResolvedAction
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cur, err := sb.Nodes().Get(ctx, args.NodeID, tx)
		if err != nil {
			return err
		}
		if cur == nil {
			return nil
		}
		state := node.EvaluatorState{
			ActionIndex:       cur.ActionIndex,
			RetryCounter:      cur.RetryCounter,
			CurrentErrorClass: cur.CurrentErrorClass,
		}
		resolved = node.Evaluate(policy, state, args.ErrorClass, nil)
		return sb.Nodes().UpdateError(ctx, args.NodeID, resolved.NewState, tx)
	}); err != nil {
		return err
	}

	// Construct the canonical signal envelope via the shared
	// `errorPolicySignal` helper so OnError's emit path matches the
	// runtime's applyErrorPolicy path. `retriesSoFar` is best-effort
	// here (the OnError path doesn't carry the consecutive-retries
	// counter that applyErrorPolicy threads through buildResolution;
	// resolved.NewState.RetryCounter is the chain-position counter,
	// which is the closest available signal for the audit row's
	// `attempt` field). The envelope is constructed once and emitted
	// inside whichever per-branch tx commits the matching state
	// transition.
	resolutionSig := errorPolicySignal(args.ErrorClass, args.Payload, resolved.Kind,
		resolved.NewState.RetryCounter, resolved.DelayMs)

	switch resolved.Kind {
	case "retry", "discard_claims_then_retry":
		// Wrap remove + enqueue (and the optional running→stale
		// transition + cascade walk) in one tx so a partial commit
		// can't strand the node with no in-flight row and no
		// replacement. Mirrors `applyResolvedAction` in
		// `runner_error_policy.go`. Without this, a remove that
		// committed followed by an enqueue that failed would leave
		// the node with no in-flight row.
		return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			// Re-read mutable fields (State, FrameID, InFlightRunID)
			// INSIDE this tx so the read-then-write pair is atomic.
			// Using these from the outer read could race with a
			// concurrent sweep that rotated the in-flight dispatch
			// between the two reads.
			cur, err := sb.Nodes().Get(ctx, args.NodeID, tx)
			if err != nil {
				return err
			}
			if cur == nil {
				return nil
			}
			if cur.FrameID == nil {
				return fmt.Errorf("OnError retry: node %s has nil frame_id", args.NodeID)
			}
			if cur.State == cascade.NodeStateRunning {
				if err := sb.Nodes().UpdateState(ctx, args.NodeID, args.RunScopeID, cascade.NodeStateStale, cascade.ReasonPolicyRetry, nil, tx); err != nil {
					return err
				}
				// Pessimistic-invalidate: running → stale is this
				// sender's invalidation in this frame. Gate downstream
				// subscribers across the retry round-trip.
				//
				//	@concept: cascade
				//	@concept: wait-set
				if err := walkCascadeForInvalidatedNode(ctx, sb, args.Queue, tx, log, args.NodeID, cur.InstanceID, *cur.FrameID); err != nil {
					return err
				}
			}
			// Recovery-aware fields: the in-flight run row at this
			// point is the predecessor dispatch being retired by
			// RemoveForNodeInTx; capture its id BEFORE the remove so
			// the new dispatch's proto:executor.proto::ExecuteRequest.
			// prior_dispatch_id resolves to the run that errored.
			priorID := cur.InFlightRunID
			if err := args.Queue.RemoveForNodeInTx(ctx, args.NodeID, args.RunScopeID, args.SupervisorID, tx); err != nil {
				return err
			}
			if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
				NodeID:                   args.NodeID,
				ExecutorName:             cur.Executor,
				RequiredStores:           requiredStores,
				EnqueuedAt:               args.Clock.Now().Add(time.Duration(resolved.DelayMs) * time.Millisecond),
				FrameID:                  *cur.FrameID,
				RunScopeID:               args.RunScopeID,
				PriorDispatchID:          priorID,
				PriorDispatchDisposition: "retry_after_error",
			}, tx); err != nil {
				// Defensive: closed RunScope means the rendezvous has
				// fired before the retry could land. Walker discipline
				// per concept:run-scope: do not enqueue into a closed
				// scope; the policy chain's state advancement already
				// committed (give-up path will fire on the next error
				// occurrence if there is one).
				if errors.Is(err, persistence.ErrRunScopeClosed) {
					log.Warn("OnError retry: skip enqueue: run scope closed",
						"node_id", args.NodeID.String(),
						"run_scope_id", args.RunScopeID.String())
					return nil
				}
				return err
			}
			// Co-commit the canonical signal with the state transition
			// that produced it. The audit row on `rimsky_events` and the
			// rimsky_nodes state change land or roll back together —
			// subscribers never see a `transient/retry/*` (or
			// `terminal/error/*`) row while the run row still contradicts
			// the disposition.
			return signalaudit.EmitSignal(ctx, sb.Events(),
				args.InstanceID, args.NodeID, resolutionSig, args.Clock.Now(), tx)
		})

	case "give_up":
		// Bundle state write + queue remove (and the optional wait-set
		// drain) in one tx so a partial commit can't leave the run
		// failed with its dispatch row stranded. Mirrors the retry
		// branch's same-tx atomicity.
		return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			// Re-read mutable fields inside the same tx that writes
			// state (tx-atomicity invariant).
			cur, err := sb.Nodes().Get(ctx, args.NodeID, tx)
			if err != nil {
				return err
			}
			if cur == nil {
				return nil
			}
			giveUpSig := "terminal/error/" + args.ErrorClass
			if err := sb.Nodes().UpdateState(ctx, args.NodeID, args.RunScopeID, cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &giveUpSig, tx); err != nil {
				return err
			}
			// Settled-state drain on failed: any wait-set rows gating
			// receivers on this sender's run release. Post-stage-5 the
			// wait-set keys on sender_run_id; resolve via the queue
			// helper.
			//
			//	@concept: wait-set
			if cur.FrameID != nil {
				runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, args.NodeID, args.RunScopeID)
				if err != nil {
					return err
				}
				if ok {
					if err := sb.WaitSet().MarkDrainedBySender(ctx, *cur.FrameID, runID, tx); err != nil {
						return err
					}
				}
			}
			if err := args.Queue.RemoveForNodeInTx(ctx, args.NodeID, args.RunScopeID, args.SupervisorID, tx); err != nil {
				return err
			}
			// Co-commit the canonical signal with the give_up state
			// transition. The `terminal/error/<class>` audit row and
			// the rimsky_nodes failed transition land together —
			// subscribers wildcard-matching `terminal/error/*` never
			// fire on an audit row whose state column still reads
			// running.
			return signalaudit.EmitSignal(ctx, sb.Events(),
				args.InstanceID, args.NodeID, resolutionSig, args.Clock.Now(), tx)
		})

	case "pass":
		// Pass settles the run as fresh. From a stale state (e.g.
		// pre-dispatch acquire/unavailable resolved as pass) this is
		// the canonical acquire-pass transition, carrying the
		// `terminal/error/<class>` envelope on settling_signal_type with
		// Color=fresh. From a running state (executor errored, resolved
		// pass via error_types) it also lands fresh with the same
		// settling_signal_type (mirroring `applyResolvedAction`'s pass
		// branch). Per
		// `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`.
		//
		//	@concept: error-policy
		return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cur, err := sb.Nodes().Get(ctx, args.NodeID, tx)
			if err != nil {
				return err
			}
			if cur == nil {
				return nil
			}
			// Pass settles fresh; settling_signal_type carries the
			// terminal/error/<class> envelope (Color is fresh — see
			// substitution-visibility gate). Per Pass 3 of spec
			// 2026-05-23-signal-taxonomy-and-policy-decoupling-design.
			//
			// Only `stale` (pre-dispatch acquire/unavailable resolved
			// pass) and `running` (executor errored, resolved pass via
			// error_types) are reachable here — both prod call sites
			// (`code:runtime/runner_lifecycle.go::handleAcquireUnavailable`
			// for the stale path; the running path via
			// `code:runtime/runner_error_policy.go::applyErrorPolicy`'s
			// pass branch handles its own state and doesn't reach OnError)
			// guarantee one of those two states. Any other state means
			// the row was rotated under us by a sweep we didn't expect;
			// fail loudly rather than emit a canonical signal that
			// contradicts the actual run row.
			passSig := "terminal/error/" + args.ErrorClass
			switch cur.State {
			case cascade.NodeStateStale:
				if err := sb.Nodes().UpdateState(ctx, args.NodeID, args.RunScopeID,
					cascade.NodeStateFresh, cascade.ReasonAcquirePass,
					&passSig, tx); err != nil {
					return err
				}
			case cascade.NodeStateRunning:
				if err := sb.Nodes().UpdateState(ctx, args.NodeID, args.RunScopeID,
					cascade.NodeStateFresh, cascade.ReasonHandlerPass,
					&passSig, tx); err != nil {
					return err
				}
			default:
				return fmt.Errorf("OnError pass branch: unexpected node state %q for node %s (expected stale|running); a concurrent rotation moved the row out from under us between the policy-resolution tx and the action-apply tx",
					cur.State, args.NodeID)
			}
			// Settled-state drain on fresh+pass: any wait-set rows
			// gating receivers on this sender's run release.
			if cur.FrameID != nil {
				runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, args.NodeID, args.RunScopeID)
				if err != nil {
					return err
				}
				if ok {
					if err := sb.WaitSet().MarkDrainedBySender(ctx, *cur.FrameID, runID, tx); err != nil {
						return err
					}
				}
			}
			if err := args.Queue.RemoveForNodeInTx(ctx, args.NodeID, args.RunScopeID, args.SupervisorID, tx); err != nil {
				return err
			}
			// Co-commit the canonical signal with the pass state
			// transition. The `terminal/error/<class>` audit row
			// (Color=fresh per Resolution.Color, carried by the
			// settling_signal_type column rather than the signal
			// payload) lands together with the rimsky_nodes fresh
			// transition.
			return signalaudit.EmitSignal(ctx, sb.Events(),
				args.InstanceID, args.NodeID, resolutionSig, args.Clock.Now(), tx)
		})
	}
	// `invalidate` action retired under the 2026-05-14 subscription-
	// cascade resolution; the validator rejects it at deploy time.
	return nil
}

// lookupPolicy resolves the error_types[ErrorClass] block from the node's
// template spec. Returns nil (with nil error) when the template does not
// declare a policy for this error class — node.Evaluate treats that as a
// give_up("unknown_error_class"). For the attribute-pipeline classes
// (`template_resolution_failed`, `template_validation_failed`,
// `executor_schema_unavailable`, `attributes_schema_failed`) this is the
// §10.4 / §9.4 default chain `[ {give_up} ]`.
func lookupPolicy(ctx context.Context, sb persistence.Tables, nd *persistence.NodeRow, errorClass string) (*node.ErrorTypePolicy, error) {
	var inst *persistence.InstanceRow
	var tmpl *persistence.TemplateRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := sb.Instances().Get(ctx, nd.InstanceID, tx)
		if err != nil {
			return err
		}
		inst = i
		if inst == nil {
			return nil
		}
		t, err := sb.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		tmpl = t
		return err
	}); err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, nil
	}
	if tmpl == nil {
		return nil, nil
	}
	for _, td := range tmpl.Spec.Nodes {
		if td.Type != nd.NodeType {
			continue
		}
		if p, ok := td.ErrorTypes[errorClass]; ok {
			// Copy so the caller can take its address without aliasing the map.
			cp := p
			return &cp, nil
		}
		return nil, nil
	}
	return nil, nil
}

// requiredStoresForNode resolves the node's required_stores list from
// the template's per-node-type definition. Used when re-enqueueing on
// retry so the rebooted dispatch row carries the same supervisor-pool
// predicate (`required_stores ⊆ accepted_stores`, spec §6.2) as the
// original. Returns nil when the template / node-def cannot be located —
// the queue treats nil and []string{} as equivalent (no required stores
// declared, accepted by every supervisor pool).
func requiredStoresForNode(ctx context.Context, sb persistence.Tables, nd *persistence.NodeRow) []string {
	var inst *persistence.InstanceRow
	var tmpl *persistence.TemplateRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := sb.Instances().Get(ctx, nd.InstanceID, tx)
		if err != nil || i == nil {
			return err
		}
		inst = i
		t, err := sb.Templates().GetByHash(ctx, i.TemplateHash, tx)
		if err != nil {
			return err
		}
		tmpl = t
		return nil
	}); err != nil {
		return nil
	}
	if inst == nil || tmpl == nil {
		return nil
	}
	for _, td := range tmpl.Spec.Nodes {
		if td.Type != nd.NodeType {
			continue
		}
		return node.RequiredStores(td)
	}
	return nil
}
