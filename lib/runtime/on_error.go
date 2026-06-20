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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type OnErrorArgs struct {
	Persist    persistence.Tables
	Queue      persistence.Queue
	Clock      shared.Clock
	Logger     shared.Logger
	NodeID     shared.UUID
	InstanceID shared.UUID
	// @concept: run-scope
	RunScopeID          shared.UUID
	SupervisorID        string
	ErrorClass          string
	PolicyFallbackClass string
	Payload             map[string]any
	Metrics             MetricsHook
}

func OnError(ctx context.Context, args OnErrorArgs) error {
	sb, log := args.Persist, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

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

	policy, err := lookupPolicy(ctx, sb, nd, args.ErrorClass, args.PolicyFallbackClass)
	if err != nil {
		return err
	}

	requiredClaimProducers := requiredClaimProducersForNode(ctx, sb, nd)

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

	resolutionSig := errorPolicySignal(args.ErrorClass, args.Payload, nil, resolved.Kind,
		resolved.NewState.RetryCounter, resolved.DelayMs)

	switch resolved.Kind {
	case "retry", "discard_claims_then_retry":
		return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
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
			}
			//	@concept: cascade
			//	@concept: signal
			var senderRunID shared.UUID
			if cur.InFlightRunID != nil {
				senderRunID = *cur.InFlightRunID
			}
			runArgs := RunArgs{Persist: sb, Queue: args.Queue, Logger: log, Clock: args.Clock}
			if err := emitSignalInTxOnce(ctx, runArgs, tx,
				args.NodeID, cur.NodeType, senderRunID, args.InstanceID, *cur.FrameID,
				resolutionSig); err != nil {
				return err
			}
			priorID := cur.InFlightRunID
			// @concept: executor
			var scratchInline []byte
			var scratchHandle, scratchBackend string
			if priorID != nil && *priorID != (shared.UUID{}) {
				var lerr error
				scratchInline, scratchHandle, scratchBackend, lerr = args.Queue.LoadScratchInTx(ctx, tx, *priorID)
				if lerr != nil {
					return fmt.Errorf("load prior scratch: %w", lerr)
				}
			}
			if err := args.Queue.RemoveForNodeInTx(ctx, args.NodeID, args.RunScopeID, args.SupervisorID, tx); err != nil {
				return err
			}
			if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
				NodeID:                      args.NodeID,
				ExecutorName:                cur.Executor,
				RequiredClaimProducers:      requiredClaimProducers,
				EnqueuedAt:                  args.Clock.Now().Add(time.Duration(resolved.DelayMs) * time.Millisecond),
				FrameID:                     *cur.FrameID,
				RunScopeID:                  args.RunScopeID,
				PriorDispatchID:             priorID,
				PriorDispatchDisposition:    "retry_after_error",
				InitialScratchInline:        scratchInline,
				InitialScratchHandle:        scratchHandle,
				InitialScratchHandleBackend: scratchBackend,
			}, tx); err != nil {
				if errors.Is(err, persistence.ErrRunScopeClosed) {
					log.Warn("OnError retry: skip enqueue: run scope closed",
						"node_id", args.NodeID.String(),
						"run_scope_id", args.RunScopeID.String())
					return nil
				}
				return err
			}
			return nil
		})

	case "give_up":
		return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
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
			//	@concept: cascade
			//	@concept: signal
			//	@concept: wait-set
			var senderRunID, senderFrameID shared.UUID
			if cur.FrameID != nil {
				if runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, args.NodeID, args.RunScopeID); err != nil {
					return err
				} else if ok {
					senderRunID, senderFrameID = runID, *cur.FrameID
				}
			}
			runArgs := RunArgs{Persist: sb, Queue: args.Queue, Logger: log, Clock: args.Clock}
			if err := emitSignalInTxOnce(ctx, runArgs, tx,
				args.NodeID, cur.NodeType, senderRunID, args.InstanceID, senderFrameID,
				resolutionSig); err != nil {
				return err
			}
			if senderRunID != (shared.UUID{}) {
				if err := sb.WaitSet().MarkDrainedBySender(ctx, senderFrameID, senderRunID, tx); err != nil {
					return err
				}
			}
			return args.Queue.RemoveForNodeInTx(ctx, args.NodeID, args.RunScopeID, args.SupervisorID, tx)
		})

	case "pass":
		//	@concept: error-policy
		return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cur, err := sb.Nodes().Get(ctx, args.NodeID, tx)
			if err != nil {
				return err
			}
			if cur == nil {
				return nil
			}
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
			//	@concept: cascade
			//	@concept: signal
			//	@concept: wait-set
			var senderRunID, senderFrameID shared.UUID
			if cur.FrameID != nil {
				if runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, args.NodeID, args.RunScopeID); err != nil {
					return err
				} else if ok {
					senderRunID, senderFrameID = runID, *cur.FrameID
				}
			}
			runArgs := RunArgs{Persist: sb, Queue: args.Queue, Logger: log, Clock: args.Clock}
			if err := emitSignalInTxOnce(ctx, runArgs, tx,
				args.NodeID, cur.NodeType, senderRunID, args.InstanceID, senderFrameID,
				resolutionSig); err != nil {
				return err
			}
			if senderRunID != (shared.UUID{}) {
				if err := sb.WaitSet().MarkDrainedBySender(ctx, senderFrameID, senderRunID, tx); err != nil {
					return err
				}
			}
			return args.Queue.RemoveForNodeInTx(ctx, args.NodeID, args.RunScopeID, args.SupervisorID, tx)
		})
	}
	return nil
}

func lookupPolicy(ctx context.Context, sb persistence.Tables, nd *persistence.NodeRow, errorClass, fallbackClass string) (*node.ErrorTypePolicy, error) {
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
			cp := p
			return &cp, nil
		}
		if fallbackClass != "" && fallbackClass != errorClass {
			if p, ok := td.ErrorTypes[fallbackClass]; ok {
				cp := p
				return &cp, nil
			}
		}
		return nil, nil
	}
	return nil, nil
}

func requiredClaimProducersForNode(ctx context.Context, sb persistence.Tables, nd *persistence.NodeRow) []string {
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
		return node.RequiredClaimProducers(td)
	}
	return nil
}
