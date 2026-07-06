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

// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelInFlightSiblings(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	parentID shared.UUID, triggerID shared.UUID,
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
			args.Logger.Warn("cancelInFlightSiblings: malformed aggregation_policy on parent claim_handle; treating as no cancel_siblings",
				"parent_claim_handle_id", parentID.String(),
				"error", err.Error())
		}
		return nil, nil
	}
	if policy.Kind != spec.AggregationKindStrict || !policy.CancelSiblings {
		return nil, nil
	}
	siblings, err := args.ClaimHandles.ListChildClaimHandles(ctx, parentID, tx)
	if err != nil {
		return nil, fmt.Errorf("cancelInFlightSiblings: ListChildClaimHandles: %w", err)
	}
	var post postCommitFn
	for _, sib := range siblings {
		if sib.ID == triggerID {
			continue
		}
		if sib.State != spec.ClaimHandleStateActive {
			continue
		}
		if sib.HolderSupervisorID == nil || *sib.HolderSupervisorID != args.SupervisorID {
			continue
		}
		current, err := args.ClaimHandles.LockForUpdate(ctx, sib.ID, tx)
		if err != nil {
			return nil, fmt.Errorf("cancelInFlightSiblings: LockForUpdate sibling %s: %w",
				sib.ID, err)
		}
		if current == nil || current.State != spec.ClaimHandleStateActive {
			continue
		}
		producerName := ""
		if sib.ProducerName != nil {
			producerName = *sib.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
		if !ok {
			return nil, fmt.Errorf("cancelInFlightSiblings: unknown producer %q for sibling %s",
				producerName, sib.ID)
		}
		hint := ClaimLineageHint{
			ProducerName: producerName,
			VersionID:    sib.VersionID,
			NodeID:       sib.HolderNodeID,
		}
		if sib.FrameID != nil {
			hint.FrameID = *sib.FrameID
		}
		if sib.NodeRunID != nil {
			hint.NodeRunID = *sib.NodeRunID
		}
		if acquirer, aErr := args.Persist.Nodes().Get(ctx, sib.HolderNodeID, tx); aErr == nil && acquirer != nil {
			hint.InstanceID = acquirer.InstanceID
		}
		pc, err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
			ClaimHandleID:       sib.ID,
			SupervisorID:        args.SupervisorID,
			Source:              HeldTerminal,
			Outcome:             OutcomeAbandonSiblingCancel,
			Producer:            producer,
			Scope:               []byte(sib.ClaimScopeData),
			Address:             []byte(sib.Address),
			Lifetime:            sib.Lifetime,
			CandidateHandle:     sib.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: sib.ParentClaimHandleID,
		})
		if err != nil {
			return nil, fmt.Errorf("cancelInFlightSiblings: force-Abandon sibling %s: %w",
				sib.ID, err)
		}
		post = chainPostCommit(post, pc)
	}
	return post, nil
}

// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelDescendantClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	rowID shared.UUID,
) (postCommitFn, error) {
	descendants, err := args.ClaimHandles.ListChildClaimHandles(ctx, rowID, tx)
	if err != nil {
		return nil, fmt.Errorf("cancelDescendantClaims: ListChildClaimHandles: %w", err)
	}
	var post postCommitFn
	for _, d := range descendants {
		if d.State != spec.ClaimHandleStateActive {
			continue
		}
		if d.HolderSupervisorID == nil || *d.HolderSupervisorID != args.SupervisorID {
			continue
		}
		current, err := args.ClaimHandles.LockForUpdate(ctx, d.ID, tx)
		if err != nil {
			return nil, fmt.Errorf("cancelDescendantClaims: LockForUpdate descendant %s: %w",
				d.ID, err)
		}
		if current == nil || current.State != spec.ClaimHandleStateActive {
			continue
		}
		producerName := ""
		if d.ProducerName != nil {
			producerName = *d.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
		if !ok {
			return nil, fmt.Errorf("cancelDescendantClaims: unknown producer %q for descendant %s",
				producerName, d.ID)
		}
		hint := ClaimLineageHint{
			ProducerName: producerName,
			VersionID:    d.VersionID,
			NodeID:       d.HolderNodeID,
		}
		if d.FrameID != nil {
			hint.FrameID = *d.FrameID
		}
		if d.NodeRunID != nil {
			hint.NodeRunID = *d.NodeRunID
		}
		if acquirer, aErr := args.Persist.Nodes().Get(ctx, d.HolderNodeID, tx); aErr == nil && acquirer != nil {
			hint.InstanceID = acquirer.InstanceID
		}
		pc, err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
			ClaimHandleID:       d.ID,
			SupervisorID:        args.SupervisorID,
			Source:              HeldTerminal,
			Outcome:             OutcomeAbandonDescendantCancel,
			Producer:            producer,
			Scope:               []byte(d.ClaimScopeData),
			Address:             []byte(d.Address),
			Lifetime:            d.Lifetime,
			CandidateHandle:     d.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: nil,
		})
		if err != nil {
			return nil, fmt.Errorf("cancelDescendantClaims: force-Abandon descendant %s: %w",
				d.ID, err)
		}
		post = chainPostCommit(post, pc)
	}
	return post, nil
}
