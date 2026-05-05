// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Atomic acquisition under stores-redesign-v3 spec §7.3.
//
// Per-candidate try tx (rimsky-side bookkeeping only):
//   - candidate selection (FOR UPDATE SKIP LOCKED) in a short read tx;
//   - per-candidate try tx: in-Go eligibility, advisory locks for
//     named locks, claimant-guarded UPDATE on rimsky_worker_request, scope
//     re-evaluation per store via byte-equal + ModeCoexists, per-spec
//     lock acquisition (Insert + remote Open + UpdateAddress for
//     ClaimSpec; Insert only for NamedLockSpec), held-claim
//     rimsky_claim_holders inserts when the alias is in a held
//     subgraph.
//   - COMMIT, then verify-before-run (separate read), then a second
//     short tx transitioning the node to running.
//
// Store.Open is invoked OVER THE WIRE in v3; the store runs its
// own state mutation in its own transaction. Tx-sharing via
// locks.WithTx / TxFromContext is gone.
//
// Two primitives, two types: locks.NamedLockSpec and locks.ClaimSpec.

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// acquisition is the in-memory record of one successful acquisition.
type acquisition struct {
	DispatchID     shared.UUID
	NodeID         shared.UUID
	InstanceID     shared.UUID
	NodeType       string
	Executor       string
	FrameID        shared.UUID
	Locks          []AcquiredLock
	NodeDef        *node.TemplateNodeDef
	HeldSubgraphs  []node.HoldingSubgraph
	InstanceParams map[string]any
}

// acquireCandidate runs the §7.3 flow against the live database.
func acquireCandidate(ctx context.Context, args RunArgs, heartbeatInterval time.Duration) (acquisition, bool, error) {
	candidates, err := selectCandidatesShortTx(ctx, args)
	if err != nil {
		return acquisition{}, false, err
	}
	if len(candidates) == 0 {
		return acquisition{}, false, nil
	}

	for _, cand := range candidates {
		if cand.FrameID == (shared.UUID{}) {
			args.Logger.Warn("acquireCandidate: skipping candidate with nil frame_id",
				"dispatch_id", cand.DispatchID.String(),
				"node_id", cand.NodeID.String(),
				"reason", "frame_id_null")
			continue
		}
		acq, ok, err := tryAcquireWithTx(ctx, args, cand, heartbeatInterval)
		if err != nil {
			return acquisition{}, false, err
		}
		if !ok {
			continue
		}
		if !verifyBeforeRun(ctx, args, acq) {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		if err := transitionToRunning(ctx, args, acq); err != nil {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := args.Persist.Nodes().UpdateHeartbeat(ctx, acq.NodeID, args.Clock.Now(), args.SupervisorID, tx); err != nil {
				return err
			}
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "work_started", Payload: map[string]any{
					"supervisor_id": args.SupervisorID,
					"dispatch_id":   acq.DispatchID.String(),
				},
			}, tx)
		})
		for _, lk := range acq.Locks {
			emitLockAcquired(ctx, args, acq, lk)
		}
		return acq, true, nil
	}
	return acquisition{}, false, nil
}

// selectCandidatesShortTx runs the candidate-selection helper in its
// own short read tx. Read-only — the surrounding tx commits with no
// writes; the rows' FOR UPDATE SKIP LOCKED locks release at commit.
func selectCandidatesShortTx(ctx context.Context, args RunArgs) ([]persistence.Candidate, error) {
	limit := args.SelectCandidatesLimit
	if limit <= 0 {
		limit = 8
	}
	var candidates []persistence.Candidate
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := args.Queue.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: args.AcceptedExecutors,
			AcceptedStores:    args.AcceptedStores,
			Limit:             limit,
		})
		if err != nil {
			return err
		}
		candidates = out
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("acquireCandidate: SelectCandidates: %w", err)
	}
	return candidates, nil
}

// tryAcquireWithTx wraps tryAcquire in its own tx so a failed
// candidate's partial mutations roll back rather than leak into the
// next candidate.
func tryAcquireWithTx(
	ctx context.Context, args RunArgs, cand persistence.Candidate,
	heartbeatInterval time.Duration,
) (acquisition, bool, error) {
	var (
		acq acquisition
		ok  bool
	)
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var inner error
		acq, ok, inner = tryAcquire(ctx, args, tx, cand, heartbeatInterval)
		if inner != nil {
			return inner
		}
		if !ok {
			// Roll back so partial mutations from a non-eligible
			// candidate don't leak.
			return errTryAcquireRollback
		}
		return nil
	})
	if err != nil && err != errTryAcquireRollback {
		return acquisition{}, false, fmt.Errorf("tryAcquireWithTx: %w", err)
	}
	if err == errTryAcquireRollback {
		return acquisition{}, false, nil
	}
	return acq, ok, nil
}

// errTryAcquireRollback is a sentinel returned from the per-candidate
// acquisition tx to force a clean rollback without surfacing a real
// error to the caller. Used when in-Go eligibility checks bail before
// any state mutation should commit.
var errTryAcquireRollback = fmt.Errorf("supervisor: tryAcquire rollback (sentinel)")

// tryAcquire runs the acquisition steps for a single candidate inside
// the open rimsky-side tx. Note: Store.Open RPCs over the wire and the
// store runs in its own tx (per spec §7.3).
//
// All persistence calls reuse the open `tx`. Passing nil here would
// self-deadlock against the SQLite driver's single-connection pool:
// the caller's tx holds the only conn, so a fresh-conn read would
// block forever waiting for the tx to commit (which can't, because
// it's awaiting the read).
func tryAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	cand persistence.Candidate, heartbeatInterval time.Duration,
) (acquisition, bool, error) {
	nd, err := args.Persist.Nodes().Get(ctx, cand.NodeID, tx)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: nodes.Get: %w", err)
	}
	if nd == nil {
		return acquisition{}, false, nil
	}
	inst, _ := args.Persist.Instances().Get(ctx, nd.InstanceID, tx)
	tmpl := lookupTemplate(ctx, args, tx, inst)
	nodeDef := lookupNodeDef(tmpl, nd.NodeType)
	specs, err := buildLockSpecs(ctx, args, tx, nd, nodeDef, inst)
	if err != nil {
		args.Logger.Warn("tryAcquire: lock-spec substitution failed",
			"node_id", cand.NodeID.String(), "error", err.Error())
		return acquisition{}, false, nil
	}
	sortLockSpecs(specs)

	if err := takeNamedAdvisoryLocks(ctx, args, tx, specs); err != nil {
		return acquisition{}, false, err
	}

	claimed, err := args.Queue.ClaimDispatchRow(ctx, tx, cand.DispatchID, args.SupervisorID)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: ClaimDispatchRow: %w", err)
	}
	if !claimed {
		return acquisition{}, false, nil
	}

	heldSubgraphs := node.HoldingSubgraphsForTemplate(tmpl)

	acquiredLocks := make([]AcquiredLock, 0, len(specs))
	for _, sp := range specs {
		al, ok, err := acquireOneLock(ctx, args, tx, sp, cand, heartbeatInterval, heldSubgraphs)
		if err != nil {
			return acquisition{}, false, err
		}
		if !ok {
			return acquisition{}, false, nil
		}
		acquiredLocks = append(acquiredLocks, al)
	}

	out := acquisition{
		DispatchID:    cand.DispatchID,
		NodeID:        cand.NodeID,
		InstanceID:    nd.InstanceID,
		NodeType:      nd.NodeType,
		Executor:      nd.Executor,
		FrameID:       cand.FrameID,
		Locks:         acquiredLocks,
		NodeDef:       nodeDef,
		HeldSubgraphs: heldSubgraphs,
	}
	if inst != nil {
		out.InstanceParams = inst.Params
	}
	return out, true, nil
}

// takeNamedAdvisoryLocks walks the sorted spec slice and takes one
// advisory lock per NamedLockSpec.
func takeNamedAdvisoryLocks(ctx context.Context, args RunArgs, tx persistence.Tx, specs []any) error {
	for _, sp := range specs {
		named, ok := sp.(locks.NamedLockSpec)
		if !ok {
			continue
		}
		if err := args.AdvisoryLocker.TakeNamedLockInTx(ctx, tx, named.Name); err != nil {
			return fmt.Errorf("takeNamedAdvisoryLocks(%q): %w", named.Name, err)
		}
	}
	return nil
}

// acquireOneLock handles one spec inside the acquisition tx.
func acquireOneLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	sp any, cand persistence.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, bool, error) {
	switch spec := sp.(type) {
	case locks.NamedLockSpec:
		return acquireNamedLock(ctx, args, tx, spec, cand, heartbeatInterval)
	case locks.ClaimSpec:
		return acquireClaim(ctx, args, tx, spec, cand, heartbeatInterval, heldSubgraphs)
	}
	return AcquiredLock{}, false, fmt.Errorf("acquireOneLock: unknown spec kind %T", sp)
}

// acquireNamedLock enforces the counter-semaphore limit then inserts
// the named lock-holder row. The per-name advisory lock has been
// taken upstream (takeNamedAdvisoryLocks); under that lock the
// CountByNamedLock + Insert pair is atomic against the limit.
//
// When the operator's NamedLocks config has no entry for this name,
// no limit is enforced (limit defaults to ∞). Templates referencing
// undeclared names should have failed validation at deploy time
// (control-api wires NamedLockDeclared unconditionally).
func acquireNamedLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.NamedLockSpec, cand persistence.Candidate, heartbeatInterval time.Duration,
) (AcquiredLock, bool, error) {
	if cfg, ok := args.NamedLocks.Get(spec.Name); ok {
		count, err := args.LockHolders.CountByNamedLock(ctx, spec.Name, tx)
		if err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: CountByNamedLock(%q): %w", spec.Name, err)
		}
		if count >= cfg.Limit {
			return AcquiredLock{}, false, nil
		}
	}
	rowID := uuid.New()
	frameID := cand.FrameID
	dispatchID := cand.DispatchID
	nameCopy := spec.Name
	in := persistence.LockHolderInsertInput{
		ID:                 rowID,
		WorkerRequestID:    &dispatchID,
		LockKind:           persistence.LockKindNamed,
		LockName:           &nameCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ExpiresAt:          args.Clock.Now().Add(5 * heartbeatInterval),
		FrameID:            &frameID,
		// Named locks are never held past active terminal; they release
		// at the worker-request's active-phase terminal.
		IsHeld: false,
	}
	if err := args.LockHolders.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: Insert: %w", err)
	}
	return AcquiredLock{
		Spec:         spec,
		LockHolderID: rowID,
	}, true, nil
}

// acquireClaim runs the claim-acquisition steps per spec §7.3 step 4.
//
// Conflict detection uses byte-equal comparison on scope bytes (per
// spec §7.7); the candidate's pre-Open scope is the substituted-
// selector bytes. For pick-policy claims the store's
// FOR UPDATE SKIP LOCKED prevents two supervisors picking the same
// item independently of rimsky's predicate. For scoped claims
// rimsky's predicate is the source of truth for invariant 4b.
//
// To prevent two supervisors from concurrently passing the in-Go
// scope-conflict predicate against each other's uncommitted INSERTs
// (READ COMMITTED hides them), this function takes a per-(store_name,
// scope_data) transactional advisory lock before evaluateScopeConflict
// runs. Analogous to the named-lock advisory; under the same lock the
// list-then-INSERT pair is atomic against any concurrent acquirer
// targeting the same (store, scope) pair.
func acquireClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.ClaimSpec, cand persistence.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, bool, error) {
	s, ok := args.StoreRegistry.Get(spec.StoreName)
	if !ok {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: unknown store %q", spec.StoreName)
	}
	scopeInitial, err := json.Marshal(spec.Selector)
	if err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: marshal selector: %w", err)
	}
	if err := args.AdvisoryLocker.TakeScopeLockInTx(ctx, tx, spec.StoreName, scopeInitial); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: TakeScopeLockInTx: %w", err)
	}
	// Pre-Open conflict check: any existing scope-byte-equal holder must
	// permit our intent under its own RealizedWriteSemantics. Per the
	// uniformity invariant (spec §2.5), all byte-equal-scope claims share
	// identical semantics, so the candidate's effective semantics on a
	// match equals the holder's recorded value.
	conflicted, err := evaluateScopeConflict(ctx, args, tx, spec, cand)
	if err != nil {
		return AcquiredLock{}, false, err
	}
	if conflicted {
		return AcquiredLock{}, false, nil
	}

	rowID := uuid.New()
	frameID := cand.FrameID
	dispatchID := cand.DispatchID
	storeNameCopy := spec.StoreName
	intentCopy := string(spec.Intent)
	// is_held is determined by the holding-subgraph membership for this
	// (acquirerType, alias). When the alias declares a held subgraph of
	// size > 1, the claim_handle persists past active terminal until
	// auto-terminal resolution.
	subgraph, hasSubgraph := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, spec.Alias)
	isHeld := hasSubgraph && subgraph.IsHeld()
	in := persistence.LockHolderInsertInput{
		ID:                 rowID,
		WorkerRequestID:    &dispatchID,
		LockKind:           persistence.LockKindScope,
		StoreName:          &storeNameCopy,
		ScopeData:          scopeInitial,
		Intent:             &intentCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ExpiresAt:          args.Clock.Now().Add(5 * heartbeatInterval),
		FrameID:            &frameID,
		IsHeld:             isHeld,
	}
	if err := args.LockHolders.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: Insert: %w", err)
	}

	claimID := locks.ClaimID(rowID.String())
	outcome, err := s.Open(ctx, claimID, spec)
	if err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: Open(%s): %w", spec.StoreName, err)
	}
	// Store has nothing to give right now (e.g. drained items-table
	// queue). Per the 2026-04-30 cleanup spec the store signals this
	// via OpenOutcome.Available=false (was: all-empty ClaimResult under
	// v3 §4.7's "pool-empty" convention). Roll back the tx and skip;
	// the next scheduler tick may retry.
	if !outcome.Available {
		return AcquiredLock{}, false, nil
	}
	cr := outcome.Result

	if err := args.LockHolders.UpdateAddress(ctx, rowID, args.SupervisorID, cr.Address, tx); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireClaim: UpdateAddress: %w", err)
	}
	// Pick-policy claims have store-chosen scope; scoped claims
	// keep the substituted selector (already written above).
	if len(cr.Scope) > 0 && string(cr.Scope) != string(scopeInitial) {
		if err := args.LockHolders.UpdateScope(ctx, rowID, args.SupervisorID, cr.Scope, tx); err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireClaim: UpdateScope: %w", err)
		}
	}
	// Persist the per-claim RealizedWriteSemantics returned by the
	// producer. Required for the in-Go scope-conflict check on
	// subsequent acquisitions; per the uniformity invariant (§2.5) all
	// byte-equal-Scope claims must share this value.
	if cr.RealizedWriteSemantics != "" {
		if err := args.LockHolders.UpdateRealizedWriteSemantics(ctx, rowID, args.SupervisorID, string(cr.RealizedWriteSemantics), tx); err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireClaim: UpdateRealizedWriteSemantics: %w", err)
		}
	}

	if err := insertHeldClaimHoldersAtAcquire(ctx, args, tx, rowID, cand, spec.Alias, heldSubgraphs); err != nil {
		return AcquiredLock{}, false, err
	}

	return AcquiredLock{
		Spec:         spec,
		LockHolderID: rowID,
		ClaimResult:  cr,
		Store:        s,
		Alias:        spec.Alias,
	}, true, nil
}

// evaluateScopeConflict re-loads existing scope holders for the
// store and runs ScopesByteEqual ∧ ModeCoexists against the candidate
// spec. Skips own-node rows. Returns true if any holder conflicts AND
// the modes don't coexist.
//
// Per spec §7.7: byte-equal comparison; the producer canonicalizes its
// scope bytes such that two claims that should conflict produce
// byte-equal scopes. The candidate's pre-Open scope is the
// substituted-selector bytes (scoped claims) — for pick-policy
// claims the actual collision check happens in the producer's own
// internal serialization.
//
// Per the uniformity invariant (spec §2.5) all byte-equal-Scope
// claims share identical RealizedWriteSemantics. The conflict check
// uses the holder's recorded RealizedWriteSemantics for both sides.
func evaluateScopeConflict(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.ClaimSpec, cand persistence.Candidate,
) (bool, error) {
	holders, err := args.LockHolders.ListByStoreScope(ctx, spec.StoreName, tx)
	if err != nil {
		return false, fmt.Errorf("evaluateScopeConflict: ListByStoreScope: %w", err)
	}
	candidateScope, err := json.Marshal(spec.Selector)
	if err != nil {
		return false, err
	}
	_ = ctx // ctx no longer used post-envelope refactor; retained for symmetry.
	for _, h := range holders {
		if h.HolderNodeID == cand.NodeID && h.HolderSupervisorID == args.SupervisorID {
			continue
		}
		if !locks.ScopesByteEqual(candidateScope, h.ScopeData) {
			continue
		}
		var holderIntent locks.Intent
		if h.Intent != nil {
			holderIntent = locks.Intent(*h.Intent)
		}
		holderRWS := locks.WriteSemantics(h.RealizedWriteSemantics)
		// By the uniformity invariant the candidate's realized semantics
		// (post-Open) MUST match the holder's; we use the holder's
		// recorded value for both sides of the matrix.
		if !locks.ModeCoexists(spec.Intent, holderRWS, holderIntent, holderRWS) {
			return true, nil
		}
	}
	return false, nil
}

// insertHeldClaimHoldersAtAcquire inserts one rimsky_claim_holders
// row per holding-subgraph member when the alias is held.
func insertHeldClaimHoldersAtAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	lockHolderID shared.UUID, cand persistence.Candidate, alias string,
	heldSubgraphs []node.HoldingSubgraph,
) error {
	subgraph, ok := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, alias)
	if !ok || !subgraph.IsHeld() {
		return nil
	}
	nd, err := args.Persist.Nodes().Get(ctx, cand.NodeID, tx)
	if err != nil || nd == nil {
		return fmt.Errorf("insertHeldClaimHoldersAtAcquire: nodes.Get: %w", err)
	}
	siblings, err := args.Persist.Nodes().ListByInstance(ctx, nd.InstanceID, tx)
	if err != nil {
		return fmt.Errorf("insertHeldClaimHoldersAtAcquire: ListByInstance: %w", err)
	}
	memberSet := make(map[string]struct{}, len(subgraph.Members))
	for _, m := range subgraph.Members {
		memberSet[m] = struct{}{}
	}
	frameID := cand.FrameID
	for _, sib := range siblings {
		if _, ok := memberSet[sib.NodeType]; !ok {
			continue
		}
		err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:           uuid.New(),
			LockHolderID: lockHolderID,
			HolderNodeID: sib.ID,
			FrameID:      &frameID,
		}, tx)
		if err != nil {
			return fmt.Errorf("insertHeldClaimHoldersAtAcquire: Insert: %w", err)
		}
	}
	return nil
}

// findHoldingSubgraphForAcquirer locates the (acquirerType, alias)
// subgraph in the precomputed list.
func findHoldingSubgraphForAcquirer(subgraphs []node.HoldingSubgraph, acquirerType, alias string) (node.HoldingSubgraph, bool) {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg, true
		}
	}
	return node.HoldingSubgraph{}, false
}

// verifyBeforeRun is the separate-read guard.
func verifyBeforeRun(ctx context.Context, args RunArgs, acq acquisition) bool {
	ownership, err := args.Queue.GetClaimedBy(ctx, acq.DispatchID)
	if err != nil {
		args.Logger.Warn("verifyBeforeRun: GetClaimedBy failed",
			"dispatch_id", acq.DispatchID.String(), "error", err.Error())
		return false
	}
	return ownership.Kind == "claimed_by" && ownership.SupervisorID == args.SupervisorID
}

// handleOrphanedClaim is the race-detection bail path: the supervisor
// has already opened the claim, inserted the lock-holder row, and
// committed the acquisition tx — and then verify-before-run discovered
// that another supervisor stole the dispatch row in the gap between
// commit and the second-read guard. The supervisor knows it just
// opened the store state and is now unwinding the in-progress
// acquisition; it owns the cleanup and calls Abandon on the store
// to release any partial state, then deletes its own lock-holder row
// claimant-guarded, then emits orphaned_claim_lost_race.
//
// This is NOT the periodic orphan reaper. The periodic reaper at
// `core/scheduler/sweep_locks.go::sweepLockHolders` deletes expired
// lock-holder rows WITHOUT firing Abandon, per v3 spec §7.5: the
// store's own TTL/sweep handles internal state for owners that
// crashed without unwinding. The two paths are deliberately distinct:
// the bail path fires Abandon because the supervisor knows what it
// just did; the reaper does NOT fire Abandon because it can't
// distinguish a crashed-supervisor state from any other.
func handleOrphanedClaim(ctx context.Context, args RunArgs, acq acquisition) {
	for _, lk := range acq.Locks {
		if lk.Store != nil {
			scope := claimScope(lk)
			address := claimAddress(lk)
			claimID := locks.ClaimID(lk.LockHolderID.String())
			if err := lk.Store.Abandon(ctx, claimID, scope, address); err != nil {
				args.Logger.Warn("handleOrphanedClaim: Abandon failed",
					"store", storeNameForSpec(lk.Spec), "error", err.Error())
			}
		}
		_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.LockHolders.Delete(ctx, lk.LockHolderID, args.SupervisorID, tx)
		})
	}
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "orphaned_claim_lost_race",
			Payload: map[string]any{
				"dispatch_id":   acq.DispatchID.String(),
				"supervisor_id": args.SupervisorID,
			},
		}, tx)
	})
}

// transitionToRunning is the short-tx state transition.
func transitionToRunning(ctx context.Context, args RunArgs, acq acquisition) error {
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
			shared.NodeStateRunning, cascade.ReasonDispatchClaimed, tx)
	})
}

// emitLockAcquired emits the per-spec lock_acquired event.
func emitLockAcquired(ctx context.Context, args RunArgs, acq acquisition, lk AcquiredLock) {
	payload := map[string]any{
		"holder_id":     lk.LockHolderID.String(),
		"supervisor_id": args.SupervisorID,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload["lock_kind"] = string(persistence.LockKindNamed)
		payload["lock_name"] = sp.Name
	case locks.ClaimSpec:
		payload["lock_kind"] = string(persistence.LockKindScope)
		payload["store_name"] = sp.StoreName
		payload["alias"] = sp.Alias
		payload["intent"] = string(sp.Intent)
	}
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "lock_acquired", Payload: payload,
		}, tx)
	})
}

// claimScope returns the store's scope bytes for a ClaimSpec
// acquisition; nil for NamedLockSpec.
func claimScope(lk AcquiredLock) []byte {
	if lk.Store == nil {
		return nil
	}
	return []byte(lk.ClaimResult.Scope)
}

// claimAddress returns the store's address bytes for a ClaimSpec
// acquisition; nil for NamedLockSpec.
func claimAddress(lk AcquiredLock) []byte {
	if lk.Store == nil {
		return nil
	}
	return []byte(lk.ClaimResult.Address)
}
