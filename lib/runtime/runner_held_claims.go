// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func isAliasHeld(subgraphs []node.HoldingSubgraph, acquirerType, alias string) bool {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg.IsHeld()
		}
	}
	return false
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
	subgraphs []node.HoldingSubgraph, nodeType string, runID, instanceID shared.UUID,
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
	candidatesByAcquirer := map[string][]aliasCandidate{}
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
		candidatesByAcquirer[sg.AcquirerType] = append(
			candidatesByAcquirer[sg.AcquirerType],
			aliasCandidate{acquirerType: sg.AcquirerType, alias: sg.Alias},
		)
	}
	if len(candidatesByAcquirer) == 0 {
		return nil, nil
	}
	out := make([]inheritedAlias, 0, len(rows))
	for _, r := range rows {
		lh, err := args.ClaimHandles.Get(ctx, r.ClaimHandleID, tx)
		if err != nil {
			return nil, fmt.Errorf("findInheritedAliasesForNode: ClaimHandles.Get: %w", err)
		}
		if lh == nil {
			continue
		}
		acquirerNode, err := args.Persist.Nodes().Get(ctx, lh.HolderNodeID, tx)
		if err != nil {
			return nil, fmt.Errorf("findInheritedAliasesForNode: Nodes.Get acquirer: %w", err)
		}
		if acquirerNode == nil {
			continue
		}
		picks, ok := candidatesByAcquirer[acquirerNode.NodeType]
		if !ok || len(picks) == 0 {
			continue
		}
		alias := pickAliasForClaimHandle(ctx, args, tx, instanceID, acquirerNode.NodeType, picks, lh)
		if alias == "" {
			continue
		}
		out = append(out, inheritedAlias{
			AcquirerType:  acquirerNode.NodeType,
			Alias:         alias,
			ClaimHandleID: r.ClaimHandleID,
		})
	}
	return out, nil
}

func pickAliasForClaimHandle(
	ctx context.Context, args RunArgs, tx persistence.Tx, instanceID shared.UUID,
	acquirerType string, picks []aliasCandidate, lh *persistence.ClaimHandleRow,
) string {
	if len(picks) == 1 {
		return picks[0].alias
	}
	inst, err := args.Persist.Instances().Get(ctx, instanceID, tx)
	if err != nil || inst == nil {
		return picks[0].alias
	}
	tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
	if err != nil || tmpl == nil {
		return picks[0].alias
	}
	def := lookupNodeDef(&tmpl.Spec, acquirerType)
	if def == nil {
		return picks[0].alias
	}
	for _, p := range picks {
		for _, sref := range def.ClaimProducers {
			if sref.AliasOf() != p.alias {
				continue
			}
			if matchesClaimScope(lh.ClaimScopeData, sref.Selector) {
				return p.alias
			}
		}
	}
	return picks[0].alias
}

type aliasCandidate struct {
	acquirerType string
	alias        string
}

func matchesClaimScope(claimScopeData []byte, selector string) bool {
	if len(claimScopeData) == 0 {
		return false
	}
	encoded, err := json.Marshal(selector)
	if err != nil {
		return false
	}
	return string(claimScopeData) == string(encoded)
}

type inheritedAlias struct {
	AcquirerType  string
	Alias         string
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
