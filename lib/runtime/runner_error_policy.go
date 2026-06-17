// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Error-resolution branch of terminal-event handling — the
// policy-chain dispatch for application Error terminals (post-E.2 the
// wire shape collapsed the pre-rename Blocked / Errored variants into
// a single Error{error_class}; "executor_blocked" is the reserved class
// that replaces the pre-rename Blocked variant). `applyErrorPolicy` +
// `applyResolvedAction` cover the application-Error branch and
// `applyTerminalInfraError` covers the infra-error branch, together
// with their helpers (`lookupPolicyForNode`, `requiredStoresForAcq`).
// Split out of runner_terminal.go to keep that file under the
// cold-read 500-line guideline; the Success branch stays there because
// it interleaves with attribute upsert + cascade fan-out and reads
// better in one place.
//
// Per spec §7.6 / §4.10 invariant 13.
//
// 2026-05-23 reshape: action vocabulary tightens to 4 values
// (pass | give_up | retry | discard_claims_then_retry); the resolution
// flows through a `spec.Resolution` 3-tuple carrying
// (signal, dispatch_disposition, color). Per
// `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// applyErrorPolicy routes an application Error{error_class} terminal
// through the policy chain and drives release + state update + queue
// mutation in one tx. Post-E.2 the wire shape collapsed the pre-rename
// Blocked / Errored variants into a single Error{error_class}; the
// reserved class "executor_blocked" replaces the pre-rename Blocked
// variant.
//
// E5 retry-cap interaction: if the resolved action is a retry shape
// AND the per-row consecutive_retries_no_progress counter (after
// increment) exceeds the effective max-retries-without-progress cap,
// the runner forces an Error{error_class:"retry_loop_no_progress"}
// verdict instead of retrying. Per plan E5.
func applyErrorPolicy(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, tx persistence.Tx,
) (postCommitFn, error) {
	return applyErrorPolicyWithScratch(ctx, args, acq, errorClass, payload, nil, nil, tx)
}

// applyErrorPolicyWithScratch is the scratch-aware entry point. Most
// callers route through applyErrorPolicy (above, scratch == nil); the
// stream-close path calls this directly to thread the executor's
// terminal-attached scratch onto the dispatch row BEFORE the retry
// branch reads it for carry-forward into the successor's
// InitialScratch* enqueue.
//
// @concept: executor
func applyErrorPolicyWithScratch(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, tags []string, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	// @constraint: persist the executor-attached terminal scratch onto
	// the row first so the retry branch's LoadScratchInTx pulls the
	// just-written bytes when stamping the successor's InitialScratch*.
	// Per STORY-opaque-executor-scratch round-trip integrity.
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, scratch); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	// @constraint: short-circuit retry-loop guard — if this terminal
	// would route to a retry action AND we'd be over the cap, rewrite
	// the error_class before resolving the policy. retry_loop_no_progress's
	// policy (give_up) takes precedence over the original class's retry.
	if errorClass != "retry_loop_no_progress" {
		if shouldForceRetryLoopGiveUp(ctx, args, acq) {
			args.Logger.Warn("applyErrorPolicy: retry_loop_no_progress cap reached; forcing give_up",
				"node_id", acq.NodeID.String(),
				"original_error_class", errorClass)
			// @constraint: capture original error class + payload BEFORE
			// reassigning, so the new payload's `original_error_class`
			// records the actual prior class, not the rewritten one.
			origErrorClass := errorClass
			origPayload := payload
			errorClass = "retry_loop_no_progress"
			payload = map[string]any{
				"original_error_class": origErrorClass,
				"original_payload":     origPayload,
			}
		}
	}
	policy, err := lookupPolicyForNode(ctx, args, acq, errorClass)
	if err != nil {
		return nil, err
	}
	state := node.EvaluatorState{}
	prior, perr := args.Persist.Nodes().Get(ctx, acq.NodeID, tx)
	if perr != nil && args.Logger != nil {
		args.Logger.Warn("applyErrorPolicy: load prior node row failed; using zero EvaluatorState",
			"node_id", acq.NodeID.String(),
			"error", perr.Error())
	}
	if prior != nil {
		state = node.EvaluatorState{
			ActionIndex:       prior.ActionIndex,
			RetryCounter:      prior.RetryCounter,
			CurrentErrorClass: prior.CurrentErrorClass,
		}
	}
	resolved := node.Evaluate(policy, state, errorClass, nil)
	// @constraint: E5 counter housekeeping — capture the prior dispatch
	// row's counter so we can carry it forward across the retry round-trip.
	// applyResolvedAction's retry branch removes the old row and inserts
	// a new one; without this carry-forward the counter would reset to
	// zero on every retry and the cap would never trigger.
	priorCount, _, _ := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	var carryForwardCount int
	if isRetryKind(resolved.Kind) {
		carryForwardCount = priorCount + 1
	} else {
		carryForwardCount = 0
	}

	// @constraint: build the canonical Resolution (signal + dispatch
	// disposition + color) once here so signal-emit + applyResolvedAction
	// share the same instance.
	resolution := buildResolution(resolved, errorClass, payload, tags, carryForwardCount)

	if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, resolved.NewState, tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	// @constraint: retry-flavored dispositions re-dispatch into the same
	// RunScope, so the parent-owned linked sub-claims must be retained —
	// see releaseLocksInTx's retainLinkedSubClaims contract.
	if err := releaseLocksInTx(ctx, args, tx, acq, false, isRetryKind(resolved.Kind)); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	if err := applyResolvedAction(ctx, args, tx, acq, prior, resolved, resolution); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	// @constraint: on retry, re-stamp the counter onto the freshly-inserted
	// dispatch row so the next iteration's shouldForceRetryLoopGiveUp can
	// see accumulated retries.
	if isRetryKind(resolved.Kind) {
		if err := args.Queue.SetRetryNoProgressForNodeInTx(ctx, tx, acq.NodeID, acq.RunScopeID, carryForwardCount); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: %w", err)
		}
	}
	// @constraint: the resolution signal's cascade AND its canonical audit
	// row land together in the same tx inside applyResolvedAction, via
	// the single emit chokepoint (emitSignalInTxOnce in signal_emit.go) —
	// co-committed with the state transition that produced it, so
	// subscribers wildcard-matching `transient/retry/*` or
	// `terminal/error/*` never see an audit row whose state column still
	// contradicts the disposition, and a settlement can no longer emit a
	// signal without cascading it.

	// @constraint: post-commit work (lineage emit on give_up + run-tree
	// propagation on give_up) must run AFTER the state transition commits —
	// PropagateIfChildAfterTerminal reads the just-written child row to
	// drive parent aggregation, and the lineage emit is an observability
	// append that's safe to lose on crash. The canonical signal-emit was
	// hoisted into the outer tx above so it shares the state transition's
	// tx-atomicity guarantee (per `code:runtime/on_error.go::OnError`).
	dispatchID := acq.DispatchID
	resSig := resolution.Signal
	post := func(ctx context.Context) {
		// @constraint: run-tree state propagation (E2) — give_up is a
		// terminal failure; the child's state has transitioned to
		// NodeStateFailed and any parent must aggregate. Retry /
		// discard_claims_then_retry / pass leave the node in a non-failed
		// terminal state so failure propagation skips them.
		if resolved.Kind == "give_up" {
			// @constraint: E8 emits leaf-run lineage record for the failed terminal.
			scope := resolveAcqScope(ctx, args, acq)
			EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
				InstanceID:         acq.InstanceID,
				FrameID:            acq.FrameID,
				RunID:              dispatchID,
				NodeID:             acq.NodeID,
				State:              string(cascade.NodeStateFailed),
				SettlingSignalType: string(resSig.Type),
				ErrorClass:         errorClass,
				TerminalKind:       "errored",
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
			settlingSig := string(resSig.Type)
			if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
				cascade.NodeStateFailed, &settlingSig); err != nil {
				args.Logger.Warn("applyErrorPolicy: run-tree propagation failed",
					"run_id", dispatchID.String(), "error", err.Error())
			}
		}
	}
	// @deliberate: `invalidate` action retired under the 2026-05-14
	// subscription-cascade resolution; the validator rejects it at
	// deploy time.
	return post, nil
}

// isRetryKind returns true when the resolved action is a retry-flavored
// disposition (retry | discard_claims_then_retry). Centralizing the
// check keeps the per-flavor list in one place; if a future retry
// flavor lands it only needs to be added here.
func isRetryKind(kind string) bool {
	return kind == "retry" || kind == "discard_claims_then_retry"
}

// applyResolvedAction wraps the per-policy action SQL (state update,
// queue mutation) so applyErrorPolicy stays inside the cold-read
// 100-line guideline. Consumes the canonical Resolution built by
// applyErrorPolicy so the run-row color comes from a single source.
//
// @blessed-invariant: state-machine-writes-single-tx — State-machine writes for a single run must be
// tx-atomic. Any operation that reads a run's current state to decide
// what state to write must perform the read and the write in the same
// transaction. Per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
//
//	@concept: wait-set
func applyResolvedAction(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, prior *persistence.NodeRow, resolved node.ResolvedAction,
	resolution spec.Resolution,
) error {
	switch resolution.DispatchDisposition {
	case spec.DispositionRetry:
		if prior != nil && prior.State == cascade.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
				cascade.NodeStateStale, cascade.ReasonPolicyRetry, nil, tx); err != nil {
				return err
			}
		}
		// @constraint: run-disposition signal transient/retry/<n>/<class> —
		// cascade AND audit via the single emit chokepoint
		// (signal_emit.go), emitted UNCONDITIONALLY (outside the running
		// guard) so the resolution always lands its audit row, matching
		// the prior unconditional emit in applyErrorPolicy.
		// @concept: signal
		if err := emitSignalInTxOnce(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
			resolution.Signal); err != nil {
			return err
		}
		// @constraint: recovery-aware fields — this retry supersedes the
		// prior dispatch (acq.DispatchID). The executor reads the
		// predecessor id on
		// proto:executor.proto::ExecuteRequest.prior_dispatch_id at the
		// retry dispatch.
		priorID := acq.DispatchID
		// @constraint: scratch carry-forward — load the prior dispatch
		// row's executor-attached scratch BEFORE retiring it so the
		// retry dispatch carries the executor's in-flight state across
		// the retry-after-error transition. STORY-opaque-executor-scratch
		// requires scratch round-trip across every prior-dispatch
		// disposition; skipping it here would silently lose the
		// executor's scratch on every policy-driven retry.
		//
		// MUST use EnqueueInTx (not the auto-commit Enqueue wrapper):
		// the scratch load, the RemoveForNodeInTx, and the recovery
		// INSERT MUST share a snapshot. The auto-commit wrapper has a
		// documented closed-scope-race surface (see postgres/queue.go
		// EnqueueInTx comment lines 84-98) where a concurrent
		// RunScopes().Close() commit between the INSERT and the
		// fallback SELECT can silently drop the retry-after-error
		// enqueue.
		//
		// @concept: executor
		scratchInline, scratchHandle, scratchBackend, lerr := args.Queue.LoadScratchInTx(ctx, tx, priorID)
		if lerr != nil {
			return fmt.Errorf("load prior scratch: %w", lerr)
		}
		// @constraint: thread `runScopeID` so fan-out children's
		// retirement lands on this specific run, not every sibling
		// claimed by this supervisor under the shared `node_id`.
		if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
			return err
		}
		if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                      acq.NodeID,
			ExecutorName:                acq.Executor,
			RequiredStores:              requiredStoresForAcq(acq),
			EnqueuedAt:                  args.Clock.Now().Add(time.Duration(resolution.RetryDelayMs) * time.Millisecond),
			FrameID:                     acq.FrameID,
			RunScopeID:                  acq.RunScopeID,
			PriorDispatchID:             &priorID,
			PriorDispatchDisposition:    "retry_after_error",
			InitialScratchInline:        scratchInline,
			InitialScratchHandle:        scratchHandle,
			InitialScratchHandleBackend: scratchBackend,
		}, tx); err != nil {
			// @constraint: defensive — a closed RunScope means the
			// rendezvous has fired while this runner was processing the
			// terminal (e.g. SweepExecutorDeadlines retired the runner's
			// own active dispatch via claimant-guard mismatch and closed
			// the scope). Walker discipline per concept:run-scope: do not
			// enqueue into a closed scope; the state writes above already
			// committed. Mirrors OnError retry and SweepExecutorDeadlines.
			// @concept: run-scope
			if errors.Is(err, persistence.ErrRunScopeClosed) {
				if args.Logger != nil {
					args.Logger.Warn("applyResolvedAction retry: skip enqueue: run scope closed",
						"node_id", acq.NodeID.String(),
						"run_scope_id", acq.RunScopeID.String())
				}
				return nil
			}
			return err
		}
		return nil
	case spec.DispositionEnd:
		// @constraint: End settles the run; color from Resolution
		// determines the terminal state. give_up → failed; pass → fresh.
		if prior != nil && prior.State == cascade.NodeStateRunning {
			settlingSig := string(resolution.Signal.Type)
			switch resolution.Color {
			case spec.ColorFailed:
				if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
					cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
					return err
				}
			case spec.ColorFresh:
				// @constraint: Pass settles the run fresh under
				// ReasonHandlerPass. The settling_signal_type column
				// carries the terminal/error/<class> signal-type-path so
				// the substitution-visibility gate (which accepts
				// fresh-color settled runs regardless of the signal
				// type-path's color implication) sees the run as
				// settled-success. The wait-set drain runs the same way
				// as give_up because both are settling terminals.
				if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
					cascade.NodeStateFresh, cascade.ReasonHandlerPass, &settlingSig, tx); err != nil {
					return err
				}
			}
		}
		// @constraint: run-disposition signal terminal/error/<class> —
		// cascade AND audit via the single emit chokepoint
		// (signal_emit.go), emitted UNCONDITIONALLY (outside the running
		// guard) so the resolution always lands its audit row, matching
		// the prior unconditional emit in applyErrorPolicy. It runs
		// BEFORE the settled-state drain below so gated receivers are
		// affirmed-then-released in the same tx (insert-then-drain).
		// @concept: cascade
		// @concept: signal
		if err := emitSignalInTxOnce(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
			resolution.Signal); err != nil {
			return err
		}
		// @constraint: settled-state drain — any wait-set rows gating
		// receivers on this sender's run release.
		if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
			return err
		}
		// @constraint: thread `runScopeID` so fan-out children's
		// retirement lands on this specific run, not every sibling claimed
		// by this supervisor under the shared `node_id`.
		return args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx)
	}
	return nil
}

// applyTerminalInfraError is the infra_reenqueue path. State→stale,
// failure-branch release, re-enqueue. Single tx.
//
// Retry-counter handling: an infra-reenqueue is NOT an application
// retry — the node didn't actually run because of an infrastructure
// fault (executor unreachable, transient supervisor crash, etc.). The
// per-row consecutive_retries_no_progress counter is therefore
// preserved across the round-trip rather than reset, mirroring the
// retry path's carry-forward (runner_error_policy.go::applyErrorPolicy).
// Without this carry, an executor that's flaky enough to alternate
// between infra-error and app-retry-with-no-progress could loop
// indefinitely because the cap would reset to 0 every infra round-trip.
func applyTerminalInfraError(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	// @constraint: sub-graph exit short-circuit — an exit row's terminal
	// scratch is already dropped at the persist site (see
	// applyTerminalScratchInTx's carve-out), but a load of the same row
	// would then return whatever prior mid-dispatch HTTP-route scratch
	// happened to land there — an asymmetric "terminal scratch dropped /
	// mid-dispatch scratch carried" bug. The exit terminates and
	// propagates state to the parent; the parent doesn't re-dispatch
	// the exit, so an infra re-enqueue here would be incorrect
	// regardless. Skip the entire scratch + re-enqueue chain so the
	// asymmetry cannot surface.
	// @concept: executor
	if isSubgraphExitNode(acq) {
		return nil, nil
	}
	// @constraint: persist executor-attached scratch onto the row BEFORE
	// the infra re-enqueue reads it for carry-forward. Mirrors the
	// application retry path; STORY-opaque-executor-scratch's
	// round-trip integrity is the load-bearing property.
	// @concept: executor
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, scratch); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
	}
	prior, perr := args.Persist.Nodes().Get(ctx, acq.NodeID, tx)
	if perr != nil && args.Logger != nil {
		args.Logger.Warn("applyTerminalInfraError: load prior node row failed",
			"node_id", acq.NodeID.String(),
			"error", perr.Error())
	}
	// @constraint: read the current counter so we can carry it forward
	// onto the freshly-inserted dispatch row.
	priorCount, _, _ := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	// @constraint: scratch carry-forward — load the prior dispatch row's
	// executor-attached scratch BEFORE the zombie row is retired by
	// RemoveForNodeInTx below. Without this load, the recovery enqueue
	// creates a successor with empty scratch and the executor's
	// in-flight state (written via the §scratch callback before the
	// stream broke, or attached to the infra terminal itself just above
	// via applyTerminalScratchInTx) is silently lost.
	//
	// STORY-opaque-executor-scratch pins scratch round-trip across
	// every recovery enqueue disposition; the infra-reenqueue path is
	// the runtime's recovery for executor stream breaks, dial failures,
	// and build-request failures — exactly the cases where an executor
	// that wrote scratch mid-dispatch most needs the carry. Mirrors the
	// pattern in applyResolvedAction's retry branch.
	//
	// MUST use EnqueueInTx (not the auto-commit Enqueue wrapper): the
	// scratch load, the RemoveForNodeInTx, and the recovery INSERT MUST
	// share a snapshot. The auto-commit wrapper has a documented
	// closed-scope-race surface (see postgres/queue.go EnqueueInTx
	// comment lines 84-98) where a concurrent RunScopes().Close()
	// commit between the INSERT and the fallback SELECT can silently
	// drop the infra-reenqueue.
	//
	// @concept: executor
	priorID := acq.DispatchID
	scratchInline, scratchHandle, scratchBackend, lerr := args.Queue.LoadScratchInTx(ctx, tx, priorID)
	if lerr != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: load prior scratch: %w", lerr)
	}
	// @constraint: infra-reenqueue re-dispatches into the same RunScope,
	// so the parent-owned linked sub-claims must be retained — see
	// releaseLocksInTx's retainLinkedSubClaims contract.
	if err := releaseLocksInTx(ctx, args, tx, acq, false, true); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
	}
	if prior != nil && prior.State == cascade.NodeStateRunning {
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
			cascade.NodeStateStale, cascade.ReasonInfraReenqueue, nil, tx); err != nil {
			return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
		}
	}
	// @constraint: thread `runScopeID` so fan-out children's retirement
	// lands on this specific run, not every sibling claimed by this
	// supervisor under the shared `node_id`.
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
	}
	if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
		NodeID:                      acq.NodeID,
		ExecutorName:                acq.Executor,
		RequiredStores:              requiredStoresForAcq(acq),
		EnqueuedAt:                  args.Clock.Now(),
		FrameID:                     acq.FrameID,
		RunScopeID:                  acq.RunScopeID,
		PriorDispatchID:             &priorID,
		PriorDispatchDisposition:    "retry_after_error",
		InitialScratchInline:        scratchInline,
		InitialScratchHandle:        scratchHandle,
		InitialScratchHandleBackend: scratchBackend,
	}, tx); err != nil {
		// @constraint: defensive — a closed RunScope means the rendezvous
		// has fired while this runner was processing the infra-error
		// terminal. Walker discipline per concept:run-scope: do not
		// enqueue into a closed scope; skip the counter re-stamp too (no
		// row was inserted to stamp). The state writes above already
		// committed. Mirrors OnError retry and SweepStaleHeartbeats.
		// @concept: run-scope
		if errors.Is(err, persistence.ErrRunScopeClosed) {
			if args.Logger != nil {
				args.Logger.Warn("applyTerminalInfraError: skip re-enqueue: run scope closed",
					"node_id", acq.NodeID.String(),
					"run_scope_id", acq.RunScopeID.String())
			}
		} else {
			return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
		}
	} else {
		// @constraint: re-stamp the counter on the freshly-inserted
		// dispatch row so the infra round-trip preserves cap-eligibility.
		if err := args.Queue.SetRetryNoProgressForNodeInTx(ctx, tx, acq.NodeID, acq.RunScopeID, priorCount); err != nil {
			return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
		}
	}

	// @constraint: post-commit best-effort audit-log append.
	post := func(ctx context.Context) {
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			// @constraint: canonical signal emission —
			// terminal/infra/<reason> carries the synthesized errorClass
			// as the reason leaf.
			// @concept: signal
			infraSig := signalpkg.Signal{
				Type: signalpkg.TypePath("terminal/infra/" + errorClass),
				Payload: map[string]any{
					"reason":  errorClass,
					"details": payload,
				},
			}
			// @deliberate: the terminal/infra/* signal above is the
			// canonical audit row per concept:signal. The pre-Pass-5
			// fixed-string "error" audit-row retired alongside spec
			// 2026-05-23-signal-taxonomy-and-policy-decoupling-design.
			return signalaudit.EmitSignal(ctx, args.Persist.Events(),
				acq.InstanceID, acq.NodeID, infraSig, args.Clock.Now(), tx)
		}); err != nil && args.Logger != nil {
			args.Logger.Warn("applyTerminalInfraError: emit terminal/infra signal failed",
				"node_id", acq.NodeID.String(),
				"error_class", errorClass,
				"error", err.Error())
		}
	}
	return post, nil
}

// lookupPolicyForNode resolves the per-error-class policy from the
// candidate's template node-def. Nil return = no-policy.
func lookupPolicyForNode(
	_ context.Context, _ RunArgs, acq *acquisition, errorClass string,
) (*node.ErrorTypePolicy, error) {
	if acq.NodeDef == nil {
		return nil, nil
	}
	p, ok := acq.NodeDef.ErrorTypes[errorClass]
	if !ok {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

// requiredStoresForAcq derives the list of store names referenced by
// this acquisition's lock specs.
func requiredStoresForAcq(acq *acquisition) []string {
	if acq == nil || acq.NodeDef == nil {
		return nil
	}
	return node.RequiredStores(*acq.NodeDef)
}

// buildResolution constructs the canonical `spec.Resolution` 3-tuple
// from a `node.ResolvedAction` plus the originating error context.
// Decouples the conflated `PolicyAction` / `ResolvedAction` pair into
// three orthogonal axes: signal envelope, dispatch disposition, and
// settled color. Per
// `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`.
//
// retriesSoFar is 1-indexed per the spec (the first retry emits
// attempt=1); the caller should pass the post-increment counter for
// retry resolutions and 0 otherwise.
//
//	@concept: error-policy
//	@concept: signal
func buildResolution(
	resolved node.ResolvedAction,
	errorClass string,
	errorPayload map[string]any,
	tags []string,
	retriesSoFar int,
) spec.Resolution {
	sig := errorPolicySignal(errorClass, errorPayload, tags, resolved.Kind, retriesSoFar, resolved.DelayMs)
	switch resolved.Kind {
	case "retry":
		return spec.Resolution{
			Signal:              sig,
			DispatchDisposition: spec.DispositionRetry,
			RetryDiscardClaims:  false,
			RetryDelayMs:        resolved.DelayMs,
		}
	case "discard_claims_then_retry":
		return spec.Resolution{
			Signal:              sig,
			DispatchDisposition: spec.DispositionRetry,
			RetryDiscardClaims:  true,
			RetryDelayMs:        resolved.DelayMs,
		}
	case "pass":
		return spec.Resolution{
			Signal:              sig,
			DispatchDisposition: spec.DispositionEnd,
			Color:               spec.ColorFresh,
		}
	default:
		return spec.Resolution{
			Signal:              sig,
			DispatchDisposition: spec.DispositionEnd,
			Color:               spec.ColorFailed,
		}
	}
}

// errorPolicySignal constructs the canonical signal-envelope for a
// resolved Error policy action.
//
//   - retry / discard_claims_then_retry → transient/retry/<attempt>/<class>
//   - give_up / pass                    → terminal/error/<class>
//
// The retries-so-far counter is 1-indexed per the spec (the first retry
// emits attempt=1). Pass and give_up emit the same signal shape; the
// run-row color differentiation lives on the `Resolution.Color`
// dimension, not the signal payload.
//
//	@concept: signal
func errorPolicySignal(errorClass string, errorPayload map[string]any, tags []string, resolvedKind string, retriesSoFar int, delayMs int) signalpkg.Signal {
	switch resolvedKind {
	case "retry", "discard_claims_then_retry":
		typ := signalpkg.TypePath(fmt.Sprintf("transient/retry/%d/%s", retriesSoFar, errorClass))
		return signalpkg.Signal{
			Type: typ,
			Payload: map[string]any{
				"attempt":          retriesSoFar,
				"error_class":      errorClass,
				"discarded_claims": resolvedKind == "discard_claims_then_retry",
				"delay_ms":         delayMs,
				"error_payload":    errorPayload,
			},
		}
	default:
		// @constraint: give_up | pass — both settle as
		// terminal/error/<class>; color is differentiated upstream via
		// Resolution.Color. concept:terminal-tag — Tags ride on the
		// settling Error envelope so subscribers can
		// `when: "<tag>" in payload.tags`-filter the same as on success.
		typ := signalpkg.TypePath("terminal/error/" + errorClass)
		return signalpkg.Signal{
			Type: typ,
			Payload: map[string]any{
				"error_class":    errorClass,
				"error_payload":  errorPayload,
				"attempt":        retriesSoFar,
				"retries_so_far": retriesSoFar,
				"tags":           tags,
			},
		}
	}
}

// shouldForceRetryLoopGiveUp returns true when the per-row
// consecutive_retries_no_progress counter is at or above the effective
// cap. The cap is resolved per plan E5:
//
//	per-row dispatch-tuning override (denormalized via
//	UpdateDispatchTuningInTx at park time)
//	> template-spec NodeDef.MaxRetriesWithoutProgress (read at retry
//	  time; lets the cap apply to non-parked retry loops too)
//	> deployment default (RunArgs.MaxRetriesWithoutProgressDefault)
//	> built-in default (100)
//
// A per-row or per-template override of 0 disables the cap entirely.
func shouldForceRetryLoopGiveUp(ctx context.Context, args RunArgs, acq *acquisition) bool {
	count, override, err := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	if err != nil {
		return false
	}
	// @constraint: fall back to the template-spec value if the dispatch
	// row's override has not been denormalized yet. This makes the cap
	// apply to retry-only loops where the row never went through a park
	// transition.
	if override == nil && acq.NodeDef != nil && acq.NodeDef.MaxRetriesWithoutProgress != nil {
		override = acq.NodeDef.MaxRetriesWithoutProgress
	}
	if override != nil && *override == 0 {
		return false
	}
	maxRetries := resolveMaxRetriesCap(args, override)
	if maxRetries <= 0 {
		return false
	}
	// @constraint: force give_up when the next retry would put us over.
	// Use count >= maxRetries so maxRetries=100 means "100 retries
	// permitted; the 101st is forced give_up."
	return count >= maxRetries
}
