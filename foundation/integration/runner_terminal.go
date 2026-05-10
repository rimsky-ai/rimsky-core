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

	"github.com/fallguy/rimsky/foundation/cascade"
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
	// Persist any NamedEvent emissions captured during the dispatch's
	// gRPC stream BEFORE applying the terminal verdict, per plan H1.
	// Failures here are best-effort and logged — events that fail to
	// persist do not block the terminal verdict.
	if len(t.NamedEvents) > 0 {
		processNamedEvents(ctx, args, acq, t.NamedEvents)
	}
	// Plan I2: record the terminal verdict by class + error_class.
	metricsOf(args).IncTerminal(string(terminalClassFor(t.Kind)), t.ErrorClass)
	switch t.Kind {
	case terminalKindComplete:
		return applyTerminalComplete(ctx, args, acq, resolvedAttrs, schema, t)
	case terminalKindBlocked:
		return applyTerminalBlockedOrErrored(ctx, args, acq, t.ErrorClass, t.Payload, "blocked")
	case terminalKindErrored:
		return applyTerminalBlockedOrErrored(ctx, args, acq, t.ErrorClass, t.Payload, "errored")
	case terminalKindInfra:
		return applyTerminalInfraError(ctx, args, acq, t.ErrorClass, t.Payload)
	case terminalKindPark:
		return applyTerminalPark(ctx, args, acq, t)
	}
	return fmt.Errorf("applyTerminal: unhandled terminal kind %v", t.Kind)
}

// terminalClassFor returns the metric label for a terminal kind. Kept
// in one place so additions to the kind enum don't drift between the
// metric labeling and the dispatch switch.
func terminalClassFor(k terminalKind) string {
	switch k {
	case terminalKindComplete:
		return "complete"
	case terminalKindBlocked:
		return "blocked"
	case terminalKindErrored:
		return "errored"
	case terminalKindInfra:
		return "infra"
	case terminalKindPark:
		return "park"
	}
	return "unknown"
}

// applyTerminalBlockedOrErrored / applyTerminalPass live in
// runner_terminal_handlers.go (split out for cold-read 500-line file
// guideline compliance).

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

	// Resolve the on_executor_complete handler. Default = by_changed
	// (today's behavior).
	resolve := node.ResolveByChanged
	var completeHandler *node.OnExecutorCompleteHandler
	if acq.NodeDef != nil && acq.NodeDef.OnExecutorComplete != nil {
		completeHandler = acq.NodeDef.OnExecutorComplete
		if completeHandler.Resolve != "" {
			resolve = completeHandler.Resolve
		}
	}
	var lastOutcome shared.LastOutcome
	switch resolve {
	case node.ResolveByChanged:
		if t.Changed {
			lastOutcome = shared.LastOutcomeFreshChanged
		} else {
			lastOutcome = shared.LastOutcomeFreshUnchanged
		}
	case node.ResolveAlwaysPropagate:
		lastOutcome = shared.LastOutcomeFreshChanged
	case node.ResolveNeverPropagate:
		lastOutcome = shared.LastOutcomeFreshUnchanged
	default:
		// Validator should have caught this, but defensive fallback.
		if t.Changed {
			lastOutcome = shared.LastOutcomeFreshChanged
		} else {
			lastOutcome = shared.LastOutcomeFreshUnchanged
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
		// running → fresh via the on_executor_complete handler.
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
			shared.NodeStateFresh, cascade.ReasonHandlerComplete, lastOutcome, tx); err != nil {
			return err
		}
		// Cascade-firing gate: now expressed as last_outcome ==
		// fresh_changed instead of t.Changed directly. Functionally
		// identical under default by_changed; diverges under
		// always_propagate (cascade fires even when t.Changed=false)
		// and never_propagate (cascade does NOT fire even when
		// t.Changed=true).
		if lastOutcome == shared.LastOutcomeFreshChanged {
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
				"last_outcome":   string(lastOutcome),
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
				"last_outcome":   string(lastOutcome),
			},
		}, tx)
	})
	if lastOutcome == shared.LastOutcomeFreshChanged {
		fanoutRecalculate(ctx, args, acq)
	}
	// Fire the optional handler.invalidate emit unconditionally (per
	// spec §3.5: invalidate emits are orthogonal to resolve).
	//
	// For frame: in: the running-tx above committed state→fresh which
	// cleared the source row's frame_id (defensive guard in
	// nodes.UpdateState). Pass acq.FrameID explicitly so
	// invalidateInFrame doesn't fall back to next-frame on the now-
	// cleared source row. Per spec §5.2 "single frame for the entire
	// drain" of an in-frame self-invalidate loop.
	if completeHandler != nil && completeHandler.Invalidate != nil {
		frameID := acq.FrameID
		emitHandlerInvalidate(ctx, args, acq.NodeID, acq.NodeType, acq.InstanceID, &frameID, completeHandler.Invalidate)
	}
	return nil
}

// emitQualityRuleFailures appends one quality_rule_failed event per
// failure entry. Called from the Complete branch when the merged
// attribute object fails one or more rules. Opens a single
// `Persist.Transaction(...)` for the whole batch — the caller is OUT-
// SIDE any open tx so a fresh tx is safe; batching keeps the per-
// failure rows in one atomic append. (Mirror of emitLockReleased's
// tx-required pattern: the inner Append uses the just-opened tx, never
// a nil tx.)
func emitQualityRuleFailures(
	ctx context.Context, args RunArgs, acq *acquisition, errs []qualityrule.Failure,
) {
	if len(errs) == 0 {
		return
	}
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, qe := range errs {
			if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "quality_rule_failed",
				Payload: map[string]any{
					"rule_type":   qe.RuleType,
					"rule_config": qe.Config,
					"severity":    string(qe.Severity),
					"details":     qe.Details,
				},
			}, tx); err != nil {
				return err
			}
		}
		return nil
	})
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

// Error-resolution branch functions (applyTerminalAppError,
// applyResolvedAction, applyTerminalInfraError, lookupPolicyForNode,
// requiredStoresForAcq, invalidateTargets) live in
// runner_terminal_errors.go. Release-path functions
// (releaseLocksInTx, releaseAcquiredLock, releaseClaim,
// releaseInheritedClaimsInTx, releaseActionString,
// emitLockReleased) live in runner_terminal_release.go. Both files
// were split out of runner_terminal.go to keep that file under the
// cold-read 500-line guideline.

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
