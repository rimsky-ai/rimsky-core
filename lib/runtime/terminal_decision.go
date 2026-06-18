// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type TerminalSource int

const (
	ActiveTerminal TerminalSource = iota

	HeldTerminal

	OwnershipBail
)

type AggregateOutcome int

const (
	AggregateCommit AggregateOutcome = iota

	AggregateAbandon
)

type TerminalCause string

const (
	TerminalCauseNatural TerminalCause = "natural"

	TerminalCauseSiblingCancel TerminalCause = "sibling_cancel"

	TerminalCauseDescendantCancel TerminalCause = "descendant_cancel"
)

type TerminalDecision struct {
	ClaimHandleID shared.UUID

	SupervisorID string

	Source TerminalSource

	Outcome AggregateOutcome

	Producer locks.ClaimProducer

	Scope   []byte
	Address []byte

	// @concept: claim-lifetime
	Lifetime spec.ClaimLifetime

	CandidateHandle []byte

	ProducerName string

	LineageHint ClaimLineageHint

	ParentClaimHandleID *shared.UUID

	Cause TerminalCause
}

type ClaimLineageHint struct {
	InstanceID   shared.UUID
	FrameID      shared.UUID
	RunID        shared.UUID
	NodeID       shared.UUID
	ProducerName string
	VersionID string
}

func ResolveClaimHandleTerminal(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	td TerminalDecision,
) error {
	if td.Producer == nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: producer is nil for claim_handle %s", td.ClaimHandleID)
	}
	versionID, err := dispatchDataProcessingTerminal(ctx, args, tx, td)
	if err != nil {
		return err
	}
	commitRes, err := fireProducerVerb(ctx, td)
	if err != nil {
		return err
	}
	if td.Outcome == AggregateCommit && commitRes.VersionID != "" {
		if err := args.ClaimHandles.SetVersionID(ctx, td.ClaimHandleID, td.SupervisorID, commitRes.VersionID, tx); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: SetVersionID (base Commit response): %w", err)
		}
		versionID = commitRes.VersionID
	}
	emitTerminalForensics(ctx, args, tx, td, versionID)
	if td.Outcome == AggregateAbandon {
		if err := cancelDescendantClaims(ctx, args, tx, td.ClaimHandleID); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: cancelDescendantClaims: %w", err)
		}
	}
	if td.Source == OwnershipBail {
		if err := args.ClaimHandles.Delete(ctx, td.ClaimHandleID, td.SupervisorID, tx); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: ownership-bail Delete: %w", err)
		}
	} else if err := promoteHandleState(ctx, args, tx, td); err != nil {
		return err
	}
	// @concept: claim-tree
	// @concept: cancel-siblings
	// @constraint: child settlement (counter bump + sibling cancel +
	// parent walk) routes through the unified settle-children primitive
	// and is a no-op for root claims (td.ParentClaimHandleID == nil).
	// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Recursive claim-tree resolution + §State aggregation rules +
	// §Error policy / `strict.cancel_siblings: true`.
	if td.ParentClaimHandleID == nil {
		return nil
	}
	if err := SettleChildren(ctx, args, tx, ChildSettlementInput{
		ParentClaimHandleID:   *td.ParentClaimHandleID,
		ChildClaimHandleID:    td.ClaimHandleID,
		ChildOutcome:          td.Outcome,
		ChildProducerMetadata: commitRes.ProducerMetadata,
	}); err != nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: settle children: %w", err)
	}
	return nil
}

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

func fireProducerVerb(ctx context.Context, td TerminalDecision) (claimproducer.CommitResult, error) {
	claimID := claimproducer.ClaimID(td.ClaimHandleID.String())
	producerName := td.ProducerName
	if producerName == "" && td.Producer != nil {
		producerName = td.Producer.Name()
	}
	ctx = peer.WithServiceName(ctx, producerName)
	var commitRes claimproducer.CommitResult
	var verbErr error
	switch td.Outcome {
	case AggregateCommit:
		commitRes, verbErr = td.Producer.Commit(ctx, claimID, td.Scope, td.Address)
	case AggregateAbandon:
		verbErr = abandonOpenedClaim(ctx, td.Producer, td.ClaimHandleID, td.Scope, td.Address)
	default:
		return claimproducer.CommitResult{}, fmt.Errorf("ResolveClaimHandleTerminal: unknown outcome %v", td.Outcome)
	}
	if verbErr != nil {
		return claimproducer.CommitResult{}, fmt.Errorf("ResolveClaimHandleTerminal: producer verb (%s, source=%d): %w",
			outcomeVerbName(td.Outcome), td.Source, verbErr)
	}
	return commitRes, nil
}

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

