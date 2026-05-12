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
//  3. Delete the rimsky_claim_handle row claimant-guarded (foundation
//     invariant 4).
//
// This file packages those three steps as ResolveClaimHandleTerminal
// so the two source paths share a single audited implementation.
//
// Foundation invariants preserved:
//   - Inv 4 (claimant-guarded release): the Delete is gated on
//     supervisor_id.
//   - Inv 13 (single auto-terminal): callers serialize via SELECT …
//     FOR UPDATE on the claim_handle row before invoking; this engine
//     does not re-take the lock.
//   - Inv 20 (claim content inert): scope/address pass through opaque;
//     no logging, no transformation.

package integration

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
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

// TerminalDecision is the input to ResolveClaimHandleTerminal.
type TerminalDecision struct {
	// ClaimHandleID is the rimsky_claim_handle row to resolve.
	ClaimHandleID shared.UUID

	// SupervisorID guards the claimant-side Delete (foundation invariant 4).
	SupervisorID string

	// Source distinguishes the call site for logging / metrics.
	Source TerminalSource

	// Outcome drives the producer verb selection.
	Outcome AggregateOutcome

	// Producer is the ClaimProducer client to dial. Either supplied by
	// the caller (active path: lk.Store from the AcquiredLock) or
	// looked up by the caller from the registry (held path: by
	// claim_handle.store_name).
	Producer locks.ClaimProducer

	// Scope and Address are the producer-supplied bytes from the
	// claim_handle row. Inert in rimsky per invariant 20.
	Scope   []byte
	Address []byte
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
	claimID := locks.ClaimID(td.ClaimHandleID.String())
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
	if err := args.ClaimHandles.Delete(ctx, td.ClaimHandleID, td.SupervisorID, tx); err != nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: Delete: %w", err)
	}
	return nil
}

// outcomeVerbName maps AggregateOutcome to the producer-verb name for
// error messages.
func outcomeVerbName(o AggregateOutcome) string {
	switch o {
	case AggregateCommit:
		return "Commit"
	case AggregateAbandon:
		return "Abandon"
	}
	return "Unknown"
}
