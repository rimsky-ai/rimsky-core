// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func insertHeldClaimHoldersAtAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	claimHandleID shared.UUID, cand persistence.Candidate, alias string,
	heldSubgraphs []node.HoldingSubgraph,
) error {
	subgraph, ok := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, alias)
	if !ok || !subgraph.IsHeld() {
		return nil
	}
	if err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
		ID:              uuid.New(),
		ClaimHandleID:   claimHandleID,
		HolderNodeRunID: cand.NodeRunID,
	}, tx); err != nil {
		return fmt.Errorf("insertHeldClaimHoldersAtAcquire: Insert: %w", err)
	}
	return nil
}

// @concept: claim-co-holdership
func insertCoHolderClaimHoldersAtAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	cand persistence.Candidate, nodeDef *node.TemplateNodeDef, tmpl *node.TemplateSpec,
) error {
	if nodeDef == nil || tmpl == nil {
		return nil
	}
	if len(nodeDef.Holds) == 0 {
		return nil
	}
	nd, err := args.Persist.Nodes().Get(ctx, cand.NodeID, tx)
	if err != nil {
		return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: nodes.Get: %w", err)
	}
	if nd == nil {
		return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: node %s not found", cand.NodeID.String())
	}
	resolved, err := resolveHolds(ctx, args, tx, nd.InstanceID, tmpl, nodeDef, cand.FrameID, true)
	if err != nil {
		return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: %w", err)
	}
	for _, r := range resolved {
		if err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:              uuid.New(),
			ClaimHandleID:   r.claimHandle.ID,
			HolderNodeRunID: cand.NodeRunID,
		}, tx); err != nil {
			return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: holds: %w", err)
		}
	}
	return nil
}

func findHoldingSubgraphForAcquirer(subgraphs []node.HoldingSubgraph, acquirerType, alias string) (node.HoldingSubgraph, bool) {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg, true
		}
	}
	return node.HoldingSubgraph{}, false
}
