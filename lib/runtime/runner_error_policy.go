// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, scratch); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	policy := lookupPolicyForNodeWithFallback(acq, errorClass, fallbackClass)
	state, err := args.Persist.Nodes().GetRunEvaluatorState(ctx, acq.NodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: load evaluator state: %w", err)
	}
	maxRetries, backoff := resolveRetryConfig(acq.NodeDef)
	resolved := node.Evaluate(policy, state, errorClass, maxRetries, backoff, nil)
	if err := args.Persist.Nodes().UpdateRunEvaluatorState(ctx, acq.NodeRunID, resolved.NewState, tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: persist evaluator state: %w", err)
	}
	sig := errorPolicySignal(errorClass, payload, attributesDel, tags, resolved.Kind, resolved.NewState.RetryCounter, resolved.DelayMs)
	decision := policyDecision{Kind: resolved.Kind, DelayMs: resolved.DelayMs, Signal: sig}

	if decision.IsRetry() {
		if err := emitSignalInTxOnce(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: emit retry signal: %w", err)
		}
		acq.RetryDecision = &decision
		return nil, nil
	}

	if decision.IsReleaseAndRequeue() {
		if err := emitSignalInTxOnce(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: emit release signal: %w", err)
		}
		releasePC, err := releaseLocksInTx(ctx, args, tx, acq, false, false)
		if err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: release locks: %w", err)
		}
		if err := deleteAbandonedClaimHandlesForRun(ctx, args, tx, acq.NodeRunID); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: clear abandoned claims for requeue: %w", err)
		}
		acq.RetryDecision = &decision
		nodeRunID := acq.NodeRunID
		supID := args.SupervisorID
		logger := args.Logger
		requeuePC := func(ctx context.Context) {
			if err := args.Queue.ReleaseClaim(ctx, nodeRunID, supID); err != nil && logger != nil {
				logger.Warn("applyErrorPolicy: ReleaseClaim failed; row may stay claimed until liveness sweep",
					"dispatch_id", nodeRunID.String(), "error", err.Error())
			}
		}
		return chainPostCommit(releasePC, requeuePC), nil
	}

	nodeRunID := acq.NodeRunID
	successOutcome := resolved.Kind == spec.ActionPass
	releasePC, err := releaseLocksInTx(ctx, args, tx, acq, successOutcome, false)
	if err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	settlingSig := string(sig.Type)
	run, rerr := args.Persist.Nodes().GetRunForGate(ctx, tx, acq.NodeRunID)
	if rerr != nil {
		return nil, fmt.Errorf("applyErrorPolicy: run lookup: %w", rerr)
	}
	var curState cascade.NodeState
	if run != nil {
		curState = run.State
	}
	if resolved.Kind == spec.ActionPass {
		switch curState {
		case cascade.NodeStateRunning:
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
				cascade.NodeStateFresh, cascade.ReasonHandlerPass, &settlingSig, tx); err != nil {
				return nil, err
			}
		case cascade.NodeStateStale:
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
				cascade.NodeStateFresh, cascade.ReasonAcquirePass, &settlingSig, tx); err != nil {
				return nil, err
			}
		}
	} else if curState == cascade.NodeStateRunning || curState == cascade.NodeStateStale {
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
			return nil, err
		}
	}
	if err := emitSignalInTxOnce(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig); err != nil {
		return nil, err
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID,
		nil, nil); err != nil {
		return nil, err
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.NodeRunID); err != nil {
		return nil, err
	}
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, err
	}

	post := func(ctx context.Context) {
		if resolved.Kind == spec.ActionGiveUp {
			scope := resolveAcqScope(ctx, args, acq)
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
			if _, err := PropagateIfChildAfterTerminal(ctx, args, nodeRunID,
				cascade.NodeStateFailed, &settlingSig); err != nil {
				args.Logger.Warn("applyErrorPolicy: run-tree propagation failed",
					"run_id", nodeRunID.String(), "error", err.Error())
			}
		}
	}
	acq.RetryDecision = &decision
	return chainPostCommit(releasePC, post), nil
}

// @concept: claim-handle
func deleteAbandonedClaimHandlesForRun(
	ctx context.Context, args RunArgs, tx persistence.Tx, nodeRunID foundationshared.UUID,
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
	if isSubgraphExitNode(acq) {
		return nil, nil
	}
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, scratch); err != nil {
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
		Payload: map[string]any{
			"reason":  errorClass,
			"details": payload,
		},
	}
	if err := emitSignalInTxOnce(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, infraSig); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: emit signal: %w", err)
	}
	acq.RetryDecision = &policyDecision{Kind: spec.ActionRetry, DelayMs: 0, Signal: infraSig}
	return nil, nil
}

func applyInfraGiveUp(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, retriesSoFar int, tx persistence.Tx,
) (postCommitFn, error) {
	releasePC, err := releaseLocksInTx(ctx, args, tx, acq, false, false)
	if err != nil {
		return nil, fmt.Errorf("applyInfraGiveUp: %w", err)
	}
	settlingSig := "terminal/error/" + errorClass
	run, rerr := args.Persist.Nodes().GetRunForGate(ctx, tx, acq.NodeRunID)
	if rerr != nil {
		return nil, fmt.Errorf("applyInfraGiveUp: run lookup: %w", rerr)
	}
	if run != nil && (run.State == cascade.NodeStateRunning || run.State == cascade.NodeStateStale) {
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
			return nil, err
		}
	}
	sig := signalpkg.BuildTerminalErrorSignal(errorClass, payload, retriesSoFar, retriesSoFar, nil, nil)
	if err := emitSignalInTxOnce(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID, sig); err != nil {
		return nil, err
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID,
		nil, nil); err != nil {
		return nil, err
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.NodeRunID); err != nil {
		return nil, err
	}
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, err
	}
	acq.RetryDecision = &policyDecision{Kind: spec.ActionGiveUp, Signal: sig}
	return releasePC, nil
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
func errorPolicySignal(errorClass string, errorPayload map[string]any, attributesDelta map[string]any, tags []string, resolvedKind string, retriesSoFar int, delayMs int) signalpkg.Signal {
	switch resolvedKind {
	case spec.ActionRetry:
		typ := signalpkg.TypePath(fmt.Sprintf("transient/retry/%d/%s", retriesSoFar, errorClass))
		return signalpkg.Signal{
			Type: typ,
			Payload: map[string]any{
				"attempt":       retriesSoFar,
				"error_class":   errorClass,
				"delay_ms":      delayMs,
				"error_payload": errorPayload,
			},
		}
	case spec.ActionReleaseAndRequeue:
		typ := signalpkg.TypePath("transient/release_and_requeue/" + errorClass)
		return signalpkg.Signal{
			Type: typ,
			Payload: map[string]any{
				"error_class":   errorClass,
				"error_payload": errorPayload,
			},
		}
	default:
		return signalpkg.BuildTerminalErrorSignal(errorClass, errorPayload, retriesSoFar, retriesSoFar, attributesDelta, tags)
	}
}
