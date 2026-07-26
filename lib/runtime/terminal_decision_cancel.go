// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type cancelWalkOpts struct {
	outcome                    TerminalOutcome
	skip                       func(row persistence.ClaimHandleRow) bool
	parentClaimHandleID        func(row persistence.ClaimHandleRow) *shared.UUID
	lineageParentClaimHandleID *shared.UUID
}

// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelClaimHandleWalk(
	ctx context.Context, args RunArgs, rows []persistence.ClaimHandleRow, opts cancelWalkOpts, tx persistence.Tx,
) (postCommitFn, error) {
	var post postCommitFn
	for _, row := range rows {
		if opts.skip != nil && opts.skip(row) {
			continue
		}
		if row.State != spec.ClaimHandleStateActive {
			continue
		}
		if row.HolderSupervisorID == nil || *row.HolderSupervisorID != args.SupervisorID {
			continue
		}
		current, err := args.ClaimHandles.LockForUpdate(ctx, row.ID, tx)
		if err != nil {
			return nil, fmt.Errorf("cancelClaimHandleWalk: LockForUpdate %s: %w", row.ID, err)
		}
		if current == nil || current.State != spec.ClaimHandleStateActive {
			continue
		}
		producerName := ""
		if row.ProducerName != nil {
			producerName = *row.ProducerName
		}
		producer, ok, resolveErr := args.ClaimProducerRegistry.ResolveWithContext(ctx, producerName, "", tx)
		if resolveErr != nil {
			return nil, fmt.Errorf("cancelClaimHandleWalk: resolving producer %q for claim handle %s: %w",
				producerName, row.ID, resolveErr)
		}
		if !ok {
			return nil, fmt.Errorf("cancelClaimHandleWalk: unknown producer %q for claim handle %s",
				producerName, row.ID)
		}
		hint := ClaimLineageHint{
			ProducerName: producerName,
			VersionID:    row.VersionID,
			NodeID:       row.HolderNodeID,
		}
		if row.FrameID != nil {
			hint.FrameID = *row.FrameID
		}
		if row.NodeRunID != nil {
			hint.NodeRunID = *row.NodeRunID
		}
		instID, err := acquirerInstanceID(ctx, args, row.HolderNodeID, tx)
		if err != nil {
			return nil, fmt.Errorf("cancelClaimHandleWalk: %w", err)
		}
		hint.InstanceID = instID
		var parentClaimHandleID *shared.UUID
		if opts.parentClaimHandleID != nil {
			parentClaimHandleID = opts.parentClaimHandleID(row)
		}
		pc, err := ResolveClaimHandleTerminal(ctx, args, TerminalDecision{
			ClaimHandleID:              row.ID,
			SupervisorID:               args.SupervisorID,
			Source:                     HeldTerminal,
			Outcome:                    opts.outcome,
			Producer:                   producer,
			Scope:                      []byte(row.ClaimScopeData),
			Address:                    []byte(row.Address),
			LeaseToken:                 row.ProducerLeaseToken,
			Lifetime:                   row.Lifetime,
			CandidateHandle:            row.ProducerCandidateHandle,
			ProducerName:               producerName,
			LineageHint:                hint,
			ParentClaimHandleID:        parentClaimHandleID,
			LineageParentClaimHandleID: opts.lineageParentClaimHandleID,
		}, tx)
		if err != nil {
			return nil, fmt.Errorf("cancelClaimHandleWalk: force-Abandon %s: %w", row.ID, err)
		}
		post = chainPostCommit(post, pc)
	}
	return post, nil
}

// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelInFlightSiblings(
	ctx context.Context, args RunArgs, parentID shared.UUID, triggerID shared.UUID, tx persistence.Tx,
) (postCommitFn, error) {
	parent, err := args.ClaimHandles.Get(ctx, parentID, tx)
	if err != nil {
		return nil, fmt.Errorf("cancelInFlightSiblings: Get parent: %w", err)
	}
	if parent == nil {
		return nil, nil
	}
	if parent.State != spec.ClaimHandleStateActive {
		return nil, nil
	}
	policy, err := persistence.UnmarshalAggregationPolicy(parent.AggregationPolicy)
	if err != nil {
		if args.Logger != nil {
			args.Logger.Warn("cancelInFlightSiblings: malformed aggregation_policy on parent claim_handle; treating as non-strict",
				"parent_claim_handle_id", parentID.String(),
				"error", err.Error())
		}
		return nil, nil
	}
	if policy.Kind != spec.AggregationKindStrict {
		return nil, nil
	}
	siblings, err := args.ClaimHandles.ListChildClaimHandles(ctx, parentID, tx)
	if err != nil {
		return nil, fmt.Errorf("cancelInFlightSiblings: ListChildClaimHandles: %w", err)
	}
	return cancelClaimHandleWalk(ctx, args, siblings, cancelWalkOpts{
		outcome: OutcomeAbandonSiblingCancel,
		skip: func(row persistence.ClaimHandleRow) bool {
			return row.ID == triggerID
		},
		parentClaimHandleID: func(row persistence.ClaimHandleRow) *shared.UUID {
			return row.ParentClaimHandleID
		},
	}, tx)
}

// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelDescendantClaims(
	ctx context.Context, args RunArgs, rowID shared.UUID, tx persistence.Tx,
) (postCommitFn, error) {
	descendants, err := args.ClaimHandles.ListChildClaimHandles(ctx, rowID, tx)
	if err != nil {
		return nil, fmt.Errorf("cancelDescendantClaims: ListChildClaimHandles: %w", err)
	}
	return cancelClaimHandleWalk(ctx, args, descendants, cancelWalkOpts{
		outcome:                    OutcomeAbandonDescendantCancel,
		lineageParentClaimHandleID: &rowID,
	}, tx)
}
