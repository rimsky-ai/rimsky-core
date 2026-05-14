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
// with their helpers (`lookupPolicyForNode`, `requiredStoresForAcq`,
// `invalidateTargets`). Split out of runner_terminal.go to keep that
// file under the cold-read 500-line guideline; the Success branch
// stays there because it interleaves with attribute upsert + cascade
// fan-out and reads better in one place.
//
// Per spec §7.6 / §4.10 invariant 13.

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
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
// (retry / discard_then_retry / resume_then_retry) AND the per-row
// consecutive_retries_no_progress counter (after increment) exceeds
// the effective max-retries-without-progress cap, the runner forces an
// Error{error_class:"retry_loop_no_progress"} verdict instead of
// retrying. Per plan E5.
func applyErrorPolicy(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any,
) error {
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
	// E5 counter housekeeping: if the resolved action is a retry, bump
	// the per-row counter; otherwise reset it. The next terminal that
	// produces a different last_outcome change will reset via the
	// applyTerminalComplete path's last-outcome-change detection (the
	// counter only tracks consecutive retries with no last_outcome
	// progress).
	// E5 counter housekeeping: capture the prior dispatch row's
	// counter so we can carry it forward across the retry round-trip.
	// applyResolvedAction's retry branch removes the old row and
	// inserts a new one — without this carry-forward the counter
	// would reset to zero on every retry and the cap would never
	// trigger.
	priorCount, _, _ := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	var carryForwardCount int
	switch resolved.Kind {
	case "retry", "discard_then_retry", "resume_then_retry":
		carryForwardCount = priorCount + 1
	default:
		carryForwardCount = 0
	}

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
		// On retry, re-stamp the counter onto the freshly-inserted
		// dispatch row so the next iteration's
		// shouldForceRetryLoopGiveUp can see accumulated retries.
		switch resolved.Kind {
		case "retry", "discard_then_retry", "resume_then_retry":
			return args.Queue.SetRetryNoProgressForNodeInTx(ctx, tx, acq.NodeID, carryForwardCount)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("applyErrorPolicy: %w", err)
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
// queue mutation) so applyErrorPolicy stays inside the cold-read
// 100-line guideline.
func applyResolvedAction(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, prior *persistence.NodeRow, resolved node.ResolvedAction,
) error {
	switch resolved.Kind {
	case "retry", "discard_then_retry", "resume_then_retry":
		if prior != nil && prior.State == cascade.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				cascade.NodeStateStale, cascade.ReasonPolicyRetry, "", tx); err != nil {
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
		if prior != nil && prior.State == cascade.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				cascade.NodeStateStale, cascade.ReasonPolicyInvalidate, "", tx); err != nil {
				return err
			}
		}
		return args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx)
	case "give_up":
		if prior != nil && prior.State == cascade.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, cascade.LastOutcomeFailed, tx); err != nil {
				return err
			}
		}
		return args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx)
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
	errorClass string, payload map[string]any,
) error {
	var prior *persistence.NodeRow
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := args.Persist.Nodes().Get(ctx, acq.NodeID, tx)
		prior = p
		return err
	})
	// Read the current counter so we can carry it forward onto the
	// freshly-inserted dispatch row.
	priorCount, _, _ := args.Queue.GetRetryNoProgress(ctx, acq.DispatchID)
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
			return err
		}
		if prior != nil && prior.State == cascade.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				cascade.NodeStateStale, cascade.ReasonInfraReenqueue, "", tx); err != nil {
				return err
			}
		}
		if err := args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx); err != nil {
			return err
		}
		if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         acq.NodeID,
			ExecutorName:   acq.Executor,
			RequiredStores: requiredStoresForAcq(acq),
			EnqueuedAt:     args.Clock.Now(),
			FrameID:        acq.FrameID,
		}, tx); err != nil {
			return err
		}
		// Re-stamp the counter on the freshly-inserted dispatch row so
		// the infra round-trip preserves cap-eligibility.
		return args.Queue.SetRetryNoProgressForNodeInTx(ctx, tx, acq.NodeID, priorCount)
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
			Metrics:      args.Metrics,
		})
	}
	return nil
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
