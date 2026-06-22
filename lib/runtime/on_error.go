// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"

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

// @concept: error-policy
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

	resolved := node.Evaluate(policy, node.EvaluatorState{}, args.ErrorClass, nil)
	resolutionSig := errorPolicySignal(args.ErrorClass, args.Payload, nil, resolved.Kind,
		resolved.NewState.RetryCounter, resolved.DelayMs)

	switch resolved.Kind {
	case "pass":
		return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cur, err := sb.Nodes().Get(ctx, args.NodeID, tx)
			if err != nil {
				return err
			}
			if cur == nil {
				return nil
			}
			var senderRunID, senderFrameID shared.UUID
			if cur.FrameID != nil {
				if runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, args.NodeID, args.RunScopeID); err != nil {
					return err
				} else if ok {
					senderRunID, senderFrameID = runID, *cur.FrameID
				}
			}
			passSig := "terminal/error/" + args.ErrorClass
			latest, err := sb.Nodes().GetLatestRunInScope(ctx, tx, args.NodeID, args.RunScopeID)
			if err != nil {
				return err
			}
			var curState cascade.NodeState
			if latest != nil {
				curState = latest.State
			}
			switch curState {
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
				return fmt.Errorf("OnError pass branch: unexpected latest run state %q for node %s (expected stale|running)",
					curState, args.NodeID)
			}
			runArgs := RunArgs{Persist: sb, Queue: args.Queue, Logger: log, Clock: args.Clock}
			if err := emitSignalInTxOnce(ctx, runArgs, tx,
				args.NodeID, cur.NodeType, senderRunID, args.InstanceID, senderFrameID,
				resolutionSig); err != nil {
				return err
			}
			if senderRunID != (shared.UUID{}) {
				if err := drainWaitSetOnSettled(ctx, runArgs, tx, senderFrameID, senderRunID); err != nil {
					return err
				}
			}
			return args.Queue.RemoveForNodeInTx(ctx, args.NodeID, args.RunScopeID, args.SupervisorID, tx)
		})

	default:
		return sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cur, err := sb.Nodes().Get(ctx, args.NodeID, tx)
			if err != nil {
				return err
			}
			if cur == nil {
				return nil
			}
			var senderRunID, senderFrameID shared.UUID
			if cur.FrameID != nil {
				if runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, args.NodeID, args.RunScopeID); err != nil {
					return err
				} else if ok {
					senderRunID, senderFrameID = runID, *cur.FrameID
				}
			}
			giveUpSig := "terminal/error/" + args.ErrorClass
			if err := sb.Nodes().UpdateState(ctx, args.NodeID, args.RunScopeID, cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &giveUpSig, tx); err != nil {
				return err
			}
			runArgs := RunArgs{Persist: sb, Queue: args.Queue, Logger: log, Clock: args.Clock}
			if err := emitSignalInTxOnce(ctx, runArgs, tx,
				args.NodeID, cur.NodeType, senderRunID, args.InstanceID, senderFrameID,
				resolutionSig); err != nil {
				return err
			}
			if senderRunID != (shared.UUID{}) {
				if err := drainWaitSetOnSettled(ctx, runArgs, tx, senderFrameID, senderRunID); err != nil {
					return err
				}
			}
			return args.Queue.RemoveForNodeInTx(ctx, args.NodeID, args.RunScopeID, args.SupervisorID, tx)
		})
	}
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
