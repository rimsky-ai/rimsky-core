// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type TerminalSource int

const (
	ActiveTerminal TerminalSource = iota

	HeldTerminal

	OwnershipBail
)

type TerminalOutcome string

const (
	OutcomeCommit TerminalOutcome = "commit"

	OutcomeAbandon TerminalOutcome = "abandon"

	OutcomeAbandonSiblingCancel TerminalOutcome = "abandon_sibling_cancel"

	OutcomeAbandonDescendantCancel TerminalOutcome = "abandon_descendant_cancel"
)

func (o TerminalOutcome) IsAbandon() bool { return o != OutcomeCommit }

func (o TerminalOutcome) valid() bool {
	switch o {
	case OutcomeCommit, OutcomeAbandon, OutcomeAbandonSiblingCancel, OutcomeAbandonDescendantCancel:
		return true
	}
	return false
}

func (o TerminalOutcome) CauseString() string {
	switch o {
	case OutcomeAbandon:
		return "natural"
	case OutcomeAbandonSiblingCancel:
		return "sibling_failed"
	case OutcomeAbandonDescendantCancel:
		return "parent_resolved"
	}
	return ""
}

type TerminalDecision struct {
	ClaimHandleID shared.UUID

	SupervisorID string

	Source TerminalSource

	Outcome TerminalOutcome

	Producer locks.ClaimProducer

	Scope   []byte
	Address []byte

	LeaseToken string

	// @concept: claim-lifetime
	Lifetime spec.ClaimLifetime

	CandidateHandle []byte

	ProducerName string

	LineageHint ClaimLineageHint

	ParentClaimHandleID *shared.UUID

	LineageParentClaimHandleID *shared.UUID
}

type ClaimLineageHint struct {
	InstanceID   shared.UUID
	FrameID      shared.UUID
	NodeRunID    shared.UUID
	NodeID       shared.UUID
	ProducerName string
	VersionID    string
}

func acquirerInstanceID(
	ctx context.Context, args RunArgs, holderNodeID shared.UUID, tx persistence.Tx,
) (shared.UUID, error) {
	acquirer, err := args.Persist.Nodes().Get(ctx, holderNodeID, tx)
	if err != nil {
		return shared.UUID{}, fmt.Errorf("acquirerInstanceID: Nodes().Get(%s): %w", holderNodeID, err)
	}
	if acquirer == nil {
		return shared.UUID{}, fmt.Errorf("acquirerInstanceID: node %s not found", holderNodeID)
	}
	return acquirer.InstanceID, nil
}

func ResolveClaimHandleTerminal(
	ctx context.Context, args RunArgs, td TerminalDecision, tx persistence.Tx,
) (postCommitFn, error) {
	if td.Producer == nil {
		return nil, fmt.Errorf("ResolveClaimHandleTerminal: producer is nil for claim_handle %s", td.ClaimHandleID)
	}
	if !td.Outcome.valid() {
		return nil, fmt.Errorf("ResolveClaimHandleTerminal: unknown terminal outcome %q for claim_handle %s",
			td.Outcome, td.ClaimHandleID)
	}
	candidateMetadata, err := dispatchDataProcessingTerminal(ctx, args, td, tx)
	if err != nil {
		return nil, err
	}
	pendingLineage := emitTerminalForensics(ctx, args, td, tx)
	// @concept: terminal-resolution
	if err := enqueueProducerVerb(ctx, args, td, pendingLineage, tx); err != nil {
		return nil, fmt.Errorf("ResolveClaimHandleTerminal: %w", err)
	}
	post := kickProducerVerbDispatch(args)
	if td.Outcome.IsAbandon() {
		pc, err := cancelDescendantClaims(ctx, args, td.ClaimHandleID, tx)
		if err != nil {
			return nil, fmt.Errorf("ResolveClaimHandleTerminal: cancelDescendantClaims: %w", err)
		}
		post = chainPostCommit(post, pc)
	}
	// @decision: fold-ownership-bail
	if td.Source == OwnershipBail {
		if err := args.ClaimHandles.Delete(ctx, td.ClaimHandleID, td.SupervisorID, tx); err != nil {
			return nil, fmt.Errorf("ResolveClaimHandleTerminal: ownership-bail Delete: %w", err)
		}
	} else if err := promoteHandleState(ctx, args, td, tx); err != nil {
		return nil, err
	}
	// @concept: claim-handle
	// @decision: held-as-state-not-phase
	heldPC, err := emitDeferredHeldCascade(ctx, args, td, tx)
	if err != nil {
		return nil, fmt.Errorf("ResolveClaimHandleTerminal: deferred held cascade: %w", err)
	}
	post = chainPostCommit(post, heldPC)
	// @concept: claim-tree
	// @concept: cancel-siblings
	if td.ParentClaimHandleID == nil {
		return post, nil
	}
	settlePC, err := SettleFromFanoutChild(ctx, args, FanoutChildSettlementInput{
		ParentClaimHandleID: *td.ParentClaimHandleID,
		ChildClaimHandleID:  td.ClaimHandleID,
		ChildOutcome:        td.Outcome,
		// @concept: data-processing
		ChildProducerMetadata: candidateMetadata,
	}, tx)
	if err != nil {
		return nil, fmt.Errorf("ResolveClaimHandleTerminal: settle children: %w", err)
	}
	post = chainPostCommit(post, settlePC)
	return post, nil
}

// @concept: data-processing
func dispatchDataProcessingTerminal(
	ctx context.Context, args RunArgs, td TerminalDecision, tx persistence.Tx,
) ([]byte, error) {
	if len(td.CandidateHandle) == 0 || td.ProducerName == "" || args.DataProcessors == nil {
		return nil, nil
	}
	dp, ok := args.DataProcessors.Get(td.ProducerName)
	if !ok {
		return nil, nil
	}
	if td.Outcome == OutcomeCommit {
		out, cErr := dp.CommitCandidate(ctx, CommitCandidateInput{
			ProducerName:    td.ProducerName,
			ClaimHandleID:   td.ClaimHandleID.String(),
			CandidateHandle: td.CandidateHandle,
		})
		if cErr != nil {
			return nil, fmt.Errorf("ResolveClaimHandleTerminal: CommitCandidate(%s): %w",
				td.ProducerName, cErr)
		}
		return out.CandidateMetadata, nil
	}
	if td.Outcome.IsAbandon() {
		if err := dp.AbandonCandidate(ctx, AbandonCandidateInput{
			ProducerName:    td.ProducerName,
			ClaimHandleID:   td.ClaimHandleID.String(),
			CandidateHandle: td.CandidateHandle,
		}); err != nil {
			return nil, fmt.Errorf("ResolveClaimHandleTerminal: AbandonCandidate(%s): %w",
				td.ProducerName, err)
		}
	}
	return nil, nil
}

func promoteHandleState(
	ctx context.Context, args RunArgs, td TerminalDecision, tx persistence.Tx,
) error {
	promoteState := spec.ClaimHandleStateCommitted
	if td.Outcome.IsAbandon() {
		promoteState = spec.ClaimHandleStateAbandoned
	}
	err := args.ClaimHandles.Promote(ctx, td.ClaimHandleID, td.SupervisorID, promoteState, tx)
	if err == nil {
		return nil
	}
	if errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		if args.Logger != nil {
			args.Logger.Warn("CLAIMHANDLE.PROMOTION.RACED", "site", "ResolveClaimHandleTerminal", "detail", "the handle is already resolved or the supervisor does not match",
				"claim_handle_id", td.ClaimHandleID.String(),
				"new_state", string(promoteState))
		}
		return nil
	}
	return fmt.Errorf("ResolveClaimHandleTerminal: Promote: %w", err)
}
