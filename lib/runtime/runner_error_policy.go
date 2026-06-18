// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func applyErrorPolicy(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, tx persistence.Tx,
) (postCommitFn, error) {
	return applyErrorPolicyWithScratch(ctx, args, acq, errorClass, payload, nil, nil, tx)
}

// @concept: executor
func applyErrorPolicyWithScratch(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, tags []string, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, scratch); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	if errorClass != "retry_loop_no_progress" {
		if shouldForceRetryLoopGiveUp(ctx, args, acq) {
			args.Logger.Warn("applyErrorPolicy: retry_loop_no_progress cap reached; forcing give_up",
				"node_id", acq.NodeID.String(),
				"original_error_class", errorClass)
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
	priorCount, _, _ := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	var carryForwardCount int
	if isRetryKind(resolved.Kind) {
		carryForwardCount = priorCount + 1
	} else {
		carryForwardCount = 0
	}

	resolution := buildResolution(resolved, errorClass, payload, tags, carryForwardCount)

	if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, resolved.NewState, tx); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	if err := releaseLocksInTx(ctx, args, tx, acq, false, isRetryKind(resolved.Kind)); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	if err := applyResolvedAction(ctx, args, tx, acq, prior, resolved, resolution); err != nil {
		return nil, fmt.Errorf("applyErrorPolicy: %w", err)
	}
	if isRetryKind(resolved.Kind) {
		if err := args.Queue.SetRetryNoProgressForNodeInTx(ctx, tx, acq.NodeID, acq.RunScopeID, carryForwardCount); err != nil {
			return nil, fmt.Errorf("applyErrorPolicy: %w", err)
		}
	}

	dispatchID := acq.DispatchID
	resSig := resolution.Signal
	post := func(ctx context.Context) {
		if resolved.Kind == "give_up" {
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
	return post, nil
}

func isRetryKind(kind string) bool {
	return kind == "retry" || kind == "discard_claims_then_retry"
}

// @blessed-invariant: state-machine-writes-single-tx
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
		// @concept: signal
		if err := emitSignalInTxOnce(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
			resolution.Signal); err != nil {
			return err
		}
		priorID := acq.DispatchID
		// @concept: executor
		scratchInline, scratchHandle, scratchBackend, lerr := args.Queue.LoadScratchInTx(ctx, tx, priorID)
		if lerr != nil {
			return fmt.Errorf("load prior scratch: %w", lerr)
		}
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
		if prior != nil && prior.State == cascade.NodeStateRunning {
			settlingSig := string(resolution.Signal.Type)
			switch resolution.Color {
			case spec.ColorFailed:
				if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
					cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &settlingSig, tx); err != nil {
					return err
				}
			case spec.ColorFresh:
				if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
					cascade.NodeStateFresh, cascade.ReasonHandlerPass, &settlingSig, tx); err != nil {
					return err
				}
			}
		}
		// @concept: cascade
		// @concept: signal
		if err := emitSignalInTxOnce(ctx, args, tx,
			acq.NodeID, acq.NodeType, acq.DispatchID, acq.InstanceID, acq.FrameID,
			resolution.Signal); err != nil {
			return err
		}
		if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
			return err
		}
		return args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx)
	}
	return nil
}

func applyTerminalInfraError(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	// @concept: executor
	if isSubgraphExitNode(acq) {
		return nil, nil
	}
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
	priorCount, _, _ := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	// @concept: executor
	priorID := acq.DispatchID
	scratchInline, scratchHandle, scratchBackend, lerr := args.Queue.LoadScratchInTx(ctx, tx, priorID)
	if lerr != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: load prior scratch: %w", lerr)
	}
	if err := releaseLocksInTx(ctx, args, tx, acq, false, true); err != nil {
		return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
	}
	if prior != nil && prior.State == cascade.NodeStateRunning {
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
			cascade.NodeStateStale, cascade.ReasonInfraReenqueue, nil, tx); err != nil {
			return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
		}
	}
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
		if err := args.Queue.SetRetryNoProgressForNodeInTx(ctx, tx, acq.NodeID, acq.RunScopeID, priorCount); err != nil {
			return nil, fmt.Errorf("applyTerminalInfraError: %w", err)
		}
	}

	post := func(ctx context.Context) {
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			// @concept: signal
			infraSig := signalpkg.Signal{
				Type: signalpkg.TypePath("terminal/infra/" + errorClass),
				Payload: map[string]any{
					"reason":  errorClass,
					"details": payload,
				},
			}
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

func requiredStoresForAcq(acq *acquisition) []string {
	if acq == nil || acq.NodeDef == nil {
		return nil
	}
	return node.RequiredStores(*acq.NodeDef)
}

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

func shouldForceRetryLoopGiveUp(ctx context.Context, args RunArgs, acq *acquisition) bool {
	count, override, err := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	if err != nil {
		return false
	}
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
	return count >= maxRetries
}
