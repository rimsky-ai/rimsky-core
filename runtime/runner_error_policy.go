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

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	signalpkg "github.com/fallguy/rimsky/foundation/signal"
	signalaudit "github.com/fallguy/rimsky/foundation/signal/audit"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
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
	// Short-circuit retry-loop guard: if this terminal would route to a
	// retry action AND we'd be over the cap, rewrite the error_class
	// before resolving the policy. retry_loop_no_progress's policy
	// (give_up) takes precedence over the original class's retry.
	if errorClass != "retry_loop_no_progress" {
		if shouldForceRetryLoopGiveUp(ctx, args, acq) {
			args.Logger.Warn("applyErrorPolicy: retry_loop_no_progress cap reached; forcing give_up",
				"node_id", acq.NodeID.String(),
				"original_error_class", errorClass)
			// Capture original error class + payload BEFORE reassigning, so the
			// new payload's `original_error_class` records the actual prior
			// class, not the rewritten one.
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
	// E5 counter housekeeping: capture the prior dispatch row's
	// counter so we can carry it forward across the retry round-trip.
	// applyResolvedAction's retry branch removes the old row and
	// inserts a new one — without this carry-forward the counter
	// would reset to zero on every retry and the cap would never
	// trigger.
	priorCount, _, _ := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	var carryForwardCount int
	if isRetryKind(resolved.Kind) {
		carryForwardCount = priorCount + 1
	} else {
		carryForwardCount = 0
	}

	// Construct the canonical Resolution (signal + dispatch disposition
	// + color) from the resolved action. Built once here so signal-emit
	// + applyResolvedAction share the same instance.
	resolution := buildResolution(resolved, errorClass, payload, carryForwardCount)

	// Primary state-mutation work runs inline in the caller's outer tx.
	if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, resolved.NewState, tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	if err := applyResolvedAction(ctx, args, tx, acq, prior, resolved, resolution); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	// On retry, re-stamp the counter onto the freshly-inserted
	// dispatch row so the next iteration's
	// shouldForceRetryLoopGiveUp can see accumulated retries.
	if isRetryKind(resolved.Kind) {
		if err := args.Queue.SetRetryNoProgressForNodeInTx(ctx, tx, acq.NodeID, acq.RunScopeID, carryForwardCount); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: %w", err)
		}
	}
	// Canonical signal emission per concept:signal — co-committed with
	// the state transition that produced it. The audit row on
	// rimsky_events and the rimsky_nodes state change land or roll back
	// together — subscribers wildcard-matching `transient/retry/*` or
	// `terminal/error/*` never see an audit row whose state column
	// still contradicts the disposition. Matches the same-tx emit
	// pattern in `code:runtime/on_error.go::OnError`. The pre-Pass-5
	// fixed-string "error" audit-row retired alongside
	// `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`.
	if err := signalaudit.EmitSignal(ctx, args.Persist.Events(),
		acq.InstanceID, acq.NodeID, resolution.Signal, args.Clock.Now(), tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: emit resolution signal: %w", err)
	}

	// Post-commit work: lineage emit (on give_up) + run-tree
	// propagation (on give_up). Both must run AFTER the state
	// transition commits — PropagateIfChildAfterTerminal reads the
	// just-written child row to drive parent aggregation, and the
	// lineage emit is an observability append that's safe to lose on
	// crash. The canonical signal-emit was hoisted into the outer tx
	// above so it shares the state transition's tx-atomicity guarantee
	// (per `code:runtime/on_error.go::OnError`).
	dispatchID := acq.DispatchID
	resSig := resolution.Signal
	post := func(ctx context.Context) {
		// Run-tree state propagation (E2): give_up is a terminal failure;
		// the child's state has transitioned to NodeStateFailed and any
		// parent must aggregate. Retry / discard_claims_then_retry / pass
		// leave the node in a non-failed terminal state so failure
		// propagation skips them.
		if resolved.Kind == "give_up" {
			// E8: emit leaf-run lineage record for the failed terminal.
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
	// `invalidate` action retired under the 2026-05-14 subscription-
	// cascade resolution; the validator rejects it at deploy time.
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
// @blessed-invariant: State-machine writes for a single run must be
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
			// Pessimistic-invalidate: running → stale is the sender's
			// invalidation in this frame. Gate downstream subscribers
			// so they don't race the retry. The retry's emitted signal
			// (transient/retry/<n>/<class>) drives subscriber CEL
			// matching per concept:signal; subscribers without a
			// transient/retry/* subscription don't fire.
			if err := cascadeSubscribersStaleInTx(ctx, args, tx,
				acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
				resolution.Signal); err != nil {
				return err
			}
		}
		// Thread `runScopeID` so fan-out children's retirement lands
		// on this specific run, not every sibling claimed by this
		// supervisor under the shared `node_id`.
		if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
			return err
		}
		// Recovery-aware fields: this retry supersedes the prior
		// dispatch (acq.DispatchID). The executor reads the
		// predecessor id on
		// proto:executor.proto::ExecuteRequest.prior_dispatch_id
		// at the retry dispatch.
		priorID := acq.DispatchID
		if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                   acq.NodeID,
			ExecutorName:             acq.Executor,
			RequiredStores:           requiredStoresForAcq(acq),
			EnqueuedAt:               args.Clock.Now().Add(time.Duration(resolution.RetryDelayMs) * time.Millisecond),
			FrameID:                  acq.FrameID,
			RunScopeID:               acq.RunScopeID,
			PriorDispatchID:          &priorID,
			PriorDispatchDisposition: "retry_after_error",
		}, tx); err != nil {
			// Defensive: a closed RunScope means the rendezvous has
			// fired while this runner was processing the terminal
			// (e.g. a heartbeat-loss sweep retired the runner's own
			// active dispatch via claimant-guard mismatch and closed
			// the scope). Walker discipline per concept:run-scope:
			// do not enqueue into a closed scope; the state writes
			// above already committed. Mirrors OnError retry and
			// SweepStaleHeartbeats.
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
		// End settles the run; color from Resolution determines the
		// terminal state. give_up → failed; pass → fresh.
		if prior != nil && prior.State == cascade.NodeStateRunning {
			settlingSig := string(resolution.Signal.Type)
			switch resolution.Color {
			case spec.ColorFailed:
				if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
					cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
					return err
				}
			case spec.ColorFresh:
				// Pass settles the run fresh under ReasonHandlerPass.
				// The settling_signal_type column carries the
				// terminal/error/<class> signal-type-path so the
				// substitution-visibility gate (which accepts
				// fresh-color settled runs regardless of the signal
				// type-path's color implication) sees the run as
				// settled-success. The wait-set drain runs the same
				// way as give_up because both are settling terminals.
				if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
					cascade.NodeStateFresh, cascade.ReasonHandlerPass, &settlingSig, tx); err != nil {
					return err
				}
			}
		}
		// Settled-state drain: any wait-set rows gating receivers on
		// this sender's run release.
		if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
			return err
		}
		// Thread `runScopeID` so fan-out children's retirement lands
		// on this specific run, not every sibling claimed by this
		// supervisor under the shared `node_id`.
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
	errorClass string, payload map[string]any, tx persistence.Tx,
) (postCommitFn, error) {
	prior, perr := args.Persist.Nodes().Get(ctx, acq.NodeID, tx)
	if perr != nil && args.Logger != nil {
		args.Logger.Warn("applyTerminalInfraError: load prior node row failed",
			"node_id", acq.NodeID.String(),
			"error", perr.Error())
	}
	// Read the current counter so we can carry it forward onto the
	// freshly-inserted dispatch row.
	priorCount, _, _ := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
	}
	if prior != nil && prior.State == cascade.NodeStateRunning {
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
			cascade.NodeStateStale, cascade.ReasonInfraReenqueue, nil, tx); err != nil {
			return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
		}
	}
	// Thread `runScopeID` so fan-out children's retirement lands
	// on this specific run, not every sibling claimed by this
	// supervisor under the shared `node_id`.
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
	}
	if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
		NodeID:         acq.NodeID,
		ExecutorName:   acq.Executor,
		RequiredStores: requiredStoresForAcq(acq),
		EnqueuedAt:     args.Clock.Now(),
		FrameID:        acq.FrameID,
		RunScopeID:     acq.RunScopeID,
	}, tx); err != nil {
		// Defensive: a closed RunScope means the rendezvous has
		// fired while this runner was processing the infra-error
		// terminal. Walker discipline per concept:run-scope: do
		// not enqueue into a closed scope; skip the counter
		// re-stamp too (no row was inserted to stamp). The state
		// writes above already committed. Mirrors OnError retry
		// and SweepStaleHeartbeats.
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
		// Re-stamp the counter on the freshly-inserted dispatch row so
		// the infra round-trip preserves cap-eligibility.
		if err := args.Queue.SetRetryNoProgressForNodeInTx(ctx, tx, acq.NodeID, acq.RunScopeID, priorCount); err != nil {
			return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
		}
	}

	// Post-commit: best-effort audit-log append.
	post := func(ctx context.Context) {
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			// Canonical signal emission per concept:signal.
			// terminal/infra/<reason> carries the synthesized
			// errorClass as the reason leaf.
			infraSig := signalpkg.Signal{
				Type: signalpkg.TypePath("terminal/infra/" + errorClass),
				Payload: map[string]any{
					"reason":  errorClass,
					"details": payload,
				},
			}
			// terminal/infra/* signal above is the canonical audit row
			// per concept:signal. The pre-Pass-5 fixed-string "error"
			// audit-row retired alongside spec
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
	retriesSoFar int,
) spec.Resolution {
	sig := errorPolicySignal(errorClass, errorPayload, resolved.Kind, retriesSoFar, resolved.DelayMs)
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
	default: // give_up + any unknown kind
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
func errorPolicySignal(errorClass string, errorPayload map[string]any, resolvedKind string, retriesSoFar int, delayMs int) signalpkg.Signal {
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
		// give_up | pass — both settle as terminal/error/<class>;
		// color is differentiated upstream via Resolution.Color.
		typ := signalpkg.TypePath("terminal/error/" + errorClass)
		return signalpkg.Signal{
			Type: typ,
			Payload: map[string]any{
				"error_class":    errorClass,
				"error_payload":  errorPayload,
				"attempt":        retriesSoFar,
				"retries_so_far": retriesSoFar,
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
	// Fall back to the template-spec value if the dispatch row's
	// override has not been denormalized yet. This makes the cap apply
	// to retry-only loops where the row never went through a park
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
	// We want to force give_up when the next retry would put us over.
	// Use count >= maxRetries so maxRetries=100 means "100 retries
	// permitted; the 101st is forced give_up."
	return count >= maxRetries
}
