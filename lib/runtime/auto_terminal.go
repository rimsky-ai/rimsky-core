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
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// @concept: auto-terminal
func CheckAndFireResolution(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	claimHandleID shared.UUID,
) (postCommitFn, error) {
	row, err := args.ClaimHandles.LockForUpdate(ctx, claimHandleID, tx)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	if row.HolderSupervisorID == nil || *row.HolderSupervisorID != args.SupervisorID {
		return nil, nil
	}
	// @concept: claim-handle
	if row.State != spec.ClaimHandleStateActive {
		return nil, nil
	}

	holders, err := args.Persist.ClaimHolders().ListByClaimHandleID(ctx, claimHandleID, tx)
	if err != nil {
		return nil, fmt.Errorf("CheckAndFireResolution: ListByClaimHandleID: %w", err)
	}
	if len(holders) == 0 {
		return nil, nil
	}
	anyActive, anyFailed := false, false
	for _, h := range holders {
		switch h.State {
		case persistence.ClaimHolderStateActive:
			anyActive = true
		case persistence.ClaimHolderStateFailed:
			anyFailed = true
		}
	}
	if anyActive {
		return nil, nil
	}
	if !anyFailed {
		expectedMissing, err := expectedInheritorsMissing(ctx, args, tx, row, holders)
		if err != nil {
			return nil, fmt.Errorf("CheckAndFireResolution: expected-inheritor check: %w", err)
		}
		if expectedMissing {
			return nil, nil
		}
	}

	producerName := ""
	if row.ProducerName != nil {
		producerName = *row.ProducerName
	}
	producer, ok := args.StoreRegistry.Get(producerName)
	if !ok {
		return nil, fmt.Errorf("CheckAndFireResolution: unknown producer %q", producerName)
	}
	outcome := OutcomeCommit
	if anyFailed {
		outcome = OutcomeAbandon
	}
	if row.ExpectedChildrenCount > 0 && !anyFailed {
		resolved := row.CommittedChildrenCount + row.AbandonedChildrenCount
		if resolved < row.ExpectedChildrenCount {
			return nil, nil
		}
		outcome = aggregateParentOutcome(row, outcome)
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
	instID, err := acquirerInstanceID(ctx, args, tx, row.HolderNodeID)
	if err != nil {
		return nil, fmt.Errorf("CheckAndFireResolution: %w", err)
	}
	hint.InstanceID = instID
	if args.CheckAndFireHook != nil {
		args.CheckAndFireHook(ctx)
	}
	td := TerminalDecision{
		ClaimHandleID:       claimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              HeldTerminal,
		Outcome:             outcome,
		Producer:            producer,
		Scope:               []byte(row.ClaimScopeData),
		Address:             []byte(row.Address),
		LeaseToken:          row.ProducerLeaseToken,
		Lifetime:            row.Lifetime,
		CandidateHandle:     row.ProducerCandidateHandle,
		ProducerName:        producerName,
		LineageHint:         hint,
		ParentClaimHandleID: row.ParentClaimHandleID,
	}
	pc, err := ResolveClaimHandleTerminal(ctx, args, tx, td)
	if err != nil {
		return nil, fmt.Errorf("CheckAndFireResolution: %w", err)
	}
	return pc, nil
}

func expectedInheritorsMissing(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	row *persistence.ClaimHandleRow, holders []persistence.ClaimHolderRow,
) (bool, error) {
	if row == nil {
		return false, nil
	}
	acquirer, err := args.Persist.Nodes().Get(ctx, row.HolderNodeID, tx)
	if err != nil {
		return false, fmt.Errorf("nodes.Get acquirer: %w", err)
	}
	if acquirer == nil {
		return false, nil
	}
	inst, err := args.Persist.Instances().Get(ctx, acquirer.InstanceID, tx)
	if err != nil {
		return false, fmt.Errorf("instances.Get acquirer instance: %w", err)
	}
	if inst == nil {
		return false, nil
	}
	tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
	if err != nil {
		return false, fmt.Errorf("templates.GetByHash: %w", err)
	}
	if tmpl == nil {
		return false, nil
	}
	acqDef := lookupNodeDef(&tmpl.Spec, acquirer.NodeType)
	if acqDef == nil {
		return false, nil
	}
	producerName := ""
	if row.ProducerName != nil {
		producerName = *row.ProducerName
	}
	var alias string
	for _, sref := range acqDef.ClaimProducers {
		if sref.Name == producerName {
			alias = sref.AliasOf()
			break
		}
	}
	if alias == "" {
		return false, nil
	}
	subgraphs := node.HoldingSubgraphsForTemplate(&tmpl.Spec)
	var members []string
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirer.NodeType && sg.Alias == alias {
			members = sg.Members
			break
		}
	}
	if len(members) <= 1 {
		return false, nil
	}
	holderTypes := make(map[string]struct{}, len(holders))
	for _, h := range holders {
		nodeID, _, err := args.Queue.GetDispatchNode(ctx, h.HolderNodeRunID)
		if err != nil || nodeID == (shared.UUID{}) {
			continue
		}
		nodeRow, err := args.Persist.Nodes().Get(ctx, nodeID, tx)
		if err != nil || nodeRow == nil {
			continue
		}
		holderTypes[nodeRow.NodeType] = struct{}{}
	}
	for _, m := range members {
		if _, present := holderTypes[m]; !present {
			return true, nil
		}
	}
	return false, nil
}
