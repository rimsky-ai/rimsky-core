// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
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
	return applyErrorPolicyWithScratch(ctx, args, acq, errorClass, "", payload, nil, nil, tx)
}

// @concept: executor
// @concept: error-policy
func applyErrorPolicyWithScratch(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, fallbackClass string,
	payload map[string]any, tags []string, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, scratch); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	policy := lookupPolicyForNodeWithFallback(acq, errorClass, fallbackClass)
	state, err := args.Persist.Nodes().GetRunEvaluatorState(ctx, acq.DispatchID, tx)
	if err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: load evaluator state: %w", err)
	}
	maxRetries, backoff := resolveRetryConfig(acq.NodeDef)
	resolved := node.Evaluate(policy, state, errorClass, maxRetries, backoff, nil)
	if err := args.Persist.Nodes().UpdateRunEvaluatorState(ctx, acq.DispatchID, resolved.NewState, tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: persist evaluator state: %w", err)
	}
	sig := errorPolicySignal(errorClass, payload, tags, resolved.Kind, resolved.NewState.RetryCounter, resolved.DelayMs)
	decision := policyDecision{Kind: resolved.Kind, DelayMs: resolved.DelayMs, Signal: sig}

	if decision.IsRetry() {
		if err := emitSignalInTxOnce(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, sig); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: emit retry signal: %w", err)
		}
		acq.RetryDecision = &decision
		return nil, nil
	}

	if decision.IsReleaseAndRequeue() {
		if err := emitSignalInTxOnce(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, sig); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: emit release signal: %w", err)
		}
		releasePC, err := releaseLocksInTx(ctx, args, tx, acq, false, false)
		if err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: release locks: %w", err)
		}
		acq.RetryDecision = &decision
		dispatchID := acq.DispatchID
		supID := args.SupervisorID
		logger := args.Logger
		requeuePC := func(ctx context.Context) {
			if err := args.Queue.ReleaseClaim(ctx, dispatchID, supID); err != nil && logger != nil {
				logger.Warn("applyErrorPolicy: ReleaseClaim failed; row may stay claimed until liveness sweep",
					"dispatch_id", dispatchID.String(), "error", err.Error())
			}
		}
		return chainPostCommit(releasePC, requeuePC), nil
	}

	dispatchID := acq.DispatchID
	successOutcome := resolved.Kind == spec.ActionPass
	releasePC, err := releaseLocksInTx(ctx, args, tx, acq, successOutcome, false)
	if err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	settlingSig := string(sig.Type)
	if resolved.Kind == spec.ActionPass {
		latest, lerr := args.Persist.Nodes().GetLatestRunInScope(ctx, tx, acq.NodeID, acq.RunScopeID)
		if lerr != nil {
			return nil, fmt.Errorf("applyErrorPolicy: latest run lookup: %w", lerr)
		}
		var curState cascade.NodeState
		if latest != nil {
			curState = latest.State
		}
		switch curState {
		case cascade.NodeStateRunning:
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
				cascade.NodeStateFresh, cascade.ReasonHandlerPass, &settlingSig, tx); err != nil {
				return nil, err
			}
		case cascade.NodeStateStale:
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
				cascade.NodeStateFresh, cascade.ReasonAcquirePass, &settlingSig, tx); err != nil {
				return nil, err
			}
		}
	} else {
		latest, lerr := args.Persist.Nodes().GetLatestRunInScope(ctx, tx, acq.NodeID, acq.RunScopeID)
		if lerr != nil {
			return nil, fmt.Errorf("applyErrorPolicy: latest run lookup: %w", lerr)
		}
		if latest != nil && (latest.State == cascade.NodeStateRunning || latest.State == cascade.NodeStateStale) {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
				cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
				return nil, err
			}
		}
	}
	if err := emitSignalInTxOnce(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, sig); err != nil {
		return nil, err
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
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
				RunID:              dispatchID,
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
				ParentRunID:        scope.ParentRunID,
				ChildKey:           scope.PartitionKey,
				SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
			})
			if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
				cascade.NodeStateFailed, &settlingSig); err != nil {
				args.Logger.Warn("applyErrorPolicy: run-tree propagation failed",
					"run_id", dispatchID.String(), "error", err.Error())
			}
		}
	}
	acq.RetryDecision = &decision
	return chainPostCommit(releasePC, post), nil
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
	state, err := args.Persist.Nodes().GetRunEvaluatorState(ctx, acq.DispatchID, tx)
	if err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: load evaluator state: %w", err)
	}
	maxRetries, _ := resolveRetryConfig(acq.NodeDef)
	if maxRetries <= 0 {
		maxRetries = defaultInfraRetryCap
	}
	if state.RetryCounter >= maxRetries {
		return applyInfraGiveUp(ctx, args, acq, errorClass, payload, tx)
	}
	state.RetryCounter++
	if err := args.Persist.Nodes().UpdateRunEvaluatorState(ctx, acq.DispatchID, state, tx); err != nil {
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
		acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, infraSig); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: emit signal: %w", err)
	}
	acq.RetryDecision = &policyDecision{Kind: spec.ActionRetry, DelayMs: 0, Signal: infraSig}
	return nil, nil
}

func applyInfraGiveUp(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, tx persistence.Tx,
) (postCommitFn, error) {
	releasePC, err := releaseLocksInTx(ctx, args, tx, acq, false, false)
	if err != nil {
		return nil, fmt.Errorf("applyInfraGiveUp: %w", err)
	}
	settlingSig := "terminal/error/" + errorClass
	latest, lerr := args.Persist.Nodes().GetLatestRunInScope(ctx, tx, acq.NodeID, acq.RunScopeID)
	if lerr != nil {
		return nil, fmt.Errorf("applyInfraGiveUp: latest run lookup: %w", lerr)
	}
	if latest != nil && (latest.State == cascade.NodeStateRunning || latest.State == cascade.NodeStateStale) {
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
			return nil, err
		}
	}
	sig := signalpkg.Signal{
		Type: signalpkg.TypePath(settlingSig),
		Payload: map[string]any{
			"reason":  errorClass,
			"details": payload,
		},
	}
	if err := emitSignalInTxOnce(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID, sig); err != nil {
		return nil, err
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, err
	}
	if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
		return nil, err
	}
	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return signalaudit.EmitSignal(ctx, args.Persist.Events(),
				acq.InstanceID, acq.NodeID, sig, args.Clock.Now(), tx)
		}); err != nil && args.Logger != nil {
			args.Logger.Warn("applyInfraGiveUp: emit terminal signal failed",
				"node_id", acq.NodeID.String(),
				"error_class", errorClass,
				"error", err.Error())
		}
		_ = dispatchID
	}
	acq.RetryDecision = &policyDecision{Kind: spec.ActionGiveUp, Signal: sig}
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
func errorPolicySignal(errorClass string, errorPayload map[string]any, tags []string, resolvedKind string, retriesSoFar int, delayMs int) signalpkg.Signal {
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
