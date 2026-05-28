// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Held-claim runtime helpers (release path §7.6 / auto-terminal
// `@blessed-invariant 13`).
//
// Post-stage-5 of the run-row lifecycle cutover, `rimsky_claim_holders`
// rows are keyed by `holder_run_id` (a `rimsky_node_runs.id`):
//
//   - The acquirer's own holder row is inserted at acquire time when
//     the claim is held (deploy-time computation via
//     `HoldingSubgraphsForTemplate`).
//   - Co-holders' rows are inserted at the co-holder's dispatch time
//     (per the `holds:` template directive, runtime entry point in
//     `runner_dispatch.go::insertCoHolderClaimHoldersAtDispatch`).
//   - Legacy `inherits:` rows are also inserted at the inheritor's
//     dispatch time (same path, derived from the holding subgraph).
//
// This file owns the per-acquired-claim release-path helpers used by
// `runner_terminal.go`'s release loop.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// isAliasHeld reports whether the alias acquired by acquirerType
// has IsHeld() == true (subgraph size > 1) in the precomputed
// holding-subgraph metadata.
func isAliasHeld(subgraphs []node.HoldingSubgraph, acquirerType, alias string) bool {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg.IsHeld()
		}
	}
	return false
}

// markClaimHolderForRun flips this run's rimsky_claim_holders row
// (for the given claim_handle_id) to 'completed' or 'failed' via a single
// targeted UPDATE keyed on the unique (claim_handle_id, holder_run_id)
// pair. Used by the terminal release path for both acquirer-of-held and
// inheritor-of-held branches.
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

// findInheritedAliasesForRun resolves one (acquirerType, alias,
// claimHandleID) entry per held subgraph this run is a non-acquirer
// member of. Used by the inheritor branch of the §7.6 release path.
//
// Per claim-holders row this run owns (keyed by holder_run_id post-
// stage-5), the function reads the parent lock-holder row to find the
// acquirer node, looks up the acquirer's NodeType, and selects the
// matching (acquirerType, alias) pair from the precomputed
// holding-subgraph metadata. The acquirer's lock-holder row carries
// `producer_name`; when an acquirer declares multiple aliases against
// the same producer_name, we further disambiguate by matching the
// lock-holder row's `claim_scope_data` against the alias's substituted
// selector — falling back to the first matching alias if the row has
// not yet had its store-chosen claim-scope written.
//
// This is deterministic on a per-row basis (no cartesian product) and
// agrees with the acquirer-side computation that drove the original
// `insertHeldClaimHoldersAtAcquire`.
//
// All persistence reads share the caller's tx — option C / no-nil-tx
// (the release path is inside an open Persist.Transaction; passing
// nil here would self-deadlock under the SQLite single-conn pool).
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
	// Pre-index held subgraphs this node could inherit from (acquirer
	// is somebody else; this node is a member; size > 1).
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
			continue // already auto-terminated by a sibling
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

// pickAliasForClaimHandle picks the alias for an inherited lock-holder
// row when the acquirer declares one or more aliases that name this
// locks. Single-candidate case: return that alias. Multi-candidate
// case: walk the acquirer's NodeDef and match each alias's substituted
// selector to the row's `claim_scope_data`; return the first match. Falls
// back to the first candidate when no selector matches (the row may
// have been inserted before the store-chosen claim-scope was written).
//
// Reuses the caller's tx (option C / no-nil-tx). See findInheritedAliasesForNode.
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
		for _, sref := range def.Stores {
			if sref.AliasOf() != p.alias {
				continue
			}
			// Best-effort selector match — we don't re-substitute
			// params/deps here; the acquirer already wrote the
			// substituted selector into claim_scope_data at acquire time.
			if matchesClaimScope(lh.ClaimScopeData, sref.Selector) {
				return p.alias
			}
		}
	}
	return picks[0].alias
}

// aliasCandidate is the per-acquirer alias-search element used by
// pickAliasForClaimHandle.
type aliasCandidate struct {
	acquirerType string
	alias        string
}

// matchesClaimScope reports whether the lock-holder row's claim_scope_data
// equals the JSON-encoded selector. Conservative: empty claim_scope_data is
// non-matching; malformed bytes are non-matching.
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

// inheritedAlias bundles the per-aliased-claim metadata an
// inheritor terminal needs.
type inheritedAlias struct {
	AcquirerType  string
	Alias         string
	ClaimHandleID shared.UUID
}

// memberOf reports whether nodeType is in subgraph.Members.
func memberOf(sg node.HoldingSubgraph, nodeType string) bool {
	for _, m := range sg.Members {
		if m == nodeType {
			return true
		}
	}
	return false
}
