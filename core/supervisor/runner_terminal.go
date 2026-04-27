// Spec §17.1 step 6 + §13.6 — terminal-event handling under
// stores-redesign-v2.
//
// Branches per terminal kind:
//
//   - Complete{changed: true}  → validate attributes, run quality rules,
//                                 fire per-claim release path (held vs.
//                                 non-held branches per §13.6),
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

package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/attributes"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
)

// applyTerminal is the §17.1 step 6 entry point.
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

// applyTerminalComplete runs the §13.6 success-branch release tx
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
			_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "attributes_schema_failed",
				Payload: map[string]any{
					"errors": []map[string]any{{"message": err.Error()}},
				},
			}, nil)
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

	tx, err := args.QueuePool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("applyTerminalComplete: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	stx := pgstorage.WrapPgxTx(tx)

	if err := releaseLocksInTx(ctx, args, tx, acq, true); err != nil {
		return err
	}
	if err := upsertFinalAttributesTx(ctx, args, stx, acq, merged); err != nil {
		return fmt.Errorf("applyTerminalComplete: upsert attributes: %w", err)
	}
	if err := args.Storage.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{}, stx); err != nil {
		return fmt.Errorf("applyTerminalComplete: clear error state: %w", err)
	}
	if err := args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
		shared.NodeStateFresh, node.ReasonWorkCompleted, stx); err != nil {
		return err
	}
	if t.Changed {
		if err := cascadeChildrenStaleInTx(ctx, args, tx, acq); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("applyTerminalComplete: commit tx: %w", err)
	}
	committed = true

	commitKind := "attributes_committed"
	if !t.Changed {
		commitKind = "no_op_commit"
	}
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: commitKind,
		Payload: map[string]any{
			"changed":        t.Changed,
			"updated_fields": fieldNames(t.AttributesDel),
			"change_summary": t.ChangeSummary,
		},
	}, nil)
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "work_completed",
		Payload: map[string]any{
			"outcome":        outcomeForChanged(t.Changed),
			"change_summary": t.ChangeSummary,
			"node_type":      acq.NodeType,
		},
	}, nil)
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
		_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "quality_rule_failed",
			Payload: map[string]any{
				"rule_type":   qe.RuleType,
				"rule_config": qe.Config,
				"severity":    string(qe.Severity),
				"details":     qe.Details,
			},
		}, nil)
	}
}

// cascadeChildrenStaleInTx marks dependent nodes stale + frame_id in
// the same tx as the parent's commit so the next scheduler tick sees
// the cascade atomically. Skipped on no-op commits per §4.4.
func cascadeChildrenStaleInTx(
	ctx context.Context, args RunArgs, tx pgx.Tx, acq *acquisition,
) error {
	stx := pgstorage.WrapPgxTx(tx)
	dependents, err := args.Storage.Nodes().ListDependentsOf(ctx, acq.NodeID, stx)
	if err != nil {
		return fmt.Errorf("cascadeChildrenStaleInTx: list dependents: %w", err)
	}
	for _, dep := range dependents {
		_, err := tx.Exec(ctx, `
			UPDATE rimsky_nodes
			SET state = 'stale', frame_id = $1, updated_at = now()
			WHERE id = $2
			  AND (state = 'fresh' OR (state = 'stale' AND frame_id IS NULL))
		`, acq.FrameID, dep.ID)
		if err != nil {
			return fmt.Errorf("cascadeChildrenStaleInTx: dep %s: %w", dep.ID, err)
		}
	}
	return nil
}

// fanoutRecalculate routes scheduler.RecalculateNode at each
// dependent post-commit. Walks ListDependentsOf a second time; the
// in-tx walk in cascadeChildrenStaleInTx mutates child state, this
// post-commit walk routes the recalculate event.
func fanoutRecalculate(ctx context.Context, args RunArgs, acq *acquisition) {
	dependents, err := args.Storage.Nodes().ListDependentsOf(ctx, acq.NodeID, nil)
	if err != nil {
		return
	}
	src := acq.NodeID
	for _, dep := range dependents {
		_ = scheduler.RecalculateNode(ctx, scheduler.RecalculateArgs{
			Storage:      args.Storage,
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
	prior, _ := args.Storage.Nodes().Get(ctx, acq.NodeID, nil)
	if prior != nil {
		state = node.EvaluatorState{
			ActionIndex:       prior.ActionIndex,
			RetryCounter:      prior.RetryCounter,
			CurrentErrorClass: prior.CurrentErrorClass,
		}
	}
	resolved := node.Evaluate(policy, state, errorClass, nil)

	tx, err := args.QueuePool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("applyTerminalAppError: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	stx := pgstorage.WrapPgxTx(tx)

	if err := args.Storage.Nodes().UpdateError(ctx, acq.NodeID, resolved.NewState, stx); err != nil {
		return err
	}
	if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
		return err
	}
	if err := applyResolvedAction(ctx, args, tx, stx, acq, prior, resolved); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("applyTerminalAppError: commit: %w", err)
	}
	committed = true

	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "error",
		Payload: map[string]any{
			"error_class":  errorClass,
			"details":      payload,
			"action_taken": resolved.Kind,
			"action_index": resolved.NewState.ActionIndex,
			"delay_ms":     resolved.DelayMs,
		},
	}, nil)
	if resolved.Kind == "invalidate" {
		return invalidateTargets(ctx, args, acq, resolved.Targets)
	}
	return nil
}

// applyResolvedAction wraps the per-policy action SQL (state update,
// queue mutation) so applyTerminalAppError stays inside the cold-read
// 100-line guideline.
func applyResolvedAction(
	ctx context.Context, args RunArgs, tx pgx.Tx, stx storage.Tx,
	acq *acquisition, prior *storage.NodeRow, resolved node.ResolvedAction,
) error {
	switch resolved.Kind {
	case "retry", "discard_then_retry", "resume_then_retry":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateStale, node.ReasonPolicyRetry, stx); err != nil {
				return err
			}
		}
		if err := removeDispatchForNodeInTx(ctx, tx, acq.NodeID, args.SupervisorID); err != nil {
			return err
		}
		return enqueueInTx(ctx, tx, queue.DispatchRequest{
			NodeID:         acq.NodeID,
			ExecutorName:   acq.Executor,
			RequiredStores: requiredStoresForAcq(acq),
			EnqueuedAt:     args.Clock.Now().Add(time.Duration(resolved.DelayMs) * time.Millisecond),
			FrameID:        acq.FrameID,
		})
	case "invalidate":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateStale, node.ReasonPolicyInvalidate, stx); err != nil {
				return err
			}
		}
		return removeDispatchForNodeInTx(ctx, tx, acq.NodeID, args.SupervisorID)
	case "give_up":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateFailed, node.ReasonPolicyGiveUp, stx); err != nil {
				return err
			}
		}
		return removeDispatchForNodeInTx(ctx, tx, acq.NodeID, args.SupervisorID)
	}
	return nil
}

// enqueueInTx mirrors core/queue/postgres/queue.go::Enqueue but runs
// on the supplied tx so the §13.6 release tx + state update +
// re-enqueue are one atomic step.
// @source: core/queue/postgres/queue.go:Enqueue
func enqueueInTx(ctx context.Context, tx pgx.Tx, req queue.DispatchRequest) error {
	stores := req.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	var executor any
	if req.ExecutorName != "" {
		executor = req.ExecutorName
	}
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("enqueueInTx: frame_id required (per blessed-invariant 19) for node %s", req.NodeID)
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO rimsky_dispatch (id, node_id, executor_name, required_stores, enqueued_at, frame_id)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
		 ON CONFLICT (node_id) DO UPDATE
		   SET enqueued_at = EXCLUDED.enqueued_at,
		       executor_name = EXCLUDED.executor_name,
		       required_stores = EXCLUDED.required_stores,
		       frame_id = EXCLUDED.frame_id
		   WHERE rimsky_dispatch.claimed_by IS NULL
		     AND rimsky_dispatch.enqueued_at <= NOW()`,
		req.NodeID, executor, stores, req.EnqueuedAt, req.FrameID,
	)
	if err != nil {
		return fmt.Errorf("enqueueInTx: %w", err)
	}
	return nil
}

// removeDispatchForNodeInTx mirrors
// core/queue/postgres/queue.go::RemoveForNode but runs on the
// supplied tx.
// @source: core/queue/postgres/queue.go:RemoveForNode
func removeDispatchForNodeInTx(
	ctx context.Context, tx pgx.Tx, nodeID shared.UUID, expectedClaimedBy string,
) error {
	if expectedClaimedBy != "" {
		_, err := tx.Exec(ctx,
			`DELETE FROM rimsky_dispatch WHERE node_id = $1 AND claimed_by = $2`,
			nodeID, expectedClaimedBy,
		)
		if err != nil {
			return fmt.Errorf("removeDispatchForNodeInTx: %w", err)
		}
		return nil
	}
	_, err := tx.Exec(ctx, `DELETE FROM rimsky_dispatch WHERE node_id = $1`, nodeID)
	if err != nil {
		return fmt.Errorf("removeDispatchForNodeInTx: %w", err)
	}
	return nil
}

// applyTerminalInfraError is the infra_reenqueue path. State→stale,
// failure-branch release, re-enqueue with no retry bump. Single tx.
func applyTerminalInfraError(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any,
) error {
	prior, _ := args.Storage.Nodes().Get(ctx, acq.NodeID, nil)
	tx, err := args.QueuePool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("applyTerminalInfraError: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	stx := pgstorage.WrapPgxTx(tx)

	if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
		return err
	}
	if prior != nil && prior.State == shared.NodeStateRunning {
		if err := args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
			shared.NodeStateStale, node.ReasonInfraReenqueue, stx); err != nil {
			return err
		}
	}
	if err := removeDispatchForNodeInTx(ctx, tx, acq.NodeID, args.SupervisorID); err != nil {
		return err
	}
	if err := enqueueInTx(ctx, tx, queue.DispatchRequest{
		NodeID:         acq.NodeID,
		ExecutorName:   acq.Executor,
		RequiredStores: requiredStoresForAcq(acq),
		EnqueuedAt:     args.Clock.Now(),
		FrameID:        acq.FrameID,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("applyTerminalInfraError: commit: %w", err)
	}
	committed = true

	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "error",
		Payload: map[string]any{
			"error_class":  errorClass,
			"details":      payload,
			"action_taken": "infra_reenqueue",
		},
	}, nil)
	return nil
}

// releaseLocksInTx is the §13.6 release-tx body. Walks the
// acquired-locks slice in §13.7 sort order. For each lock:
//
//   - NamedLockSpec → claimant-guarded delete.
//   - ClaimSpec acquirer + held → mark this node's claim_holders row
//     'completed'/'failed', call CheckAndFireResolution.
//   - ClaimSpec acquirer + non-held → call substrate verb (Commit /
//     Abandon / Delete / release_to_*) per claim_resolutions, delete
//     the lock-holder row.
//
// The inheritor branch is handled by releaseInheritedClaimsInTx, run
// from the same tx.
func releaseLocksInTx(
	ctx context.Context, args RunArgs, tx pgx.Tx, acq *acquisition, success bool,
) error {
	storeCtx := store.WithTx(ctx, tx)
	for _, lk := range acq.Locks {
		if err := releaseAcquiredLock(storeCtx, args, tx, acq, lk, success); err != nil {
			return err
		}
	}
	return releaseInheritedClaimsInTx(ctx, args, tx, acq, success)
}

// releaseAcquiredLock dispatches one acquired lock to the right
// release branch.
func releaseAcquiredLock(
	storeCtx context.Context, args RunArgs, tx pgx.Tx,
	acq *acquisition, lk AcquiredLock, success bool,
) error {
	switch sp := lk.Spec.(type) {
	case store.NamedLockSpec:
		_ = sp
		if err := args.LockHolders.DeleteByID(storeCtx, tx, lk.LockHolderID, args.SupervisorID); err != nil {
			return fmt.Errorf("releaseAcquiredLock: named DeleteByID: %w", err)
		}
		emitLockReleased(storeCtx, args, acq, lk, releaseActionString(success))
		return nil
	case store.ClaimSpec:
		return releaseClaim(storeCtx, args, tx, acq, lk, sp, success)
	}
	return fmt.Errorf("releaseAcquiredLock: unknown spec %T", lk.Spec)
}

// releaseClaim handles the per-ClaimSpec release-path branching
// (held vs. non-held). For non-held claims, region and address are
// read from the lock-holder row (spec §13.6) so the substrate verb
// receives the canonical bytes regardless of whether `lk.ClaimResult`
// survived an async-callback round-trip.
func releaseClaim(
	storeCtx context.Context, args RunArgs, tx pgx.Tx,
	acq *acquisition, lk AcquiredLock, spec store.ClaimSpec, success bool,
) error {
	held := isAliasHeld(acq.HeldSubgraphs, acq.NodeType, spec.Alias)
	if held {
		if err := markClaimHolderForNode(storeCtx, args, tx, lk.LockHolderID, acq.NodeID, success); err != nil {
			return err
		}
		if err := CheckAndFireResolution(storeCtx, args, tx,
			lk.LockHolderID, spec.Alias, claimResolutionsForAcq(acq)); err != nil {
			return err
		}
		emitLockReleased(storeCtx, args, acq, lk, "held_marked")
		return nil
	}
	resolution := resolutionForAlias(acq.NodeDef, spec.Alias)
	verbAction, _ := selectResolutionAction(resolution, success)
	region, address, err := loadLockHolderRegionAndAddress(storeCtx, tx, lk.LockHolderID)
	if err != nil {
		return fmt.Errorf("releaseClaim: load region/address: %w", err)
	}
	if err := fireResolutionVerb(storeCtx, lk.Store, verbAction, success, region, address); err != nil {
		return fmt.Errorf("releaseClaim: substrate verb (%s): %w", verbAction, err)
	}
	if err := args.LockHolders.DeleteByID(storeCtx, tx, lk.LockHolderID, args.SupervisorID); err != nil {
		return fmt.Errorf("releaseClaim: DeleteByID: %w", err)
	}
	emitLockReleased(storeCtx, args, acq, lk, verbAction)
	return nil
}

// loadLockHolderRegionAndAddress reads region_data and address from the
// lock-holder row inside the supplied tx. Returns (nil, nil, nil) when
// the row is gone — the caller treats that as a substrate no-op (the
// row may have been auto-terminated by a sibling on a held subgraph).
func loadLockHolderRegionAndAddress(ctx context.Context, tx pgx.Tx, id shared.UUID) ([]byte, []byte, error) {
	var (
		region  []byte
		address []byte
	)
	err := tx.QueryRow(ctx,
		`SELECT region_data, address FROM rimsky_lock_holders WHERE id = $1`, id,
	).Scan(&region, &address)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return region, address, nil
}

// releaseInheritedClaimsInTx walks the precomputed holding-subgraph
// metadata and, for each subgraph this node is a non-acquirer
// member of, marks the inheritor's claim_holders row and calls
// CheckAndFireResolution. The auto-terminal mechanism handles the
// substrate verb.
func releaseInheritedClaimsInTx(
	ctx context.Context, args RunArgs, tx pgx.Tx, acq *acquisition, success bool,
) error {
	inherited, err := findInheritedAliasesForNode(ctx, args, acq.HeldSubgraphs, acq.NodeType, acq.NodeID, acq.InstanceID)
	if err != nil {
		return err
	}
	for _, ia := range inherited {
		if err := markClaimHolderForNode(ctx, args, tx, ia.LockHolderID, acq.NodeID, success); err != nil {
			return err
		}
		resolution, err := resolutionForAcquirerNode(ctx, args, acq.InstanceID, ia.AcquirerType, ia.Alias)
		if err != nil {
			return err
		}
		// The auto-terminal lookup needs the per-alias resolution
		// keyed by alias; fake a single-entry map for this call.
		resolutions := map[string]node.ClaimResolution{ia.Alias: resolution}
		if err := CheckAndFireResolution(ctx, args, tx, ia.LockHolderID, ia.Alias, resolutions); err != nil {
			return err
		}
	}
	return nil
}

// claimResolutionsForAcq returns the acquirer's per-alias resolution
// map. Empty when NodeDef is nil.
func claimResolutionsForAcq(acq *acquisition) map[string]node.ClaimResolution {
	if acq == nil || acq.NodeDef == nil {
		return nil
	}
	return acq.NodeDef.ClaimResolutions
}

// releaseActionString maps success bool → event payload string.
// Named locks have no substrate verb so we synthesize "release" /
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
	ctx context.Context, args RunArgs, stx storage.Tx, acq *acquisition, merged map[string]any,
) error {
	prior, _ := args.Storage.NodeAttributes().Get(ctx, acq.NodeID)
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
	return upsertNodeAttributesInTx(ctx, stx, acq.NodeID, attempt, final)
}

// upsertNodeAttributesInTx writes a node_attributes row inside the
// supplied storage.Tx.
// @source: core/storage/postgres/node_attributes.go:Upsert
func upsertNodeAttributesInTx(
	ctx context.Context, stx storage.Tx, nodeID shared.UUID,
	runAttempt int, data map[string]any,
) error {
	if data == nil {
		data = map[string]any{}
	}
	pgT, err := pgstorage.PgxTxFromStorage(stx)
	if err != nil {
		return fmt.Errorf("upsertNodeAttributesInTx: %w", err)
	}
	if pgT == nil {
		return fmt.Errorf("upsertNodeAttributesInTx: stx is nil — caller must pass an active tx")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("upsertNodeAttributesInTx: marshal: %w", err)
	}
	_, err = pgT.Exec(ctx,
		`INSERT INTO rimsky_node_attributes (node_id, run_attempt, data, updated_at)
		 VALUES ($1, $2, $3::jsonb, now())
		 ON CONFLICT (node_id) DO UPDATE
		   SET run_attempt = EXCLUDED.run_attempt,
		       data        = EXCLUDED.data,
		       updated_at  = now()`,
		nodeID, runAttempt, raw,
	)
	if err != nil {
		return fmt.Errorf("upsertNodeAttributesInTx: %w", err)
	}
	return nil
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
	case store.NamedLockSpec:
		payload["lock_kind"] = string(store.LockHolderKindNamed)
		payload["lock_name"] = sp.Name
	case store.ClaimSpec:
		payload["lock_kind"] = string(store.LockHolderKindRegion)
		payload["store_name"] = sp.StoreName
		payload["alias"] = sp.Alias
	}
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "lock_released", Payload: payload,
	}, nil)
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
// IDs in the same instance and routes scheduler.InvalidateNode to
// each.
func invalidateTargets(
	ctx context.Context, args RunArgs, acq *acquisition, targets []string,
) error {
	other, err := args.Storage.Nodes().ListByInstance(ctx, acq.InstanceID, nil)
	if err != nil {
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
		_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "unresolved_invalidate_target",
			Payload: map[string]any{
				"unresolved_targets": unresolved,
			},
		}, nil)
	}
	src := acq.NodeID
	for _, tid := range resolved {
		_ = scheduler.InvalidateNode(ctx, scheduler.InvalidateArgs{
			Storage: args.Storage, Queue: args.Queue,
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
	errs, _, err := qualityrule.EvaluateAll(context.Background(), rules,
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
