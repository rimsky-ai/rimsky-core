// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unified terminal-decision engine — Phase 6 of the
// layer-crystallization plan. Both the executor-terminal path
// (releaseClaim's non-held branch in runner_terminal.go) and the
// auto-terminal path (CheckAndFireResolution in auto_terminal.go) end
// in the same three-step sequence:
//
//  1. Determine aggregate outcome (success → Commit; failure → Abandon).
//  2. Fire the producer verb at-least-once (claim_id is the idempotency
//     key per foundation contract §4.4 / spec §7.8 obligation #3).
//  3. Promote the rimsky_claim_handles row to state='committed' /
//     state='abandoned', claimant-guarded (foundation invariant 4 in its
//     post-Stage-4 form: active-row mutations are claimant-guarded,
//     non-active rows are absence-guarded by the CHECK constraint that
//     nulls holder_supervisor_id at Promote). The carve-out paths
//     `abandonOpenedClaim` (called from `runner_lifecycle.go::
//     abandonPartialLocks` and `runner_acquire.go::handleOrphanedClaim`)
//     continue to Delete the row directly — those rows never went
//     through Promote, so the absence-guard contract doesn't apply.
//
// This file packages those three steps as ResolveClaimHandleTerminal
// so the two source paths share a single audited implementation.
//
// Foundation invariants preserved:
//   - Inv 4 (claimant-guarded release, post-Stage-4 form): active-row
//     Promote is gated on supervisor_id; the CHECK constraint
//     simultaneously nulls holder_supervisor_id so the post-Promote row
//     is absence-guarded for subsequent retention-sweep deletes.
//   - Inv 13 (single auto-terminal): callers serialize via SELECT …
//     FOR UPDATE on the claim_handle row before invoking; this engine
//     does not re-take the lock.
//   - Inv 20 (claim content inert): scope/address pass through opaque;
//     no logging, no transformation.

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/runtime/peer"
)

// TerminalSource discriminates the call site so logging and metrics
// can distinguish active-phase vs held-phase resolutions.
type TerminalSource int

const (
	// ActiveTerminal: the executor's terminal-event handler released a
	// non-held claim (spec §7.6).
	ActiveTerminal TerminalSource = iota

	// HeldTerminal: the auto-terminal mechanism fired at holding-
	// subgraph completion (spec §4.10 invariant 13).
	HeldTerminal
)

// AggregateOutcome is the success/failure binary rimsky carries across
// the wire. Per the 2026-04-30 stores-protocol cleanup, store
// disposition (commit vs release vs delete on the producer's own
// state) is governed by per-store config and not by template metadata.
type AggregateOutcome int

const (
	// AggregateCommit: every contributing terminal succeeded → Commit.
	AggregateCommit AggregateOutcome = iota

	// AggregateAbandon: at least one contributing terminal failed →
	// Abandon.
	AggregateAbandon
)

// TerminalCause discriminates how an Abandon resolution was reached so
// the lineage + events projections can distinguish a natural exhaustion
// (give_up, error policy, holder failure) from an operator- /
// sibling-driven force-cancel. Set on `TerminalDecision`; flows into the
// `claim_terminal` lineage row's `outcome` column + the
// `claim_resolution.abandon` event payload.
type TerminalCause string

const (
	// TerminalCauseNatural — the default. Covers AggregateCommit
	// resolutions and AggregateAbandon resolutions that fired because the
	// holding subgraph itself failed.
	TerminalCauseNatural TerminalCause = "natural"

	// TerminalCauseSiblingCancel — Abandon was forced by the parent's
	// `strict.cancel_siblings: true` walk in `cancelInFlightSiblings`.
	// The sibling is being cancelled because a peer sub-claim already
	// resolved as Abandon.
	TerminalCauseSiblingCancel TerminalCause = "sibling_cancel"

	// TerminalCauseDescendantCancel — Abandon was forced by
	// `cancelDescendantClaims`'s recursive descent: a parent row that is
	// itself resolving as Abandon walks its in-flight descendants and
	// fires force-Abandon on each before deleting itself.
	TerminalCauseDescendantCancel TerminalCause = "descendant_cancel"
)

// TerminalDecision is the input to ResolveClaimHandleTerminal.
type TerminalDecision struct {
	// ClaimHandleID is the rimsky_claim_handles row to resolve.
	ClaimHandleID shared.UUID

	// SupervisorID guards the claimant-side Delete (foundation invariant 4).
	SupervisorID string

	// Source distinguishes the call site for logging / metrics.
	Source TerminalSource

	// Outcome drives the producer verb selection.
	Outcome AggregateOutcome

	// Producer is the ClaimProducer client to dial. Either supplied by
	// the caller (active path: lk.Producer from the AcquiredLock) or
	// looked up by the caller from the registry (held path: by
	// claim_handle.producer_name).
	Producer locks.ClaimProducer

	// Scope and Address are the producer-supplied bytes from the
	// claim_handle row. Inert in rimsky per invariant 20.
	Scope   []byte
	Address []byte

	// Lifetime is the row's `lifetime` column ("subgraph" | "durable").
	// Post-Stage-4 of the claim-handle state-column refactor, every
	// terminal — durable or subgraph — runs the same Promote path
	// (committed | abandoned). Lifetime gates only the retention-sweep
	// eligibility downstream: subgraph rows are reaped at the configured
	// trailing window, while committed-durable rows persist as the asset
	// surface until released by `ReleaseHeldDurableClaims` (instance
	// termination) or the operator `DELETE /instances/{id}/assets/{alias}`
	// handler. Empty defaults to "subgraph". Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Held-durable claim lifecycle.
	//
	// @concept: claim-lifetime
	Lifetime spec.ClaimLifetime

	// CandidateHandle, when non-empty, signals that the row was
	// acquired against a `DataProcessing`-capable producer via
	// `BeginCandidate`. The engine routes the per-candidate disposition
	// (`CommitCandidate` / `AbandonCandidate`) via `args.DataProcessors`
	// BEFORE the standard `ClaimProducer.Commit` / `Abandon` so the
	// producer can stage the per-version metadata that the standard
	// verb then finalizes. Empty → no candidate dispatch fires (standard
	// non-data-processing path). Inert in rimsky per
	// @blessed-invariant 20-class.
	CandidateHandle []byte

	// ProducerName resolves the DataProcessing client for the
	// candidate dispatch above. Required when CandidateHandle is set;
	// ignored otherwise.
	ProducerName string

	// LineageHint carries the per-claim observability metadata the
	// engine needs to emit a `claim_terminal` lineage record at the
	// producer-verb fire. Optional — when unset (zero values), the
	// lineage write + claim_resolution.* event are skipped (used by
	// code paths that have already emitted their own lineage or have
	// nothing observable to record). Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Content lineage + the 2026-05-16 forensics extension.
	LineageHint ClaimLineageHint

	// ParentClaimHandleID, when non-nil, identifies the row's parent
	// claim handle in a sub-claim chain (fan-out / data-processing).
	// After the standard Delete path completes, the engine recurses
	// into `resolveParentClaimChain` so the parent's aggregation walk
	// fires regardless of whether the just-resolved sub-claim was
	// released via the held or the non-held branch. Without this
	// recurse, a fan-out child whose leaf alias is NON-held would
	// drop the sub-claim row via `releaseClaim`'s non-held branch and
	// the parent's `ListChildClaimHandles` walk would never see it,
	// stranding the parent's auto-terminal. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Recursive claim-tree resolution + §Fan-out template DSL.
	ParentClaimHandleID *shared.UUID

	// Cause discriminates Abandon provenance for forensics. Default
	// (`""` / `TerminalCauseNatural`) is the natural-exhaustion path;
	// the cancel walkers (`cancelInFlightSiblings` /
	// `cancelDescendantClaims`) set `TerminalCauseSiblingCancel` /
	// `TerminalCauseDescendantCancel` on the recursive
	// `ResolveClaimHandleTerminal` calls they fire. Ignored on
	// `AggregateCommit` (those always emit `outcome: committed`).
	Cause TerminalCause
}

// ClaimLineageHint carries the per-claim metadata the terminal-decision
// engine needs to emit a lineage record alongside the producer verb.
type ClaimLineageHint struct {
	InstanceID   shared.UUID
	FrameID      shared.UUID
	RunID        shared.UUID
	NodeID       shared.UUID
	ProducerName string
	// VersionID, when non-empty, is persisted on the lineage row's
	// payload. Populated on durable-claim Commit by the producer's
	// returned version identifier (col:rimsky_claim_handles.version_id).
	VersionID string
}

// ResolveClaimHandleTerminal fires the producer verb for the given
// outcome and deletes the claim_handle row claimant-guarded. Runs
// inside the caller's tx so the verb + the row delete commit
// atomically with whatever else the caller is mutating.
//
// Per spec §7.3 / foundation contract §4.4 the producer's verb runs
// in its own (producer-side) transaction; rimsky's bookkeeping tx
// commits the claim_handle DELETE independently. At-least-once
// delivery + claim_id idempotency on the producer side handles
// transient failures.
//
// The caller MUST have already serialized concurrent terminations on
// the same claim_handle (e.g. via SELECT … FOR UPDATE for held-phase
// resolutions, or via the per-row tx natural ordering for active-
// phase resolutions). This function does not re-take any lock.
func ResolveClaimHandleTerminal(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	td TerminalDecision,
) error {
	if td.Producer == nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: producer is nil for claim_handle %s", td.ClaimHandleID)
	}
	// 1. DataProcessing candidate dispatch (sub-claim path; no-op for
	//    non-DataProcessing producers). Returns the committed version id
	//    when present.
	versionID, err := dispatchDataProcessingTerminal(ctx, args, tx, td)
	if err != nil {
		return err
	}
	// 2. Producer verb dispatch (Commit / Abandon).
	if err := fireProducerVerb(ctx, td); err != nil {
		return err
	}
	// 3. Forensics emission (lineage row + claim_resolution event).
	emitTerminalForensics(ctx, args, tx, td, versionID)
	// 4. Recursive descendant cancellation on AggregateAbandon (BEFORE
	//    the row's own state transition).
	if td.Outcome == AggregateAbandon {
		if err := cancelDescendantClaims(ctx, args, tx, td.ClaimHandleID); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: cancelDescendantClaims: %w", err)
		}
	}
	// 5. Promote-not-delete (Stage 3 of the claim-handle state-column
	//    refactor).
	if err := promoteHandleState(ctx, args, tx, td); err != nil {
		return err
	}
	// 6. Parent-claim recursion (counter bump + sibling cancel + parent
	//    walk).
	return bumpParentAndRecurse(ctx, args, tx, td)
}

// dispatchDataProcessingTerminal handles the DataProcessing-candidate
// dispatch (sub-claim path). Fires BEFORE the standard ClaimProducer
// verb so the producer can stage the per-candidate version metadata
// that Commit then finalizes. Empty CandidateHandle / nil registry →
// no-op (standard non-DataProcessing path). Returns the version id
// from CommitCandidate (empty string if no candidate or Abandon).
// Spec §Protocol surfaces / DataProcessing.
func dispatchDataProcessingTerminal(
	ctx context.Context, args RunArgs, tx persistence.Tx, td TerminalDecision,
) (string, error) {
	if len(td.CandidateHandle) == 0 || td.ProducerName == "" || args.DataProcessors == nil {
		return "", nil
	}
	dp, ok := args.DataProcessors.Get(td.ProducerName)
	if !ok {
		return "", nil
	}
	switch td.Outcome {
	case AggregateCommit:
		cOut, cErr := dp.CommitCandidate(ctx, CommitCandidateInput{
			ProducerName:    td.ProducerName,
			ClaimHandleID:   td.ClaimHandleID.String(),
			CandidateHandle: td.CandidateHandle,
		})
		if cErr != nil {
			return "", fmt.Errorf("ResolveClaimHandleTerminal: CommitCandidate(%s): %w",
				td.ProducerName, cErr)
		}
		if cOut.VersionID != "" {
			if err := args.ClaimHandles.SetVersionID(ctx, td.ClaimHandleID, td.SupervisorID, cOut.VersionID, tx); err != nil {
				return "", fmt.Errorf("ResolveClaimHandleTerminal: SetVersionID: %w", err)
			}
		}
		return cOut.VersionID, nil
	case AggregateAbandon:
		if err := dp.AbandonCandidate(ctx, AbandonCandidateInput{
			ProducerName:    td.ProducerName,
			ClaimHandleID:   td.ClaimHandleID.String(),
			CandidateHandle: td.CandidateHandle,
		}); err != nil {
			return "", fmt.Errorf("ResolveClaimHandleTerminal: AbandonCandidate(%s): %w",
				td.ProducerName, err)
		}
	}
	return "", nil
}

// fireProducerVerb invokes the producer's Commit or Abandon (or routes
// through the carve-out abandonOpenedClaim helper). Returns a typed
// error on unknown outcomes or verb-side failures.
func fireProducerVerb(ctx context.Context, td TerminalDecision) error {
	claimID := locks.ClaimID(td.ClaimHandleID.String())
	// Stamp the producer name so a host-agent-proxy fronting the
	// claim-producer protocol can route this Commit/Abandon by service
	// name. Prefer td.ProducerName (the claim_handle's recorded name);
	// fall back to the client's own Name when unset.
	producerName := td.ProducerName
	if producerName == "" && td.Producer != nil {
		producerName = td.Producer.Name()
	}
	ctx = peer.WithServiceName(ctx, producerName)
	var verbErr error
	switch td.Outcome {
	case AggregateCommit:
		verbErr = td.Producer.Commit(ctx, claimID, td.Scope, td.Address)
	case AggregateAbandon:
		verbErr = abandonOpenedClaim(ctx, td.Producer, td.ClaimHandleID, td.Scope, td.Address)
	default:
		return fmt.Errorf("ResolveClaimHandleTerminal: unknown outcome %v", td.Outcome)
	}
	if verbErr != nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: producer verb (%s, source=%d): %w",
			outcomeVerbName(td.Outcome), td.Source, verbErr)
	}
	return nil
}

// promoteHandleState performs the Promote-not-delete state transition.
// Post-Stage-3 of the claim-handle state-column refactor, every
// terminal flips the row's state column rather than deleting it. The
// row is preserved past terminal for forensics and for asset-
// presentation queries (committed-durable case); the retention sweep
// reaps non-durable terminal rows at the configured cutoff.
//
// Carve-out paths (`runtime/abandon_claim.go::abandonOpenedClaim`
// called by `runner_lifecycle.go::abandonPartialLocks` and
// `runner_acquire.go::handleOrphanedClaim`) continue to Delete
// directly; those rows never went through Promote.
//
// `ErrIllegalClaimHandleTransition` signals a race (concurrent
// resolution or supervisor mismatch); log + continue (idempotent
// semantics, mirroring the pre-refactor no-op-on-claimant-mismatch
// behavior of `Delete`).
//
// @blessed-invariant 4 (post-refactor): active-row mutations are
// claimant-guarded.
func promoteHandleState(
	ctx context.Context, args RunArgs, tx persistence.Tx, td TerminalDecision,
) error {
	promoteState := spec.ClaimHandleStateCommitted
	if td.Outcome == AggregateAbandon {
		promoteState = spec.ClaimHandleStateAbandoned
	}
	err := args.ClaimHandles.Promote(ctx, td.ClaimHandleID, td.SupervisorID, promoteState, tx)
	if err == nil {
		return nil
	}
	if errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		if args.Logger != nil {
			args.Logger.Warn("ResolveClaimHandleTerminal: Promote raced (already resolved or supervisor mismatch)",
				"claim_handle_id", td.ClaimHandleID.String(),
				"new_state", string(promoteState))
		}
		return nil
	}
	return fmt.Errorf("ResolveClaimHandleTerminal: Promote: %w", err)
}

// bumpParentAndRecurse handles the parent-claim chain: bumps the
// parent's per-outcome counter, optionally fires `cancel_siblings`
// on AggregateAbandon under strict policy, and recurses into
// `resolveParentClaimChain`. No-op for root claims
// (`td.ParentClaimHandleID == nil`).
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Recursive claim-tree resolution + §State aggregation rules +
// §Error policy / `strict.cancel_siblings: true`.
//
// @concept: claim-tree
// @concept: cancel-siblings
func bumpParentAndRecurse(
	ctx context.Context, args RunArgs, tx persistence.Tx, td TerminalDecision,
) error {
	if td.ParentClaimHandleID == nil {
		return nil
	}
	outcomeKey := "commit"
	if td.Outcome == AggregateAbandon {
		outcomeKey = "abandon"
	}
	if err := args.ClaimHandles.BumpChildOutcomeCount(ctx, *td.ParentClaimHandleID, td.SupervisorID, outcomeKey, 1, tx); err != nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: BumpChildOutcomeCount: %w", err)
	}
	if td.Outcome == AggregateAbandon {
		if err := cancelInFlightSiblings(ctx, args, tx, *td.ParentClaimHandleID, td.ClaimHandleID); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: cancelInFlightSiblings: %w", err)
		}
	}
	if err := resolveParentClaimChain(ctx, args, tx, *td.ParentClaimHandleID, td.Outcome); err != nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: recurse parent: %w", err)
	}
	return nil
}

// `cancelInFlightSiblings` and `cancelDescendantClaims` (the
// strict-cancel walkers invoked from `ResolveClaimHandleTerminal`) live
// in `terminal_decision_cancel.go`.
//
// `outcomeVerbName`, `emitTerminalForensics`, `terminalOutcomeKey`, and
// `preferVersionID` (the forensics + observability emission helpers)
// live in `terminal_decision_forensics.go`.
