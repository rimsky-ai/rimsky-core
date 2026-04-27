// Spec §17.1 step 6 + §12.6 + §13.6 — terminal-event handling.
//
// Branches per terminal kind and policy chain action:
//
//   - Complete{changed: true}  → validate attributes, run quality rules,
//                                 commit + ReleaseLock(commit) per lock,
//                                 §5.6.4 resolution for held claims,
//                                 delete-or-preserve lock-holder rows,
//                                 persist final attributes, state→fresh,
//                                 emit `attributes_committed` event.
//   - Complete{changed: false} → as above, skip validation if no
//                                 executor writeback; ReleaseLock(commit)
//                                 still runs; emit `no_op_commit` event
//                                 (preserved kind, spec §16) instead of
//                                 `attributes_committed`; no cascade.
//   - Blocked / Errored        → policy chain: discard_then_retry |
//                                 resume_then_retry | give_up |
//                                 invalidate(targets). Map each to
//                                 ReleaseLock action and node state.
//   - Infra error              → infra_reenqueue: state→stale, ReleaseLock
//                                 (give_up), re-enqueue without retry bump.

package supervisor

import (
	"context"
	"encoding/json"
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
	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

// applyTerminal is the §17.1 step 6 terminal handler entry point. The
// runner calls this after a synchronous executor RPC completes (or
// after the native dispatch path produces a synthetic Complete).
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

// applyTerminalComplete runs the §17.1 step 6c commit transaction. One
// pgx.Tx covers: per-lock store.Commit + ReleaseLock(commit), §5.6.4
// resolution for held claims, lock-holder DELETE-or-PRESERVE, final
// attributes upsert, node state→fresh.
//
// The dispatch row (rimsky_dispatch) is deleted by the supervisor's
// runLoop on a successful return; this function does NOT touch
// rimsky_dispatch directly (mirrors the existing supervisor.go
// contract).
func applyTerminalComplete(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	t terminalEvent,
) error {
	// Merge executor's attributes_delta into the resolved attribute
	// object. Schema validation runs on the merged result.
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

	// Run node-level quality rules. Failure routes through the policy
	// chain like a Blocked terminal.
	if acq.NodeDef != nil && len(acq.NodeDef.QualityRules) > 0 {
		if errs := runQualityRules(acq.NodeDef.QualityRules, merged); len(errs) > 0 {
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
	storeCtx := store.WithTx(ctx, tx)
	stx := pgstorage.WrapPgxTx(tx)
	// Spec §17.1 step 6c: per-lock work + state→fresh + final-attribute
	// persist all run inside one transaction. Locks are walked in §13.7
	// sort order (acq.Locks is the pre-sorted slice). The commit branch
	// never preserves for resume — only the policy-chain
	// `resume_then_retry` branch (in releaseLocksTx) does — so
	// resume_grace doesn't apply here.
	for _, lk := range acq.Locks {
		if lk.Store != nil {
			if _, err := lk.Store.Commit(storeCtx, lk.Handle); err != nil {
				return fmt.Errorf("applyTerminalComplete: Commit(%s): %w", lk.Handle.StoreName, err)
			}
			if err := lk.Store.ReleaseLock(storeCtx, lk.Handle, store.ReleaseCommit); err != nil {
				return fmt.Errorf("applyTerminalComplete: ReleaseLock(%s): %w", lk.Handle.StoreName, err)
			}
			// §5.6.4: claim-kind held-claim resolution. Fires only when
			// THIS node directly holds the claim (rare — typically the
			// terminal-leaf in a held chain doesn't itself acquire a
			// `claim:true` lock; it only resolves via `claim_resolutions`).
			if lk.Handle.Kind == string(store.LockHolderKindClaim) {
				if err := resolveHeldClaim(storeCtx, lk, acq.NodeID, claimstorepg.TerminalCommit); err != nil {
					return err
				}
			}
		}
		// DELETE the lock-holder row, claimant-guarded. (Preserve-for-
		// resume is not on the commit branch; the spec sets that path
		// only on policy-chain `resume_then_retry`.)
		if err := args.LockHolders.DeleteByID(ctx, tx, mustParseUUID(lk.Handle.ID), args.SupervisorID); err != nil {
			return fmt.Errorf("applyTerminalComplete: DeleteByID(%s): %w", lk.Handle.ID, err)
		}
		emitLockReleased(ctx, args, acq, lk, "commit")
	}
	// §5.6.3: when this node holds one or more `claim:true, hold:true`
	// claims, register one rimsky_claim_holders row per terminal-leaf of
	// the §11.4 holding subgraph. The held items-table row stays
	// `in_progress` until the leaves resolve via §5.6.4.
	if err := insertHeldClaimHolders(ctx, args, tx, acq); err != nil {
		return fmt.Errorf("applyTerminalComplete: insert held claim holders: %w", err)
	}
	// §5.6.4: when this node declares `claim_resolutions`, run the
	// reference-counted resolution algorithm for every active holder
	// row keyed by (this node, declared store).
	if err := resolveDeclaredClaimHolders(ctx, args, tx, acq, claimstorepg.TerminalCommit); err != nil {
		return fmt.Errorf("applyTerminalComplete: resolve declared claim holders: %w", err)
	}
	// Persist final attributes inside the tx so a partial failure
	// rolls back together with the lock release.
	if err := upsertFinalAttributesTx(ctx, args, stx, acq, merged); err != nil {
		return fmt.Errorf("applyTerminalComplete: upsert attributes: %w", err)
	}
	if err := args.Storage.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{
		ActionIndex: 0, RetryCounter: 0, CurrentErrorClass: "",
	}, stx); err != nil {
		return fmt.Errorf("applyTerminalComplete: clear error state: %w", err)
	}
	if err := args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
		shared.NodeStateFresh, node.ReasonWorkCompleted, stx); err != nil {
		return err
	}
	// UpdateState atomically clears frame_id when target state is 'fresh'
	// (defensive guard in enforceAndUpdate per spec §4.4 + §10.3). Failed
	// nodes preserve frame_id (the §17/§14.2 contract); the centralised
	// clear in enforceAndUpdate is what makes that asymmetry hold without
	// a separate SetFrameID call here.
	// Cascade message-pass: mark children stale + frame_id BEFORE the tx
	// commits, so the child's frame_id is visible to the next scheduler
	// tick's frame-end predicate (§4.2) atomically with the parent's
	// fresh transition. Skip on no-op commits (changed=false) per §4.4
	// pruning: children stay fresh, no dispatch enqueued.
	if t.Changed {
		dependents, err := args.Storage.Nodes().ListDependentsOf(ctx, acq.NodeID, stx)
		if err != nil {
			return fmt.Errorf("applyTerminalComplete: list dependents: %w", err)
		}
		for _, dep := range dependents {
			// Cascade: mark child stale + frame_id when it's in a state
			// the cascade can legitimately advance: 'fresh' (the spec's
			// canonical case, §4.4) or 'stale' with no frame_id (a
			// defensive backstop — under the frame model Create() defaults
			// to 'fresh', so this branch is not reachable from a normal
			// initial-create. It still defends against (a) orphan-reap
			// recovery paths that may transition a node to stale before
			// re-entering the engine and (b) any future code path that
			// lands a node at stale without a frame_id; the cascade should
			// stamp the frame_id rather than skip the row).
			_, err := tx.Exec(ctx, `
                UPDATE rimsky_nodes
                SET state = 'stale', frame_id = $1, updated_at = now()
                WHERE id = $2
                  AND (state = 'fresh' OR (state = 'stale' AND frame_id IS NULL))
            `, acq.FrameID, dep.ID)
			if err != nil {
				return fmt.Errorf("applyTerminalComplete: cascade child %s: %w", dep.ID, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("applyTerminalComplete: commit tx: %w", err)
	}
	committed = true

	// Spec §16 event-kind matrix: a real attribute commit emits
	// `attributes_committed`; a no-op commit (changed=false) emits the
	// preserved `no_op_commit` kind so dependents can distinguish a
	// data-bearing commit from a "ran, nothing changed" run without
	// having to inspect the payload.
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

	// Cascade on real changes.
	//
	// This is a second walk over ListDependentsOf — the first happened in
	// the in-tx cascade (mark each child stale + frame_id). The two walks
	// have different responsibilities: the in-tx walk needs to atomically
	// mutate the child's state alongside this node's commit; the post-
	// commit walk routes the recalculate event through scheduler.
	// RecalculateNode, which checks dep-fresh-state and enqueues a
	// dispatch only when all deps are fresh. Inlining the second walk
	// into the in-tx cascade would couple supervisor commit semantics to
	// scheduler-side enqueue logic and require either threading the
	// queue's enqueue inside this tx (which has its own implications for
	// the supervisor-pool predicate's dep-fresh assumption) or
	// duplicating the dep-fresh check inline. Acceptable cost: one extra
	// SELECT per terminal commit.
	if t.Changed {
		dependents, err := args.Storage.Nodes().ListDependentsOf(ctx, acq.NodeID, nil)
		if err == nil {
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
	}
	return nil
}

// applyTerminalAppError routes a Blocked / Errored terminal through the
// node's policy chain (per spec §7.3 / existing OnError semantics) and
// then drives ReleaseLock + lock-holder cleanup + state update + queue
// mutation in one transaction so a partial failure rolls back cleanly.
//
// The resolved action's `Kind` is the runtime intent:
//   - "retry"              → release mode picked from acq's resumable flag
//     (back-compat shape kept for callers that still emit plain "retry").
//   - "discard_then_retry" → ReleaseLock(give_up) explicitly.
//   - "resume_then_retry"  → ReleaseLock(preserve_for_resume) when the
//     spec is resumable; falls back to give_up + warns otherwise.
//   - "invalidate"         → give_up release + invalidate(targets).
//   - "give_up"            → give_up release + state→failed.
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
	releaseAction := releaseActionForKind(resolved.Kind, acq, args)
	terminalForResolution := claimstorepg.TerminalGiveUp

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
	if err := releaseLocksInTx(ctx, args, tx, acq, releaseAction, terminalForResolution); err != nil {
		return err
	}

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
		if err := enqueueInTx(ctx, tx, queue.DispatchRequest{
			NodeID:         acq.NodeID,
			ExecutorName:   acq.Executor,
			RequiredStores: requiredStoresForAcq(acq),
			EnqueuedAt:     args.Clock.Now().Add(time.Duration(resolved.DelayMs) * time.Millisecond),
			FrameID:        acq.FrameID,
		}); err != nil {
			return err
		}
	case "invalidate":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateStale, node.ReasonPolicyInvalidate, stx); err != nil {
				return err
			}
		}
		if err := removeDispatchForNodeInTx(ctx, tx, acq.NodeID, args.SupervisorID); err != nil {
			return err
		}
	case "give_up":
		if prior != nil && prior.State == shared.NodeStateRunning {
			if err := args.Storage.Nodes().UpdateState(ctx, acq.NodeID,
				shared.NodeStateFailed, node.ReasonPolicyGiveUp, stx); err != nil {
				return err
			}
		}
		if err := removeDispatchForNodeInTx(ctx, tx, acq.NodeID, args.SupervisorID); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("applyTerminalAppError: commit: %w", err)
	}
	committed = true

	// Side effects that must NOT roll back if the tx fails:
	// — error event (audit trail) and invalidate-targets fan-out.
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

// releaseActionForKind maps a ResolvedAction.Kind to the §13.6
// ReleaseLock action. Delegates to MapTerminalToReleaseAction (the
// authoritative §12.6 table); the wrapper exists to log a warning when
// resume_then_retry has to fall back to give_up because the acq has no
// resumable spec.
func releaseActionForKind(kind string, acq *acquisition, args RunArgs) store.ReleaseAction {
	resumable := policyAllowsResume(acq)
	if kind == string(PolicyResumeThenRetry) && !resumable && args.Logger != nil {
		args.Logger.Warn("resume_then_retry on non-resumable acq — falling back to give_up",
			"node_id", acq.NodeID.String())
	}
	// Non-Complete / non-Infra path: kind is the resolved policy name.
	return MapTerminalToReleaseAction(TerminalKindErrored, PolicyResolution(kind), resumable)
}

// enqueueInTx mirrors core/queue/postgres/queue.go:Enqueue but runs on
// the supplied tx so the §13.6 release tx + state update + re-enqueue
// are one atomic step. @source: core/queue/postgres/queue.go:Enqueue
//
// FrameID is required (blessed-invariant 19, spec §10.2).
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
// core/queue/postgres/queue.go:RemoveForNode but runs on the supplied
// tx. @source: core/queue/postgres/queue.go:RemoveForNode
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

// applyTerminalInfraError implements the infra_reenqueue path. The
// release, state→stale, and re-enqueue all run inside one tx so a
// partial failure rolls back together. ReleaseLock action is always
// give_up — infra errors don't preserve sidecars.
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

	if err := releaseLocksInTx(ctx, args, tx, acq, store.ReleaseGiveUp, claimstorepg.TerminalGiveUp); err != nil {
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

	// Audit event (no rollback needed).
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
// acquired-locks slice in §13.7 sort order, calls store.ReleaseLock
// with the supplied action, runs §5.6.4 resolution for held claims,
// and either DELETEs or PRESERVEs each lock-holder row. Caller owns
// the tx (begin + commit/rollback) so additional in-tx work — state
// updates, queue mutations — can ride alongside the release.
func releaseLocksInTx(
	ctx context.Context, args RunArgs, tx pgx.Tx, acq *acquisition,
	action store.ReleaseAction, terminalForResolution claimstorepg.TerminalOutcome,
) error {
	resumeGrace := args.ResumeGrace
	if resumeGrace <= 0 {
		resumeGrace = 30 * time.Minute
	}
	storeCtx := store.WithTx(ctx, tx)
	for _, lk := range acq.Locks {
		if lk.Store != nil {
			if err := lk.Store.ReleaseLock(storeCtx, lk.Handle, action); err != nil {
				return fmt.Errorf("releaseLocksInTx: ReleaseLock(%s): %w", lk.Handle.StoreName, err)
			}
			if lk.Handle.Kind == string(store.LockHolderKindClaim) {
				if err := resolveHeldClaim(storeCtx, lk, acq.NodeID, terminalForResolution); err != nil {
					return err
				}
			}
		}
		if action == store.ReleasePreserveResume && lk.Handle.Kind != string(store.LockHolderKindNamed) {
			if err := args.LockHolders.PreserveForResume(ctx, tx,
				mustParseUUID(lk.Handle.ID), args.SupervisorID, int(resumeGrace.Seconds())); err != nil {
				return fmt.Errorf("releaseLocksInTx: PreserveForResume(%s): %w", lk.Handle.ID, err)
			}
			emitLockReleased(ctx, args, acq, lk, "preserve_for_resume")
			continue
		}
		if err := args.LockHolders.DeleteByID(ctx, tx, mustParseUUID(lk.Handle.ID), args.SupervisorID); err != nil {
			return fmt.Errorf("releaseLocksInTx: DeleteByID(%s): %w", lk.Handle.ID, err)
		}
		emitLockReleased(ctx, args, acq, lk, string(action))
	}
	return nil
}

// resolveHeldClaim runs the §5.6.4 algorithm for one acquired claim
// lock. Only fires when the lock has an active rimsky_claim_holders
// row (a hold-mode claim). claim-and-forget claims have no holder row
// and the helper no-ops.
func resolveHeldClaim(
	storeCtx context.Context, lk AcquiredLock, nodeID shared.UUID,
	terminal claimstorepg.TerminalOutcome,
) error {
	cs, ok := lk.Store.(*claimstorepg.Store)
	if !ok {
		return nil // direct-mode store; no §5.6.4 work.
	}
	if lk.ClaimResult.ClaimID == "" {
		return nil
	}
	return cs.ResolveOnTerminal(storeCtx, lk.ClaimResult.ClaimID, nodeID.String(), terminal)
}

// upsertFinalAttributesTx writes the merged-and-validated attribute
// object back to rimsky_node_attributes inside the supplied tx so it
// commits or rolls back atomically with the lock-release work in
// §17.1 step 6c. The prior `run_attempt` is read on the same tx;
// absence (no prior row) defaults to 1.
//
// Per spec §5.7.2 the executor may have written incremental fields via
// the §12.5 `POST /v1/attributes/{node_id}` callback. Those merges land
// directly on the DB row via NodeAttributes.MergeDelta and would be
// clobbered if we wrote `merged` (which contains only the resolved
// source-driven fields plus the terminal `attributes_delta`) outright.
// To preserve incremental writes the final row is `prior.Data + merged`,
// where `prior` is the DB row and `merged` overrides for fields the
// terminal carries. For the common case (no incremental writes) `prior`
// equals the resolved attributes the runner upserted at dispatch and
// the result is identical to `merged`.
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
// supplied storage.Tx. Mirrors the SQL in
// core/storage/postgres/node_attributes.go's Upsert; we replicate it
// here because the storage interface's Upsert does not accept a Tx,
// and the §17.1 step 6c contract requires the write to share the
// release tx.
//
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
// into the substituted attribute object. Top-level keys in delta
// overwrite top-level keys in the base; nested objects are replaced
// (matching §12.5's PG `||` merge semantics).
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
		"lock_kind":     lk.Handle.Kind,
		"store_name":    lk.Handle.StoreName,
		"holder_id":     lk.Handle.ID,
		"supervisor_id": args.SupervisorID,
		"action":        action,
	}
	if named, ok := lk.Spec.(store.NamedLockSpec); ok {
		payload["lock_name"] = named.Name
	}
	if lk.ClaimResult.ClaimID != "" {
		payload["claim_id"] = lk.ClaimResult.ClaimID
	}
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "lock_released", Payload: payload,
	}, nil)
}

// lookupPolicyForNode resolves the per-error-class policy from the
// candidate's template node-def. nil return is the canonical
// "no-policy" signal — node.Evaluate maps it to give_up(unknown_error_class).
func lookupPolicyForNode(
	ctx context.Context, args RunArgs, acq *acquisition, errorClass string,
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

// policyAllowsResume reports whether any of the acq's lock specs
// declared `resumable: true`. Only when at least one spec is resumable
// can the runner take the preserve_for_resume branch on retry; the
// rest fall through to give_up.
func policyAllowsResume(acq *acquisition) bool {
	for _, lk := range acq.Locks {
		switch v := lk.Spec.(type) {
		case store.RegionLockSpec:
			if v.Resumable {
				return true
			}
		case store.ClaimLockSpec:
			if v.Resumable {
				return true
			}
		}
	}
	return false
}

// requiredStoresForAcq derives the list of store names referenced by
// this acquisition's lock specs. Used when re-enqueueing on retry /
// infra_reenqueue so the rebooted dispatch row carries the same
// supervisor-pool predicate as the original.
func requiredStoresForAcq(acq *acquisition) []string {
	if acq == nil || acq.NodeDef == nil {
		return nil
	}
	return node.RequiredStores(*acq.NodeDef)
}

// invalidateTargets resolves the policy's target node-types to node
// IDs in the same instance and routes scheduler.InvalidateNode to
// each. Mirrors the existing OnError invalidate branch.
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

// outcomeForChanged maps a Complete{changed} bool to the existing
// work_completed event's `outcome` payload value.
func outcomeForChanged(changed bool) string {
	if changed {
		return "committed"
	}
	return "no_op"
}

// runQualityRules walks the per-node quality rules against a populated
// attributes object and returns the failures (errors only, no
// warnings). Empty return = all rules accepted at error severity.
//
// The attribute object is passed as `NewData`; previous-version
// comparisons (for rules like row_count_ratio) are not exercised by
// attribute writeback in v1 — the attribute object is the whole new
// record, and previous-data wiring happens elsewhere if at all.
func runQualityRules(rules []qualityrule.Spec, attrs map[string]any) []qualityrule.Failure {
	if len(rules) == 0 {
		return nil
	}
	errs, _, err := qualityrule.EvaluateAll(context.Background(), rules,
		qualityrule.EvalInput{NewData: attrs})
	if err != nil {
		// Treat evaluation errors as a synthetic failure carrying the
		// error string in Details. The terminal handler emits one
		// quality_rule_failed event per entry.
		return []qualityrule.Failure{{
			RuleType: "evaluation_error",
			Severity: shared.SeverityError,
			Details:  err.Error(),
		}}
	}
	return errs
}
