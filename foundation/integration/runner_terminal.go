// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Terminal-event handling under the stores redesign — release path
// (§7.6 / §4.10 invariant 13 auto-terminal).
//
// Branches per terminal kind:
//
//   - Complete{changed: true}  → validate attributes, run quality rules,
//                                 fire per-claim release path (held vs.
//                                 non-held branches per §7.6),
//                                 persist final attributes, state→fresh,
//                                 emit `attributes_committed`,
//                                 cascade message-pass on dependents.
//   - Complete{changed: false} → as above; emit `no_op_commit`; no
//                                 cascade.
//   - Blocked / Errored        → policy chain: discard_then_retry |
//                                 give_up | invalidate(targets). All
//                                 release through the failure branch
//                                 (Abandon for non-held; mark
//                                 'failed' + auto-terminal for held).
//   - Infra error              → infra_reenqueue: state→stale, failure-
//                                 branch release, re-enqueue without
//                                 retry bump.

package integration

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/attribute"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/qualityrule"
	qreval "github.com/fallguy/rimsky/modeling/qualityrule/eval"
	"github.com/fallguy/rimsky/modeling/shared"
)

// applyTerminal is the omnibus runner's terminal-event entry point.
func applyTerminal(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent,
) error {
	switch t.Kind {
	case terminalKindComplete:
		return applyTerminalComplete(ctx, args, acq, resolvedAttrs, schema, t)
	case terminalKindBlocked, terminalKindErrored:
		return applyTerminalAppError(ctx, args, acq, t.ErrorClass, t.Payload)
	case terminalKindInfra:
		return applyTerminalInfraError(ctx, args, acq, t.ErrorClass, t.Payload)
	}
	return fmt.Errorf("applyTerminal: unhandled terminal kind %v", t.Kind)
}

// applyTerminalComplete runs the §7.6 success-branch release tx
// alongside the state→fresh transition, final attribute upsert, and
// cascade message-pass to dependents.
func applyTerminalComplete(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent,
) error {
	merged := mergeAttributesDelta(resolvedAttrs, t.AttributesDel)
	if t.Changed && len(t.AttributesDel) > 0 && schema != nil {
		if err := attributes.Validate(schema, merged, attributes.PhaseCommit); err != nil {
			_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
					NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
					Kind: "attributes_schema_failed",
					Payload: map[string]any{
						"errors": []map[string]any{{"message": err.Error()}},
					},
				}, tx)
			})
			return applyTerminalAppError(ctx, args, acq, "attributes_schema_failed",
				map[string]any{"error": err.Error()})
		}
	}

	if acq.NodeDef != nil && len(acq.NodeDef.QualityRules) > 0 {
		if errs := runQualityRules(acq.NodeDef.QualityRules, merged); len(errs) > 0 {
			emitQualityRuleFailures(ctx, args, acq, errs)
			return applyTerminalAppError(ctx, args, acq, "quality_rule_failed",
				map[string]any{"errors": errs})
		}
	}

	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := releaseLocksInTx(ctx, args, tx, acq, true); err != nil {
			return err
		}
		if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
			return fmt.Errorf("applyTerminalComplete: upsert attributes: %w", err)
		}
		if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{}, tx); err != nil {
			return fmt.Errorf("applyTerminalComplete: clear error state: %w", err)
		}
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
			shared.NodeStateFresh, cascade.ReasonWorkCompleted, tx); err != nil {
			return err
		}
		if t.Changed {
			if err := cascadeChildrenStaleInTx(ctx, args, tx, acq); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	commitKind := "attributes_committed"
	if !t.Changed {
		commitKind = "no_op_commit"
	}
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: commitKind,
			Payload: map[string]any{
				"changed":        t.Changed,
				"updated_fields": fieldNames(t.AttributesDel),
				"change_summary": t.ChangeSummary,
			},
		}, tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "work_completed",
			Payload: map[string]any{
				"outcome":        outcomeForChanged(t.Changed),
				"change_summary": t.ChangeSummary,
				"node_type":      acq.NodeType,
			},
		}, tx)
	})
	if t.Changed {
		fanoutRecalculate(ctx, args, acq)
	}
	return nil
}

// emitQualityRuleFailures appends one quality_rule_failed event per
// failure entry. Called from the Complete branch when the merged
// attribute object fails one or more rules.
func emitQualityRuleFailures(
	ctx context.Context, args RunArgs, acq *acquisition, errs []qualityrule.Failure,
) {
	for _, qe := range errs {
		qe := qe
		_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "quality_rule_failed",
				Payload: map[string]any{
					"rule_type":   qe.RuleType,
					"rule_config": qe.Config,
					"severity":    string(qe.Severity),
					"details":     qe.Details,
				},
			}, tx)
		})
	}
}

// cascadeChildrenStaleInTx marks dependent nodes stale + frame_id in
// the same tx as the parent's commit so the next scheduler tick sees
// the cascade atomically. Skipped on no-op commits per §4.4.
func cascadeChildrenStaleInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition,
) error {
	dependents, err := args.Persist.Nodes().ListDependentsOf(ctx, acq.NodeID, tx)
	if err != nil {
		return fmt.Errorf("cascadeChildrenStaleInTx: list dependents: %w", err)
	}
	for _, dep := range dependents {
		if err := args.Persist.Nodes().MarkStaleForCascade(ctx, dep.ID, acq.FrameID, tx); err != nil {
			return fmt.Errorf("cascadeChildrenStaleInTx: dep %s: %w", dep.ID, err)
		}
	}
	return nil
}

// fanoutRecalculate routes RecalculateNode at each
// dependent post-commit. Walks ListDependentsOf a second time; the
// in-tx walk in cascadeChildrenStaleInTx mutates child state, this
// post-commit walk routes the recalculate event.
func fanoutRecalculate(ctx context.Context, args RunArgs, acq *acquisition) {
	var dependents []persistence.NodeRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.Persist.Nodes().ListDependentsOf(ctx, acq.NodeID, tx)
		dependents = rows
		return err
	}); err != nil {
		return
	}
	src := acq.NodeID
	for _, dep := range dependents {
		_ = RecalculateNode(ctx, RecalculateArgs{
			Persist:      args.Persist,
			Queue:        args.Queue,
			Clock:        args.Clock,
			Logger:       args.Logger,
			SourceNodeID: &src,
			TargetNodeID: dep.ID,
		})
	}
}

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
		return invalidateTargets(ctx, args, acq, resolved.Targets)
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
				shared.NodeStateStale, cascade.ReasonPolicyRetry, tx); err != nil {
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
				shared.NodeStateStale, cascade.ReasonPolicyInvalidate, tx); err != nil {
				return err
			}
		}
		return args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx)
	case "give_up":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateFailed, cascade.ReasonPolicyGiveUp, tx); err != nil {
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
				shared.NodeStateStale, cascade.ReasonInfraReenqueue, tx); err != nil {
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

// releaseLocksInTx is the release-tx body. Walks the acquired-locks
// slice in sort order. For each lock:
//
//   - NamedLockSpec → claimant-guarded delete.
//   - ClaimSpec acquirer + held → mark this node's claim_holders row
//     'completed'/'failed', call CheckAndFireResolution.
//   - ClaimSpec acquirer + non-held → call the store verb directly
//     (success → Commit; failure → Abandon), delete the lock-holder
//     row.
//
// Per spec §7.3 the store's verb runs in its own (store-side)
// transaction; rimsky's bookkeeping tx commits the lock-holder DELETE
// independently. At-least-once delivery + claim_id idempotency on the
// store side handles transient failures (per spec §7.8 obligation
// #3).
//
// The inheritor branch is handled by releaseInheritedClaimsInTx, run
// from the same tx.
func releaseLocksInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
) error {
	for _, lk := range acq.Locks {
		if err := releaseAcquiredLock(ctx, args, tx, acq, lk, success); err != nil {
			return err
		}
	}
	return releaseInheritedClaimsInTx(ctx, args, tx, acq, success)
}

// releaseAcquiredLock dispatches one acquired lock to the right
// release branch.
func releaseAcquiredLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, success bool,
) error {
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		_ = sp
		if err := args.LockHolders.Delete(ctx, lk.LockHolderID, args.SupervisorID, tx); err != nil {
			return fmt.Errorf("releaseAcquiredLock: named Delete: %w", err)
		}
		emitLockReleased(ctx, args, acq, lk, releaseActionString(success))
		return nil
	case locks.ClaimSpec:
		return releaseClaim(ctx, args, tx, acq, lk, sp, success)
	}
	return fmt.Errorf("releaseAcquiredLock: unknown spec %T", lk.Spec)
}

// releaseClaim handles the per-ClaimSpec release-path branching
// (held vs. non-held). For non-held claims, scope and address are
// read from the lock-holder row so the store verb receives the
// canonical bytes regardless of whether `lk.ClaimResult` survived an
// async-callback round-trip. Store disposition (what Commit /
// Abandon mean for the store's own state) is governed entirely
// by per-store config; rimsky carries only the success/failure
// binary.
func releaseClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, spec locks.ClaimSpec, success bool,
) error {
	held := isAliasHeld(acq.HeldSubgraphs, acq.NodeType, spec.Alias)
	if held {
		if err := markClaimHolderForNode(ctx, args, tx, lk.LockHolderID, acq.NodeID, success); err != nil {
			return err
		}
		if err := CheckAndFireResolution(ctx, args, tx, lk.LockHolderID); err != nil {
			return err
		}
		emitLockReleased(ctx, args, acq, lk, "held_marked")
		return nil
	}
	row, err := args.LockHolders.Get(ctx, lk.LockHolderID, tx)
	if err != nil {
		return fmt.Errorf("releaseClaim: load scope/address: %w", err)
	}
	var (
		scope   []byte
		address []byte
	)
	if row != nil {
		scope = []byte(row.ScopeData)
		address = []byte(row.Address)
	}
	verbAction := releaseActionString(success)
	outcome := AggregateCommit
	if !success {
		outcome = AggregateAbandon
	}
	if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID: lk.LockHolderID,
		SupervisorID:  args.SupervisorID,
		Source:        ActiveTerminal,
		Outcome:       outcome,
		Producer:      lk.Store,
		Scope:         scope,
		Address:       address,
	}); err != nil {
		return fmt.Errorf("releaseClaim: %w", err)
	}
	emitLockReleased(ctx, args, acq, lk, verbAction)
	return nil
}

// releaseInheritedClaimsInTx walks the precomputed holding-subgraph
// metadata and, for each subgraph this node is a non-acquirer
// member of, marks the inheritor's claim_holders row and calls
// CheckAndFireResolution. The auto-terminal mechanism handles the
// store verb.
func releaseInheritedClaimsInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
) error {
	inherited, err := findInheritedAliasesForNode(ctx, args, tx, acq.HeldSubgraphs, acq.NodeType, acq.NodeID, acq.InstanceID)
	if err != nil {
		return err
	}
	for _, ia := range inherited {
		if err := markClaimHolderForNode(ctx, args, tx, ia.LockHolderID, acq.NodeID, success); err != nil {
			return err
		}
		if err := CheckAndFireResolution(ctx, args, tx, ia.LockHolderID); err != nil {
			return err
		}
	}
	return nil
}

// releaseActionString maps success bool → event payload string.
// Named locks have no store verb so we synthesize "release" /
// "release_failed" labels for the audit trail.
func releaseActionString(success bool) string {
	if success {
		return "release"
	}
	return "release_failed"
}

// upsertFinalAttributesTx writes the merged-and-validated attribute
// object back inside the supplied tx. Per spec §5.7.2 the executor
// may have written incremental fields via the §12.5 callback; the
// final row is `prior.Data + merged` so those incremental writes are
// preserved.
func upsertFinalAttributesTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, merged map[string]any,
) error {
	prior, _ := args.Persist.NodeAttributes().Get(ctx, acq.NodeID, tx)
	attempt := 1
	final := merged
	if prior != nil {
		attempt = prior.RunAttempt
		if len(prior.Data) > 0 {
			combined := make(map[string]any, len(prior.Data)+len(merged))
			for k, v := range prior.Data {
				combined[k] = v
			}
			for k, v := range merged {
				combined[k] = v
			}
			final = combined
		}
	}
	if final == nil {
		final = map[string]any{}
	}
	return args.Persist.NodeAttributes().Upsert(ctx, acq.NodeID, attempt, final, tx)
}

// mergeAttributesDelta shallow-merges the executor's attributes_delta
// into the substituted attribute object.
func mergeAttributesDelta(base, delta map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(delta))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range delta {
		out[k] = v
	}
	return out
}

// emitLockReleased emits the per-spec lock_released event.
func emitLockReleased(ctx context.Context, args RunArgs, acq *acquisition, lk AcquiredLock, action string) {
	payload := map[string]any{
		"holder_id":     lk.LockHolderID.String(),
		"supervisor_id": args.SupervisorID,
		"action":        action,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload["lock_kind"] = string(persistence.LockKindNamed)
		payload["lock_name"] = sp.Name
	case locks.ClaimSpec:
		payload["lock_kind"] = string(persistence.LockKindScope)
		payload["store_name"] = sp.StoreName
		payload["alias"] = sp.Alias
	}
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "lock_released", Payload: payload,
		}, tx)
	})
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
// each.
func invalidateTargets(
	ctx context.Context, args RunArgs, acq *acquisition, targets []string,
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
		})
	}
	return nil
}

func outcomeForChanged(changed bool) string {
	if changed {
		return "committed"
	}
	return "no_op"
}

// runQualityRules walks the per-node quality rules against a populated
// attributes object and returns the failures.
func runQualityRules(rules []qualityrule.Spec, attrs map[string]any) []qualityrule.Failure {
	if len(rules) == 0 {
		return nil
	}
	errs, _, err := qreval.EvaluateAll(context.Background(), rules,
		qualityrule.EvalInput{NewData: attrs})
	if err != nil {
		return []qualityrule.Failure{{
			RuleType: "evaluation_error",
			Severity: shared.SeverityError,
			Details:  err.Error(),
		}}
	}
	return errs
}
