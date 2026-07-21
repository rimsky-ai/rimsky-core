// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func findHoldingSubgraph(subgraphs []node.HoldingSubgraph, acquirerType, alias string) (node.HoldingSubgraph, bool) {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg, true
		}
	}
	return node.HoldingSubgraph{}, false
}

func isAliasHeld(subgraphs []node.HoldingSubgraph, acquirerType, alias string) bool {
	sg, ok := findHoldingSubgraph(subgraphs, acquirerType, alias)
	return ok && sg.IsHeld()
}

func markClaimHolderForRun(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	claimHandleID, runID shared.UUID, success bool,
) error {
	state := persistence.ClaimHolderStateCompleted
	if !success {
		state = persistence.ClaimHolderStateFailed
	}
	if err := args.Persist.ClaimHolders().CompleteByClaimHandleAndRun(
		ctx, claimHandleID, runID, state, tx,
	); err != nil {
		return fmt.Errorf("markClaimHolderForRun: CompleteByClaimHandleAndRun: %w", err)
	}
	return nil
}

func findInheritedAliasesForRun(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	subgraphs []node.HoldingSubgraph, nodeType string, runID shared.UUID,
) ([]inheritedAlias, error) {
	if len(subgraphs) == 0 {
		return nil, nil
	}
	rows, err := args.Persist.ClaimHolders().ListByHolderRun(ctx, runID, tx)
	if err != nil {
		return nil, fmt.Errorf("findInheritedAliasesForRun: ListByHolderRun: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	heldAcquirerTypes := map[string]struct{}{}
	for _, sg := range subgraphs {
		if sg.AcquirerType == nodeType {
			continue
		}
		if !sg.IsHeld() {
			continue
		}
		if !memberOf(sg, nodeType) {
			continue
		}
		heldAcquirerTypes[sg.AcquirerType] = struct{}{}
	}
	if len(heldAcquirerTypes) == 0 {
		return nil, nil
	}
	out := make([]inheritedAlias, 0, len(rows))
	for _, r := range rows {
		lh, err := args.ClaimHandles.Get(ctx, r.ClaimHandleID, tx)
		if err != nil {
			return nil, fmt.Errorf("findInheritedAliasesForRun: ClaimHandles.Get: %w", err)
		}
		if lh == nil {
			continue
		}
		acquirerNode, err := args.Persist.Nodes().Get(ctx, lh.HolderNodeID, tx)
		if err != nil {
			return nil, fmt.Errorf("findInheritedAliasesForRun: Nodes.Get acquirer: %w", err)
		}
		if acquirerNode == nil {
			continue
		}
		if _, ok := heldAcquirerTypes[acquirerNode.NodeType]; !ok {
			continue
		}
		out = append(out, inheritedAlias{ClaimHandleID: r.ClaimHandleID})
	}
	return out, nil
}

type inheritedAlias struct {
	ClaimHandleID shared.UUID
}

func memberOf(sg node.HoldingSubgraph, nodeType string) bool {
	for _, m := range sg.Members {
		if m == nodeType {
			return true
		}
	}
	return false
}
