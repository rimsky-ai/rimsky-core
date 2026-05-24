// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// runner_acquire_claims.go — claim (scope-kind) acquisition path. Split
// out of `runner_acquire.go` per the 2026-05-17 cold-read paydown
// (Item 4 / Tier 1).

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
)

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
// claim-scope-conflict predicate against each other's uncommitted INSERTs
// (READ COMMITTED hides them), this function takes a per-(producer_name,
// claim_scope_data) transactional advisory lock before evaluateClaimScopeConflict
// runs. Analogous to the named-lock advisory; under the same lock the
// list-then-INSERT pair is atomic against any concurrent acquirer
// targeting the same (producer, claim-scope) pair.
func acquireClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.ClaimSpec, cand persistence.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, openResult, error) {
	// Latency timer for `rimsky_claim_acquisition_latency_seconds`. Start
	// the clock at the top of the function so the histogram includes the
	// pre-Open advisory-lock + scope-conflict check; observe only on
	// resolved outcomes (acquired / unavailable).
	acquireStart := args.Clock.Now()
	s, ok := args.StoreRegistry.Get(spec.ProducerName)
	if !ok {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: unknown store %q", spec.ProducerName)
	}
	scopeInitial, err := json.Marshal(spec.Selector)
	if err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: marshal selector: %w", err)
	}
	if err := args.AdvisoryLocker.TakeClaimScopeLockInTx(ctx, tx, spec.ProducerName, scopeInitial); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: TakeClaimScopeLockInTx: %w", err)
	}
	// Pre-Open conflict check: any existing claim-scope-byte-equal holder
	// must permit our intent under its own RealizedWriteSemantics. Per the
	// uniformity invariant (spec §2.5), all byte-equal-claim-scope claims
	// share identical semantics, so the candidate's effective semantics on a
	// match equals the holder's recorded value.
	conflicted, err := evaluateClaimScopeConflict(ctx, args, tx, spec, cand)
	if err != nil {
		return AcquiredLock{}, openResultBail, err
	}
	if conflicted {
		return AcquiredLock{}, openResultBail, nil
	}

	rowID := uuid.New()
	frameID := cand.FrameID
	dispatchID := cand.DispatchID
	producerNameCopy := spec.ProducerName
	intentCopy := string(spec.Intent)
	// is_held is determined by the holding-subgraph membership for this
	// (acquirerType, alias). When the alias declares a held subgraph of
	// size > 1, the claim_handle persists past active terminal until
	// auto-terminal resolution.
	subgraph, hasSubgraph := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, spec.Alias)
	isHeld := hasSubgraph && subgraph.IsHeld()
	in := persistence.ClaimHandleInsertInput{
		ID:                 rowID,
		NodeRunID:          &dispatchID,
		LockKind:           persistence.LockKindScope,
		ProducerName:       &producerNameCopy,
		ClaimScopeData:     scopeInitial,
		Intent:             &intentCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ExpiresAt:          args.Clock.Now().Add(5 * heartbeatInterval),
		FrameID:            &frameID,
		IsHeld:             isHeld,
	}
	if err := args.ClaimHandles.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: Insert: %w", err)
	}

	claimID := locks.ClaimID(rowID.String())
	outcome, err := s.Open(ctx, claimID, spec)
	if err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: Open(%s): %w", spec.ProducerName, err)
	}
	// Producer has nothing to give right now (e.g. drained items-table
	// queue). The producer signals this via OpenOutcome.Available=false.
	// Distinguished from openResultBail so the outer caller can route
	// through the on_acquire_unavailable handler (default = silent
	// retry preserving today's behavior).
	if !outcome.Available {
		// Producer signalled unavailable — count as a resolved
		// acquisition outcome with intent="unavailable".
		metricsOf(args).IncClaimAcquisition(spec.ProducerName, "unavailable")
		metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())
		return AcquiredLock{}, openResultUnavailable, nil
	}
	cr := outcome.Result

	if err := args.ClaimHandles.UpdateAddress(ctx, rowID, args.SupervisorID, cr.Address, tx); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateAddress: %w", err)
	}
	// Pick-policy claims have store-chosen claim-scope; scoped claims
	// keep the substituted selector (already written above).
	if len(cr.ClaimScope) > 0 && string(cr.ClaimScope) != string(scopeInitial) {
		if err := args.ClaimHandles.UpdateClaimScope(ctx, rowID, args.SupervisorID, cr.ClaimScope, tx); err != nil {
			return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateClaimScope: %w", err)
		}
	}
	// Persist the per-claim RealizedWriteSemantics returned by the
	// producer. Required for the in-Go scope-conflict check on
	// subsequent acquisitions; per the uniformity invariant (§2.5) all
	// byte-equal-Scope claims must share this value.
	if cr.RealizedWriteSemantics != "" {
		if err := args.ClaimHandles.UpdateRealizedWriteSemantics(ctx, rowID, args.SupervisorID, string(cr.RealizedWriteSemantics), tx); err != nil {
			return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateRealizedWriteSemantics: %w", err)
		}
	}

	if err := insertHeldClaimHoldersAtAcquire(ctx, args, tx, rowID, cand, spec.Alias, heldSubgraphs); err != nil {
		return AcquiredLock{}, openResultBail, err
	}

	metricsOf(args).IncClaimAcquisition(spec.ProducerName, "acquired")
	metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())

	return AcquiredLock{
		Spec:          spec,
		ClaimHandleID: rowID,
		ClaimResult:   cr,
		Producer:      s,
		Alias:         spec.Alias,
		IsHeld:        isHeld,
	}, openResultAcquired, nil
}

// evaluateClaimScopeConflict re-loads existing claim-scope holders for the
// store and runs ClaimScopesByteEqual ∧ ModeCoexists against the candidate
// spec. Skips own-node rows. Returns true if any holder conflicts AND
// the modes don't coexist.
//
// Per spec §7.7: byte-equal comparison; the producer canonicalizes its
// claim-scope bytes such that two claims that should conflict produce
// byte-equal claim-scopes. The candidate's pre-Open claim-scope is the
// substituted-selector bytes (scoped claims) — for pick-policy
// claims the actual collision check happens in the producer's own
// internal serialization.
//
// Per the uniformity invariant (spec §2.5) all byte-equal-ClaimScope
// claims share identical RealizedWriteSemantics. The conflict check
// uses the holder's recorded RealizedWriteSemantics for both sides.
func evaluateClaimScopeConflict(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.ClaimSpec, cand persistence.Candidate,
) (bool, error) {
	holders, err := args.ClaimHandles.ListByProducerClaimScope(ctx, spec.ProducerName, tx)
	if err != nil {
		return false, fmt.Errorf("evaluateClaimScopeConflict: ListByProducerClaimScope: %w", err)
	}
	candidateScope, err := json.Marshal(spec.Selector)
	if err != nil {
		return false, err
	}
	for _, h := range holders {
		// Same-node-skip note: this branch only takes effect on still-
		// active claims that this supervisor is in the middle of
		// acquiring inside the current in-flight transaction
		// (`HolderSupervisorID == args.SupervisorID` matches the
		// in-flight INSERT). Committed-durable rows
		// (state='committed' AND lifetime='durable') correctly DO
		// surface in conflict detection across the Promote boundary —
		// an asset belongs to its scope until explicit Release, so the
		// post-Promote CHECK constraint nulls `holder_supervisor_id`
		// and the equality test naturally fails. A same-node retry
		// through an existing durable asset is a no-op the caller must
		// handle, not a scope conflict to skip — the listing query
		// (`ListByProducerClaimScope`) returns both active and
		// committed-durable rows for exactly this reason.
		if h.HolderNodeID == cand.NodeID && h.HolderSupervisorID != nil && *h.HolderSupervisorID == args.SupervisorID {
			continue
		}
		if !locks.ClaimScopesByteEqual(candidateScope, h.ClaimScopeData) {
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
