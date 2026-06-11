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
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	fspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// acquireClaim runs the claim-acquisition steps per spec §7.3 step 4.
//
// Conflict detection is producer-aware per @blessed-invariant 4b: when
// the resolved producer advertises SupportsScopesConflict, rimsky
// consults the producer's own ScopesConflict predicate (which may treat
// non-byte-equal scopes — e.g. prefix-overlapping ones — as conflicting);
// when it does not, rimsky falls back to byte-equal comparison on scope
// bytes (per spec §7.7). The candidate's pre-Open scope is the
// substituted-selector bytes. For pick-policy claims the store's
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
	ctx context.Context, args RunArgs, tx persistence.Tx, instanceID shared.UUID,
	spec claimproducer.ClaimSpec, cand persistence.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, openResult, error) {
	// Latency timer for `rimsky_claim_acquisition_latency_seconds`. Start
	// the clock at the top of the function so the histogram includes the
	// pre-Open advisory-lock + scope-conflict check; observe only on
	// resolved outcomes (acquired / unavailable).
	acquireStart := args.Clock.Now()
	// Dispatch-time claim-producer resolution: late-bind-aware so a
	// per-instance service binding routes to the configured proxy. Falls
	// through to a bare Get when no instance context / no late-bind config.
	s, ok := args.StoreRegistry.GetWithContext(ctx, spec.ProducerName, instanceID.String())
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
	// Pre-Open conflict check: any existing conflicting holder must permit
	// our intent under its own RealizedWriteSemantics. The conflict
	// predicate is producer-aware (@blessed-invariant 4b) — byte-equal by
	// default, or the producer's own ScopesConflict when advertised. Per
	// the uniformity invariant (spec §2.5), all conflicting claims share
	// identical semantics, so the candidate's effective semantics on a
	// match equals the holder's recorded value.
	//
	// When the conflicting holder is a committed-durable row (the asset
	// surface; concept:claim-lifetime invariant "Conflict detection
	// includes committed-durable rows"), the conflict is permanent for
	// the duration of the holder's asset lifetime — retrying the
	// scope-conflict bail forever would silently stall the node. Instead
	// surface the conflict as openResultUnavailable so it routes through
	// the operator's `error_types: { acquire/unavailable: ... }` chain
	// (defaulting to give_up when no policy is declared), matching the
	// shape `concept:asset` and `concept:claim-lifetime` require:
	// "competing acquirer against the same scope hits
	// terminal/error/acquire/unavailable while the row is
	// committed-durable."
	//
	// Active conflicts (a still-in-flight acquirer) keep the bail
	// shape — the holder may release on its own terminal and the
	// scheduler tick legitimately retries.
	//
	// @story: claim-handoff-durable
	conflicted, persistent, err := evaluateClaimScopeConflict(ctx, args, tx, s, spec, cand)
	if err != nil {
		return AcquiredLock{}, openResultBail, err
	}
	if conflicted {
		if persistent {
			// Surface as unavailable so the operator's error_types chain
			// routes the durable-conflict. UnavailableClass empty → the
			// synthetic "acquire/unavailable" leaf the chain keys on.
			// Count as a resolved acquisition outcome with
			// intent="unavailable" — the durable-conflict terminates the
			// tick into the error-policy chain (not a retry-bail), so it
			// is symmetric with the producer-returned-unavailable branch
			// below at IncClaimAcquisition / ObserveClaimAcquisitionLatency.
			metricsOf(args).IncClaimAcquisition(spec.ProducerName, "unavailable")
			metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())
			return AcquiredLock{}, openResultUnavailable, nil
		}
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
		// Thread the template store-ref's lifetime hint onto the persisted
		// row. spec.Lifetime is the rimsky-internal plain-string carried by
		// the ClaimSpec (lib/protocols may not import lib/foundation/spec);
		// convert at this persistence boundary. Empty → the persistence layer
		// defaults to "subgraph". @concept: claim-lifetime
		Lifetime: fspec.ClaimLifetime(spec.Lifetime),
	}
	if err := args.ClaimHandles.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: Insert: %w", err)
	}

	claimID := claimproducer.ClaimID(rowID.String())
	// Stamp the producer name so a host-agent-proxy fronting the
	// claim-producer protocol can route this Open by service name.
	openCtx := peer.WithServiceName(ctx, spec.ProducerName)
	outcome, err := s.Open(openCtx, claimID, spec)
	if err != nil {
		// A producer-side wire fault (the gRPC Open call returned an
		// error) carries a translated error_class on *peer.ProducerCallError.
		// Surface it as openResultErrored so tryAcquire routes the class
		// through the operator's `error_types:` chain rather than aborting
		// the tick (openResultBail). Errors that are not producer-call
		// faults (none today on this line, defense-in-depth) still abort.
		var pcErr *peer.ProducerCallError
		if errors.As(err, &pcErr) {
			return AcquiredLock{}, openResultErrored, pcErr
		}
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: Open(%s): %w", spec.ProducerName, err)
	}
	// Producer has nothing to give right now (e.g. drained items-table
	// queue). The producer signals this via OpenOutcome.Available=false.
	// Distinguished from openResultBail so the outer caller can route
	// through the operator's `error_types: { acquire/unavailable:
	// ... }` chain (default = give_up("unknown_error_class") absent
	// an operator-declared policy).
	if !outcome.Available {
		// Producer signalled unavailable — count as a resolved
		// acquisition outcome with intent="unavailable".
		metricsOf(args).IncClaimAcquisition(spec.ProducerName, "unavailable")
		metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())
		// Carry the producer-declared acquisition-failure class (when the
		// producer named one) out of this branch so tryAcquire can stamp it
		// onto the acquisition and handleAcquireUnavailable keys the
		// operator's `error_types:` chain on it. Empty → synthetic
		// "acquire/unavailable" routing, unchanged.
		return AcquiredLock{UnavailableClass: outcome.UnavailableClass}, openResultUnavailable, nil
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
	// Persist the producer-supplied capture-time Payload so downstream
	// co-holders' `{{claim.<alias>.payload.<f>}}` substitution can read
	// the same bytes at their own acquire-tx. Inert in rimsky per
	// @blessed-invariant 20. Empty payload → no UPDATE (the column
	// stays NULL; the substitution returns ErrMissingSource which is
	// the correct outcome when the producer didn't return one).
	//
	// @concept: claim-co-holdership
	if len(cr.Payload) > 0 {
		if err := args.ClaimHandles.UpdatePayload(ctx, rowID, args.SupervisorID, cr.Payload, tx); err != nil {
			return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdatePayload: %w", err)
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
// store and runs scopesConflict ∧ ModeCoexists against the candidate spec.
// Skips own-node rows. Returns (conflicted, persistent, err): `conflicted`
// = any holder conflicts AND the modes don't coexist; `persistent` = AT
// LEAST ONE such conflicting holder is a committed-durable row (the
// asset surface; won't release on its own). Iterating all conflicting
// holders rather than returning on the first lets a durable-committed
// conflict ANY of them mark the conflict as persistent — without this
// OR-fold, a still-active conflicting acquirer that happens to sort
// before a committed-durable holder on `claimed_at ASC` would mask the
// durable conflict and send the node into retry-bail forever even
// though the durable row guarantees the conflict can never clear without
// operator action. Active-only conflicts (no durable holder among the
// rejecters) keep `persistent=false` so the caller's retry-bail shape
// keeps the scheduler legitimately polling for the active holder's
// terminal.
//
// @blessed-invariant 4b: single-writer-per-scope, producer-aware overlap.
// The scope-overlap predicate is byte-equal by default (spec §7.7 — the
// producer canonicalizes its claim-scope bytes so two claims that should
// conflict produce byte-equal claim-scopes), but a producer advertising
// SupportsScopesConflict defines overlap non-trivially: rimsky consults
// the producer's ScopesConflict so two writers whose scopes overlap (e.g.
// prefix-containment) but are NOT byte-equal still conflict. The
// candidate's pre-Open claim-scope is the substituted-selector bytes
// (scoped claims) — for pick-policy claims the actual collision check
// happens in the producer's own internal serialization.
//
// Per the uniformity invariant (spec §2.5) all conflicting claims share
// identical RealizedWriteSemantics. The conflict check uses the holder's
// recorded RealizedWriteSemantics for both sides.
//
// @story: claim-handoff-durable
func evaluateClaimScopeConflict(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	s claimproducer.ClaimProducer, spec claimproducer.ClaimSpec, cand persistence.Candidate,
) (bool, bool, error) {
	holders, err := args.ClaimHandles.ListByProducerClaimScope(ctx, spec.ProducerName, tx)
	if err != nil {
		return false, false, fmt.Errorf("evaluateClaimScopeConflict: ListByProducerClaimScope: %w", err)
	}
	candidateScope, err := json.Marshal(spec.Selector)
	if err != nil {
		return false, false, err
	}
	// Resolve the producer's conflict capability ONCE per evaluation. When
	// advertised, each holder comparison routes through the producer's own
	// ScopesConflict; otherwise byte-equal is the trivial default
	// (@blessed-invariant 4b).
	caps, err := s.Capabilities(ctx)
	if err != nil {
		return false, false, fmt.Errorf("evaluateClaimScopeConflict: Capabilities(%s): %w", spec.ProducerName, err)
	}
	// Track whether ANY conflicting holder is committed-durable so the
	// returned `persistent` reflects the worst case across the holder
	// set, not just the first one inspected.
	var (
		sawConflict             bool
		sawDurableCommittedConf bool
	)
	for _, h := range holders {
		// Same-node-skip note: this branch only takes effect on still-
		// active claims that this supervisor is in the middle of
		// acquiring inside the current in-flight transaction
		// (`HolderSupervisorID == args.SupervisorID` matches the
		// in-flight INSERT).
		if h.HolderNodeID == cand.NodeID && h.HolderSupervisorID != nil && *h.HolderSupervisorID == args.SupervisorID {
			continue
		}
		// Same-node re-materialization of an existing durable asset:
		// a row that has already promoted to (state='committed',
		// lifetime='durable') and whose holder_node_id equals the
		// candidate's node is an asset belonging to THIS node. The
		// operator's `POST /instances/{id}/assets/{alias}/materialize`
		// path (and any other invalidate-driven re-dispatch of the
		// producing node) MUST be able to re-acquire — the spec story
		// "triggering a re-materialization causes the supervisor to
		// dispatch the producing node again" turns on this allowing.
		// Treat the prior asset row as same-node and skip the conflict
		// check; the re-dispatch creates a fresh active row, drives to
		// terminal, and Promotes that row to a new committed-durable
		// asset row alongside the prior one (each materialization is
		// its own asset row, mapping naturally to the per-claim-handle
		// version-history and materialization-history surfaces). Cross-
		// node acquisitions of the same scope still conflict — the
		// guard is HolderNodeID-equality, not a blanket skip.
		// @concept: asset
		if h.HolderNodeID == cand.NodeID &&
			h.State == fspec.ClaimHandleStateCommitted &&
			h.Lifetime == fspec.ClaimLifetimeDurable {
			continue
		}
		// @blessed-invariant 4b: producer-aware scope-overlap. A producer
		// advertising SupportsScopesConflict owns the overlap predicate
		// (e.g. prefix-containment), so a non-byte-equal-but-overlapping
		// holder still conflicts; otherwise byte-equal is the trivial
		// default.
		conflicts, cErr := scopesConflict(ctx, s, caps, candidateScope, h.ClaimScopeData)
		if cErr != nil {
			return false, false, fmt.Errorf("evaluateClaimScopeConflict: ScopesConflict(%s): %w", spec.ProducerName, cErr)
		}
		if !conflicts {
			continue
		}
		var holderIntent claimproducer.Intent
		if h.Intent != nil {
			holderIntent = claimproducer.Intent(*h.Intent)
		}
		holderRWS := claimproducer.WriteSemantics(h.RealizedWriteSemantics)
		// By the uniformity invariant the candidate's realized semantics
		// (post-Open) MUST match the holder's; we use the holder's
		// recorded value for both sides of the matrix.
		if !locks.ModeCoexists(spec.Intent, holderRWS, holderIntent, holderRWS) {
			// At least one conflicting holder. Persistent iff THIS holder
			// (or any prior in the loop) is the asset surface (committed-
			// durable): the row won't release on its own, so the conflict
			// cannot be cleared by waiting on the holder's terminal —
			// the operator's `error_types: { acquire/unavailable: ... }`
			// chain decides. We must keep iterating after the first
			// rejection: an active-conflicting row may sort before a
			// later durable-committed conflicting row by `claimed_at
			// ASC`, and a first-rejection-wins shape would mask the
			// durable conflict.
			sawConflict = true
			if h.State == fspec.ClaimHandleStateCommitted && h.Lifetime == fspec.ClaimLifetimeDurable {
				sawDurableCommittedConf = true
			}
		}
	}
	return sawConflict, sawDurableCommittedConf, nil
}

// scopesConflict reports whether two claim-scope byte strings conflict
// under @blessed-invariant 4b. The predicate is producer-aware: a producer
// that advertises SupportsScopesConflict owns the overlap definition (it
// may treat non-byte-equal scopes — e.g. prefix-overlapping ones — as
// conflicting), so rimsky delegates to its ScopesConflict; a producer that
// does not advertise keeps the trivial byte-equal default (spec §7.7).
//
// caps is resolved once by the caller and passed in so a tight conflict
// loop does not re-fetch capabilities per holder. Shared by the top-level
// acquisition path (evaluateClaimScopeConflict) and the fan-out sub-claim
// path (AcquireSubClaims).
func scopesConflict(
	ctx context.Context, s claimproducer.ClaimProducer, caps claimproducer.Capabilities, a, b []byte,
) (bool, error) {
	if !caps.SupportsScopesConflict {
		return locks.ClaimScopesByteEqual(a, b), nil
	}
	return s.ScopesConflict(ctx, a, b)
}
