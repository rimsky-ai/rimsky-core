// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Error-resolution branch of terminal-event handling — the
// policy-chain dispatch for application Blocked / Errored terminals
// (`applyTerminalAppError` + `applyResolvedAction`) and the
// infra-error branch (`applyTerminalInfraError`) together with their
// helpers (`lookupPolicyForNode`, `requiredStoresForAcq`,
// `invalidateTargets`). Split out of runner_terminal.go to keep that
// file under the cold-read 500-line guideline; the Complete branch
// stays there because it interleaves with attribute upsert + cascade
// fan-out and reads better in one place.
//
// Per spec §7.6 / §4.10 invariant 13.

package integration

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// applyTerminalAppError routes a Blocked / Errored terminal through
// the policy chain and drives release + state update + queue
// mutation in one tx.
func applyTerminalAppError(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any,
) error {
	policy, err := lookupPolicyForNode(ctx, args, acq, errorClass)
	if err != nil {
		return err
	}
	state := node.EvaluatorState{}
	var prior *persistence.NodeRow
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := args.Persist.Nodes().Get(ctx, acq.NodeID, tx)
		prior = p
		return err
	})
	if prior != nil {
		state = node.EvaluatorState{
			ActionIndex:       prior.ActionIndex,
			RetryCounter:      prior.RetryCounter,
			CurrentErrorClass: prior.CurrentErrorClass,
		}
	}
	resolved := node.Evaluate(policy, state, errorClass, nil)

	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, resolved.NewState, tx); err != nil {
			return err
		}
		if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
			return err
		}
		if err := applyResolvedAction(ctx, args, tx, acq, prior, resolved); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("applyTerminalAppError: %w", err)
	}

	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "error",
			Payload: map[string]any{
				"error_class":  errorClass,
				"details":      payload,
				"action_taken": resolved.Kind,
				"action_index": resolved.NewState.ActionIndex,
				"delay_ms":     resolved.DelayMs,
			},
		}, tx)
	})
	if resolved.Kind == "invalidate" {
		return invalidateTargets(ctx, args, acq, resolved.Targets, resolved.Frame)
	}
	return nil
}

// applyResolvedAction wraps the per-policy action SQL (state update,
// queue mutation) so applyTerminalAppError stays inside the cold-read
// 100-line guideline.
func applyResolvedAction(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, prior *persistence.NodeRow, resolved node.ResolvedAction,
) error {
	switch resolved.Kind {
	case "retry", "discard_then_retry", "resume_then_retry":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateStale, cascade.ReasonPolicyRetry, "", tx); err != nil {
				return err
			}
		}
		if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx); err != nil {
			return err
		}
		return args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         acq.NodeID,
			ExecutorName:   acq.Executor,
			RequiredStores: requiredStoresForAcq(acq),
			EnqueuedAt:     args.Clock.Now().Add(time.Duration(resolved.DelayMs) * time.Millisecond),
			FrameID:        acq.FrameID,
		}, tx)
	case "invalidate":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateStale, cascade.ReasonPolicyInvalidate, "", tx); err != nil {
				return err
			}
		}
		return args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx)
	case "give_up":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateFailed, cascade.ReasonPolicyGiveUp, shared.LastOutcomeFailed, tx); err != nil {
				return err
			}
		}
		return args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx)
	}
	return nil
}

// applyTerminalInfraError is the infra_reenqueue path. State→stale,
// failure-branch release, re-enqueue with no retry bump. Single tx.
func applyTerminalInfraError(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any,
) error {
	var prior *persistence.NodeRow
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := args.Persist.Nodes().Get(ctx, acq.NodeID, tx)
		prior = p
		return err
	})
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
			return err
		}
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateStale, cascade.ReasonInfraReenqueue, "", tx); err != nil {
				return err
			}
		}
		if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx); err != nil {
			return err
		}
		return args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         acq.NodeID,
			ExecutorName:   acq.Executor,
			RequiredStores: requiredStoresForAcq(acq),
			EnqueuedAt:     args.Clock.Now(),
			FrameID:        acq.FrameID,
		}, tx)
	}); err != nil {
		return fmt.Errorf("applyTerminalInfraError: %w", err)
	}

	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "error",
			Payload: map[string]any{
				"error_class":  errorClass,
				"details":      payload,
				"action_taken": "infra_reenqueue",
			},
		}, tx)
	})
	return nil
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

// invalidateTargets resolves the policy's target node-types to node
// IDs in the same instance and routes InvalidateNode to
// each. `frame` is the per-emit FrameIn / FrameNext setting from the
// PolicyAction; empty defaults to FrameNext at the InvalidateNode call
// site.
func invalidateTargets(
	ctx context.Context, args RunArgs, acq *acquisition, targets []string, frame string,
) error {
	var other []persistence.NodeRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.Persist.Nodes().ListByInstance(ctx, acq.InstanceID, tx)
		other = rows
		return err
	}); err != nil {
		return err
	}
	typeToID := make(map[string]shared.UUID, len(other))
	for _, o := range other {
		typeToID[o.NodeType] = o.ID
	}
	var resolved []shared.UUID
	var unresolved []string
	for _, t := range targets {
		if id, ok := typeToID[t]; ok {
			resolved = append(resolved, id)
		} else {
			unresolved = append(unresolved, t)
		}
	}
	if len(unresolved) > 0 {
		_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "unresolved_invalidate_target",
				Payload: map[string]any{
					"unresolved_targets": unresolved,
				},
			}, tx)
		})
	}
	src := acq.NodeID
	for _, tid := range resolved {
		_ = InvalidateNode(ctx, InvalidateArgs{
			Persist: args.Persist, Queue: args.Queue,
			Clock: args.Clock, Logger: args.Logger,
			SourceNodeID: &src,
			TargetNodeID: tid,
			Reason:       "policy_invalidate",
			SupervisorID: args.SupervisorID,
			Frame:        frame,
		})
	}
	return nil
}
