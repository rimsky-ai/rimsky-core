// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: error-policy
type policyDecision struct {
	Kind    string
	DelayMs int
	Signal  signalpkg.Signal
}

func (d policyDecision) IsRetry() bool {
	return d.Kind == spec.ActionRetry
}

func (d policyDecision) IsReleaseAndRequeue() bool {
	return d.Kind == spec.ActionReleaseAndRequeue
}

func applyErrorPolicy(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, tx persistence.Tx,
) (postCommitFn, error) {
	return applyErrorPolicyWithScratch(ctx, args, acq, errorClass, "", payload, nil, nil, nil, tx)
}

// @concept: executor
// @concept: error-policy
func applyErrorPolicyWithScratch(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, fallbackClass string,
	payload map[string]any, tags []string, attributesDel map[string]any, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	return applyErrorPolicyWithScratchAndSettleHook(ctx, args, acq, errorClass, fallbackClass, payload, tags, attributesDel, scratch, nil, tx)
}

// @concept: executor
// @concept: error-policy
func applyErrorPolicyWithScratchAndSettleHook(
	ctx context.Context, args RunArgs, acq *acquisition, errorClass string, fallbackClass string, payload map[string]any, tags []string, attributesDel map[string]any, scratch []byte, onSettle func(ctx context.Context, tx persistence.Tx) error, tx persistence.Tx,
) (postCommitFn, error) {
	if err := applyTerminalScratchInTx(ctx, args, acq, scratch, tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	policy := lookupPolicyForNodeWithFallback(acq, errorClass, fallbackClass)
	state, err := args.Persist.Nodes().GetRunEvaluatorState(ctx, acq.NodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: load evaluator state: %w", err)
	}
	maxRetries, backoff := resolveRetryConfig(acq.NodeDef)
	resolved := node.Evaluate(policy, state, maxRetries, backoff, nil)
	if err := args.Persist.Nodes().UpdateRunEvaluatorState(ctx, acq.NodeRunID, resolved.NewState, tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: persist evaluator state: %w", err)
	}
	sig := errorPolicySignal(errorClass, payload, attributesDel, tags, resolved.Kind, resolved.NewState.RetryCounter, maxRetries, resolved.DelayMs)
	decision := policyDecision{Kind: resolved.Kind, DelayMs: resolved.DelayMs, Signal: sig}

	if decision.IsRetry() {
		if err := emitSignalInTxOnce(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig, tx); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: emit retry signal: %w", err)
		}
		if err := stampRetryAfterErrorInTx(ctx, args, acq, tx); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: %w", err)
		}
		acq.RetryDecision = &decision
		return nil, nil
	}

	if decision.IsReleaseAndRequeue() {
		if err := emitSignalInTxOnce(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig, tx); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: emit release signal: %w", err)
		}
		releasePC, err := releaseLocksInTx(ctx, args, acq, false, false, tx)
		if err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: release locks: %w", err)
		}
		if err := deleteAbandonedClaimHandlesForRun(ctx, args, acq.NodeRunID, tx); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: clear abandoned claims for requeue: %w", err)
		}
		acq.RetryDecision = &decision
		nodeRunID := acq.NodeRunID
		supID := args.SupervisorID
		logger := args.Logger
		requeuePC := func(ctx context.Context) {
			// @concept: node-run
			if err := args.Queue.ReleaseClaimWithDisposition(ctx, nodeRunID, supID, "retry_after_error"); err != nil && logger != nil {
				logger.Warn("applyErrorPolicy: release-and-requeue failed; row may stay claimed until liveness sweep",
					"dispatch_id", nodeRunID.String(), "error", err.Error())
			}
		}
		return chainPostCommit(releasePC, requeuePC), nil
	}

	if onSettle != nil {
		if err := onSettle(ctx, tx); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: settle hook: %w", err)
		}
	}

	nodeRunID := acq.NodeRunID
	successOutcome := resolved.Kind == spec.ActionPass

	// @concept: claim-handle
	// @decision: held-as-state-not-phase
	heldBeforeRelease, herr := runHasActiveHeldClaims(ctx, args, acq.NodeRunID, tx)
	if herr != nil {
		return nil, fmt.Errorf("applyErrorPolicy: held probe (pre-release): %w", herr)
	}

	releasePC, err := releaseLocksInTx(ctx, args, acq, successOutcome, false, tx)
	if err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	settlingSig := string(sig.Type)
	run, rerr := args.Persist.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
	if rerr != nil {
		return nil, fmt.Errorf("applyErrorPolicy: run lookup: %w", rerr)
	}
	var curState cascade.NodeState
	if run != nil {
		curState = run.State
	}
	giveUpInFlight := resolved.Kind == spec.ActionGiveUp &&
		(curState == cascade.NodeStateRunning || curState == cascade.NodeStateStale)

	// @decision: held-as-state-not-phase
	if giveUpInFlight && heldBeforeRelease {
		pc, err := applyErrorPolicyGiveUpHeld(ctx, args, acq, sig, settlingSig, errorClass, tx)
		if err != nil {
			return nil, err
		}
		acq.RetryDecision = &decision
		return chainPostCommit(releasePC, pc), nil
	}

	var transitioned bool
	finalState := cascade.NodeStateFresh
	if resolved.Kind == spec.ActionPass {
		switch curState {
		case cascade.NodeStateRunning:
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
				cascade.NodeStateFresh, cascade.ReasonHandlerPass, &settlingSig, tx); err != nil {
				return nil, err
			}
			transitioned = true
		case cascade.NodeStateStale:
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
				cascade.NodeStateFresh, cascade.ReasonAcquirePass, &settlingSig, tx); err != nil {
				return nil, err
			}
			transitioned = true
		}
		if transitioned {
			if err := recordRunTreeChanged(ctx, args, acq.NodeRunID, cascade.NodeStateFresh, &settlingSig, false, tx); err != nil {
				return nil, fmt.Errorf("applyErrorPolicy: %w", err)
			}
		}
	} else if giveUpInFlight {
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
			return nil, err
		}
		finalState = cascade.NodeStateFailed
		transitioned = true
	}
	if err := emitSignalInTxOnce(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig, tx); err != nil {
		return nil, err
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, nil, nil, tx); err != nil {
		return nil, err
	}
	if err := drainWaitSetOnSettled(ctx, args, acq.FrameID, acq.NodeRunID, tx); err != nil {
		return nil, err
	}
	if err := args.Queue.RemoveForNode(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, err
	}

	post := func(ctx context.Context) {
		if !transitioned {
			return
		}
		terminalKind := "handler_pass"
		errClass := ""
		if finalState == cascade.NodeStateFailed {
			terminalKind = "errored"
			errClass = errorClass
		}
		scope := resolveAcqScope(ctx, args, acq, nil)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			NodeRunID:          nodeRunID,
			NodeID:             acq.NodeID,
			State:              string(finalState),
			SettlingSignalType: settlingSig,
			ErrorClass:         errClass,
			TerminalKind:       terminalKind,
			NodeAlias:          acq.NodeType,
			ExecutorName:       acq.Executor,
			TemplateHash:       acq.TemplateHash,
			Params:             acq.InstanceParams,
			AttributesMerged:   acq.MergedAttributes,
			HeldClaims:         HeldClaimsForLineage(acq),
			ParentNodeRunID:    scope.ParentNodeRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
		if _, err := PropagateIfChildAfterTerminal(ctx, args, nodeRunID); err != nil && args.Logger != nil {
			args.Logger.Warn("applyErrorPolicy: run-tree propagation failed",
				"run_id", nodeRunID.String(), "error", err.Error())
		}
	}
	acq.RetryDecision = &decision
	return chainPostCommit(releasePC, post), nil
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func applyErrorPolicyGiveUpHeld(
	ctx context.Context, args RunArgs, acq *acquisition, sig signalpkg.Signal, settlingSig, errorClass string, tx persistence.Tx,
) (postCommitFn, error) {
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
		cascade.NodeStateHeld, cascade.ReasonHandlerHeld, &settlingSig, tx); err != nil {
		return nil, err
	}
	if err := args.Queue.RemoveForNode(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicyGiveUpHeld: remove for node: %w", err)
	}
	tmplSpec, terr := loadTemplateSpec(ctx, args, acq.InstanceID, tx)
	if terr != nil {
		return nil, fmt.Errorf("applyErrorPolicyGiveUpHeld: load template for held filter: %w", terr)
	}
	heldFilter := subgraphMemberFilter(tmplSpec, acq.NodeType)
	visited := map[foundationshared.UUID]struct{}{}
	if err := cascadeSignalInTxWithFilter(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig, visited, heldFilter, tx); err != nil {
		return nil, err
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, visited, heldFilter, tx); err != nil {
		return nil, err
	}
	if err := drainWaitSetOnSettled(ctx, args, acq.FrameID, acq.NodeRunID, tx); err != nil {
		return nil, err
	}
	transitionPC, err := transitionThisHolderIfFullyResolved(ctx, args, acq, tx)
	if err != nil {
		return nil, err
	}
	nodeRunID := acq.NodeRunID
	post := func(ctx context.Context) {
		fanoutRecalculate(ctx, args, acq)
		scope := resolveAcqScope(ctx, args, acq, nil)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:       acq.InstanceID,
			FrameID:          acq.FrameID,
			NodeRunID:        nodeRunID,
			NodeID:           acq.NodeID,
			State:            string(cascade.NodeStateHeld),
			TerminalKind:     "errored",
			ErrorClass:       errorClass,
			NodeAlias:        acq.NodeType,
			ExecutorName:     acq.Executor,
			TemplateHash:     acq.TemplateHash,
			Params:           acq.InstanceParams,
			AttributesMerged: acq.MergedAttributes,
			HeldClaims:       HeldClaimsForLineage(acq),
			ParentNodeRunID:  scope.ParentNodeRunID,
			ChildKey:         scope.PartitionKey,
			SubstitutionRefs: CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
	}
	return chainPostCommit(transitionPC, post), nil
}

// @concept: claim-handle
func deleteAbandonedClaimHandlesForRun(
	ctx context.Context, args RunArgs, nodeRunID foundationshared.UUID, tx persistence.Tx,
) error {
	if args.ClaimHandles == nil {
		return nil
	}
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, nodeRunID, tx)
	if err != nil {
		return fmt.Errorf("deleteAbandonedClaimHandlesForRun: list: %w", err)
	}
	for _, row := range rows {
		if row.State != spec.ClaimHandleStateAbandoned || row.IsHeld {
			continue
		}
		if err := args.ClaimHandles.DeleteResolved(ctx, row.ID, tx); err != nil {
			return fmt.Errorf("deleteAbandonedClaimHandlesForRun: delete %s: %w", row.ID, err)
		}
	}
	return nil
}

const defaultInfraRetryCap = 10

// @concept: executor
func applyTerminalInfraError(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	if err := applyTerminalScratchInTx(ctx, args, acq, scratch, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
	}
	state, err := args.Persist.Nodes().GetRunEvaluatorState(ctx, acq.NodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: load evaluator state: %w", err)
	}
	maxRetries, _ := resolveRetryConfig(acq.NodeDef)
	if maxRetries <= 0 {
		maxRetries = defaultInfraRetryCap
	}
	if state.RetryCounter >= maxRetries {
		return applyInfraGiveUp(ctx, args, acq, errorClass, payload, state.RetryCounter, tx)
	}
	state.RetryCounter++
	if err := args.Persist.Nodes().UpdateRunEvaluatorState(ctx, acq.NodeRunID, state, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: persist evaluator state: %w", err)
	}
	infraSig := signalpkg.Signal{
		Type: signalpkg.TypePath("transient/infra/" + errorClass),
		Payload: eventpayload.New(&genv1.TransientInfraSignalPayload{
			Reason:  errorClass,
			Details: signalpkg.MustStruct(payload),
		}),
	}
	if err := emitSignalInTxOnce(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, infraSig, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: emit signal: %w", err)
	}
	if err := stampRetryAfterErrorInTx(ctx, args, acq, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
	}
	acq.RetryDecision = &policyDecision{Kind: spec.ActionRetry, DelayMs: 0, Signal: infraSig}
	return nil, nil
}

// @concept: node-run
// @concept: error-policy
func stampRetryAfterErrorInTx(
	ctx context.Context, args RunArgs, acq *acquisition, tx persistence.Tx,
) error {
	if err := args.Queue.StampPriorDispatch(ctx, acq.NodeRunID, acq.NodeRunID, "retry_after_error", tx); err != nil {
		return fmt.Errorf("stamp retry_after_error: %w", err)
	}
	priorID := acq.NodeRunID
	acq.PriorNodeRunID = &priorID
	acq.PriorDispatchDisposition = "retry_after_error"
	return nil
}

func applyInfraGiveUp(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, retriesSoFar int, tx persistence.Tx,
) (postCommitFn, error) {
	releasePC, err := releaseLocksInTx(ctx, args, acq, false, false, tx)
	if err != nil {
		return nil, fmt.Errorf("applyInfraGiveUp: %w", err)
	}
	settlingSig := "terminal/error/" + errorClass
	run, rerr := args.Persist.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
	if rerr != nil {
		return nil, fmt.Errorf("applyInfraGiveUp: run lookup: %w", rerr)
	}
	var transitioned bool
	if run != nil && (run.State == cascade.NodeStateRunning || run.State == cascade.NodeStateStale) {
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
			return nil, err
		}
		transitioned = true
	}
	sig := signalpkg.BuildTerminalErrorSignal(errorClass, payload, retriesSoFar, retriesSoFar, nil, nil)
	if err := emitSignalInTxOnce(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig, tx); err != nil {
		return nil, err
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, nil, nil, tx); err != nil {
		return nil, err
	}
	if err := drainWaitSetOnSettled(ctx, args, acq.FrameID, acq.NodeRunID, tx); err != nil {
		return nil, err
	}
	if err := args.Queue.RemoveForNode(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, err
	}
	acq.RetryDecision = &policyDecision{Kind: spec.ActionGiveUp, Signal: sig}
	if !transitioned {
		return releasePC, nil
	}
	nodeRunID := acq.NodeRunID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq, nil)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			NodeRunID:          nodeRunID,
			NodeID:             acq.NodeID,
			State:              string(cascade.NodeStateFailed),
			SettlingSignalType: settlingSig,
			ErrorClass:         errorClass,
			TerminalKind:       "errored",
			NodeAlias:          acq.NodeType,
			ExecutorName:       acq.Executor,
			TemplateHash:       acq.TemplateHash,
			Params:             acq.InstanceParams,
			AttributesMerged:   acq.MergedAttributes,
			HeldClaims:         HeldClaimsForLineage(acq),
			ParentNodeRunID:    scope.ParentNodeRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
		if _, err := PropagateIfChildAfterTerminal(ctx, args, nodeRunID); err != nil && args.Logger != nil {
			args.Logger.Warn("applyInfraGiveUp: run-tree propagation failed",
				"run_id", nodeRunID.String(), "error", err.Error())
		}
	}
	return chainPostCommit(releasePC, post), nil
}

func lookupPolicyForNode(acq *acquisition, errorClass string) *node.ErrorTypePolicy {
	if acq.NodeDef == nil {
		return nil
	}
	p, ok := acq.NodeDef.ErrorTypes[errorClass]
	if !ok {
		return nil
	}
	cp := p
	return &cp
}

// @decision: acquire-prefix-fallback
func lookupPolicyForNodeWithFallback(acq *acquisition, primary, fallback string) *node.ErrorTypePolicy {
	if p := lookupPolicyForNode(acq, primary); p != nil {
		return p
	}
	if fallback != "" && fallback != primary {
		return lookupPolicyForNode(acq, fallback)
	}
	return nil
}

func resolveRetryConfig(nd *node.TemplateNodeDef) (int, node.BackoffConfig) {
	maxRetries := 0
	if nd != nil && nd.MaxRetries != nil {
		maxRetries = *nd.MaxRetries
	}
	var backoff node.BackoffConfig
	if nd != nil && nd.RetryBackoff != nil {
		backoff = node.BackoffConfig{
			Kind:        nd.RetryBackoff.Kind,
			Jitter:      nd.RetryBackoff.Jitter,
			BaseDelayMs: nd.RetryBackoff.BaseDelayMs,
			MaxDelayMs:  nd.RetryBackoff.MaxDelayMs,
		}
	}
	return maxRetries, backoff
}

func requiredClaimProducersForAcq(acq *acquisition) []string {
	if acq == nil || acq.NodeDef == nil {
		return nil
	}
	return node.RequiredClaimProducers(*acq.NodeDef)
}

// @concept: signal
func errorPolicySignal(errorClass string, errorPayload map[string]any, attributesDelta map[string]any, tags []string, resolvedKind string, retriesSoFar int, retryCap int, delayMs int) signalpkg.Signal {
	switch resolvedKind {
	case spec.ActionRetry:
		typ := signalpkg.TypePath(fmt.Sprintf("transient/retry/%d/%s", retriesSoFar, errorClass))
		return signalpkg.Signal{
			Type: typ,
			Payload: eventpayload.New(&genv1.TransientRetrySignalPayload{
				Attempt:      int32(retriesSoFar),
				Cap:          int32(retryCap),
				ErrorClass:   errorClass,
				DelayMs:      int32(delayMs),
				ErrorPayload: signalpkg.MustStruct(errorPayload),
			}),
		}
	case spec.ActionReleaseAndRequeue:
		typ := signalpkg.TypePath("transient/release_and_requeue/" + errorClass)
		return signalpkg.Signal{
			Type: typ,
			Payload: eventpayload.New(&genv1.TransientReleaseAndRequeueSignalPayload{
				ErrorClass:   errorClass,
				ErrorPayload: signalpkg.MustStruct(errorPayload),
			}),
		}
	default:
		return signalpkg.BuildTerminalErrorSignal(errorClass, errorPayload, retriesSoFar, retriesSoFar, attributesDelta, tags)
	}
}
