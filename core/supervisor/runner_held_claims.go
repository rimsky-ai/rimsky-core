// Held-claim runtime helpers (release path §7.6 / auto-terminal
// §4.10 invariant 13).
//
// Held-claim rimsky_claim_holders rows are inserted at acquisition
// (in runner_acquire.go::insertHeldClaimHoldersAtAcquire), not at
// terminal. This file owns the per-acquired-claim release-path
// helpers used by runner_terminal.go's release loop.

package supervisor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
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

// markClaimHolderForNode flips this node's rimsky_claim_holders row
// (for the given lock_holder_id) to 'completed' or 'failed' via a single
// targeted UPDATE keyed on the unique (lock_holder_id, holder_node_id)
// pair. Used by the terminal release path for both acquirer-of-held and
// inheritor-of-held branches.
func markClaimHolderForNode(
	ctx context.Context, args RunArgs, tx pgx.Tx,
	lockHolderID, nodeID shared.UUID, success bool,
) error {
	stx := pgstorage.WrapPgxTx(tx)
	state := storage.ClaimHolderStateCompleted
	if !success {
		state = storage.ClaimHolderStateFailed
	}
	if err := args.Storage.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, nodeID, state, stx,
	); err != nil {
		return fmt.Errorf("markClaimHolderForNode: CompleteByLockHolderAndNode: %w", err)
	}
	return nil
}

// findInheritedAliasesForNode resolves one (acquirerType, alias,
// lockHolderID) entry per held subgraph this node is a non-acquirer
// member of. Used by the inheritor branch of the §7.6 release path.
//
// Per claim-holders row this node owns, the function reads the parent
// lock-holder row to find the acquirer node, looks up the acquirer's
// NodeType, and selects the matching (acquirerType, alias) pair from
// the precomputed holding-subgraph metadata. The acquirer's lock-holder
// row carries `store_name`; when an acquirer declares multiple
// aliases against the same store_name, we further disambiguate by
// matching the lock-holder row's `region_data` against the alias's
// substituted selector — falling back to the first matching alias if
// the row has not yet had its substrate-chosen region written.
//
// This is deterministic on a per-row basis (no cartesian product) and
// agrees with the acquirer-side computation that drove the original
// `insertHeldClaimHoldersAtAcquire`.
func findInheritedAliasesForNode(
	ctx context.Context, args RunArgs,
	subgraphs []node.HoldingSubgraph, nodeType string, nodeID, instanceID shared.UUID,
) ([]inheritedAlias, error) {
	if len(subgraphs) == 0 {
		return nil, nil
	}
	rows, err := args.Storage.ClaimHolders().ListByHolderNode(ctx, nodeID, nil)
	if err != nil {
		return nil, fmt.Errorf("findInheritedAliasesForNode: ListByHolderNode: %w", err)
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
		lh, err := args.Storage.LockHolders().Get(ctx, r.LockHolderID, nil)
		if err != nil {
			return nil, fmt.Errorf("findInheritedAliasesForNode: LockHolders.Get: %w", err)
		}
		if lh == nil {
			continue // already auto-terminated by a sibling
		}
		acquirerNode, err := args.Storage.Nodes().Get(ctx, lh.HolderNodeID, nil)
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
		alias := pickAliasForLockHolder(ctx, args, instanceID, acquirerNode.NodeType, picks, lh)
		if alias == "" {
			continue
		}
		out = append(out, inheritedAlias{
			AcquirerType: acquirerNode.NodeType,
			Alias:        alias,
			LockHolderID: r.LockHolderID,
		})
	}
	return out, nil
}

// pickAliasForLockHolder picks the alias for an inherited lock-holder
// row when the acquirer declares one or more aliases that name this
// store. Single-candidate case: return that alias. Multi-candidate
// case: walk the acquirer's NodeDef and match each alias's substituted
// selector to the row's `region_data`; return the first match. Falls
// back to the first candidate when no selector matches (the row may
// have been inserted before the substrate-chosen region was written).
func pickAliasForLockHolder(
	ctx context.Context, args RunArgs, instanceID shared.UUID,
	acquirerType string, picks []aliasCandidate, lh *storage.LockHolderRow,
) string {
	if len(picks) == 1 {
		return picks[0].alias
	}
	inst, err := args.Storage.Instances().Get(ctx, instanceID, nil)
	if err != nil || inst == nil {
		return picks[0].alias
	}
	tmpl, err := args.Storage.Templates().Get(ctx, inst.TemplateID, nil)
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
			// substituted selector into region_data at acquire time.
			if matchesRegion(lh.RegionData, sref.Selector) {
				return p.alias
			}
		}
	}
	return picks[0].alias
}

// aliasCandidate is the per-acquirer alias-search element used by
// pickAliasForLockHolder.
type aliasCandidate struct {
	acquirerType string
	alias        string
}

// matchesRegion reports whether the lock-holder row's region_data
// equals the JSON-encoded selector. Conservative: empty region_data is
// non-matching; malformed bytes are non-matching.
func matchesRegion(regionData []byte, selector string) bool {
	if len(regionData) == 0 {
		return false
	}
	encoded, err := json.Marshal(selector)
	if err != nil {
		return false
	}
	return string(regionData) == string(encoded)
}

// inheritedAlias bundles the per-aliased-claim metadata an
// inheritor terminal needs.
type inheritedAlias struct {
	AcquirerType string
	Alias        string
	LockHolderID shared.UUID
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
