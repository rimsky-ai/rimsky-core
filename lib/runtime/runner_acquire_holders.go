// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// runner_acquire_holders.go — held-claim co-holdership inserts at
// acquire-time. Split out of `runner_acquire.go` per the 2026-05-17
// cold-read paydown (Item 4 / Tier 1).

package runtime

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// insertHeldClaimHoldersAtAcquire inserts the acquirer's own
// `rimsky_claim_holders` row when the alias is held. Post-stage-5 of
// the run-row lifecycle cutover, holder rows are keyed by
// `holder_run_id` (a `rimsky_node_runs.id`), so only the acquirer's
// own row — whose run id is known at acquire-time — is inserted here.
// Inheritor / co-holder rows are inserted at the inheritor's own
// dispatch time (see
// `runner_dispatch.go::insertCoHolderClaimHoldersAtDispatch`), where
// the inheritor's run id is the in-flight dispatch row.
//
// Inserting the acquirer's row at acquire prevents auto-terminal from
// firing prematurely before any inheritor / co-holder gets a chance to
// register: the row stays `active` until the acquirer's release path
// marks it (the `releaseClaim` held branch calls `markClaimHolderForRun`
// before `CheckAndFireResolution`).
//
// `@blessed-invariant 13`: held-claim resolution is auto-terminal,
// single, and aggregate-outcome-driven. The holders set this function
// seeds is the auto-terminal's input.
func insertHeldClaimHoldersAtAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	claimHandleID shared.UUID, cand persistence.Candidate, alias string,
	heldSubgraphs []node.HoldingSubgraph,
) error {
	subgraph, ok := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, alias)
	if !ok || !subgraph.IsHeld() {
		return nil
	}
	frameID := cand.FrameID
	if err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
		ID:            uuid.New(),
		ClaimHandleID: claimHandleID,
		HolderRunID:   cand.DispatchID,
		FrameID:       &frameID,
	}, tx); err != nil {
		return fmt.Errorf("insertHeldClaimHoldersAtAcquire: Insert: %w", err)
	}
	return nil
}

// insertCoHolderClaimHoldersAtAcquire inserts one `rimsky_claim_holders`
// row per co-holdership declared by this node. Two sources:
//
//  1. `holds:` — explicit co-holdership (spec §Claim co-holdership).
//     Each entry names an upstream node-alias whose claim is co-held.
//  2. `inherits:` — legacy pre-co-holdership inheritance. The acquirer
//     is resolved via the holding-subgraph computation.
//
// The row's `holder_run_id` is this run's id (`cand.DispatchID`);
// `state` is `'active'`. Idempotent — duplicate inserts in the same
// tx are blocked by the table's UNIQUE (claim_handle_id, holder_run_id).
//
// Runs inside the caller's tx (the acquisition tx). Per plan E4b step 2,
// the INSERTs commit atomically with this run's own claim acquisition.
//
// @blessed-invariant 13
// @concept: claim-co-holdership
func insertCoHolderClaimHoldersAtAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	cand persistence.Candidate, nodeDef *node.TemplateNodeDef, tmpl *node.TemplateSpec,
) error {
	if nodeDef == nil || tmpl == nil {
		return nil
	}
	if len(nodeDef.Holds) == 0 && len(nodeDef.Inherits) == 0 {
		return nil
	}
	nd, err := args.Persist.Nodes().Get(ctx, cand.NodeID, tx)
	if err != nil {
		return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: nodes.Get: %w", err)
	}
	if nd == nil {
		// Defense-in-depth: the candidate's node row must exist (the
		// scheduler tick that produced this candidate read it minutes
		// earlier). A missing node here would mean the row was deleted
		// between candidate selection and acquisition — a structural
		// invariant violation rather than a transient race. Fail loudly
		// so the acquisition tx rolls back instead of inserting an
		// orphan holder row.
		return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: node %s not found", cand.NodeID.String())
	}
	frameID := cand.FrameID
	// `holds:` entries (post-co-holdership wiring).
	for alias, binding := range nodeDef.Holds {
		upstreamType := binding.From
		if upstreamType == "" {
			continue
		}
		upstreamNode := findInstanceNodeByType(ctx, args, tx, nd.InstanceID, upstreamType)
		if upstreamNode == nil {
			args.Logger.Warn("insertCoHolderClaimHoldersAtAcquire: upstream node-type not found in instance",
				"node_id", cand.NodeID.String(),
				"alias", alias,
				"upstream_type", upstreamType)
			continue
		}
		lh := lookupClaimHandleForAlias(ctx, args, tx, upstreamNode.ID, tmpl, upstreamType, alias)
		if lh == nil {
			// Upstream's claim handle is missing — either the upstream
			// hasn't acquired yet (DAG violation: holds.from must be an
			// upstream dependency), or auto-terminal already fired
			// (committed-subgraph row swept, or abandoned). Skip silently;
			// CheckAndFireResolution is idempotent.
			continue
		}
		if err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            uuid.New(),
			ClaimHandleID: lh.ID,
			HolderRunID:   cand.DispatchID,
			FrameID:       &frameID,
		}, tx); err != nil {
			return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: holds: %w", err)
		}
	}
	// `inherits:` entries (legacy). Each names an alias; the acquirer is
	// the unique upstream member that acquires the alias per the
	// holding-subgraph computation.
	if len(nodeDef.Inherits) > 0 {
		subgraphs := node.HoldingSubgraphsForTemplate(tmpl)
		for _, ie := range nodeDef.Inherits {
			alias := ie.Claim
			if alias == "" {
				continue
			}
			var acquirerType string
			for _, sg := range subgraphs {
				if sg.Alias != alias {
					continue
				}
				if !memberOf(sg, nodeDef.Type) {
					continue
				}
				if sg.AcquirerType == nodeDef.Type {
					continue
				}
				acquirerType = sg.AcquirerType
				break
			}
			if acquirerType == "" {
				continue
			}
			upstreamNode := findInstanceNodeByType(ctx, args, tx, nd.InstanceID, acquirerType)
			if upstreamNode == nil {
				continue
			}
			lh := lookupClaimHandleForAlias(ctx, args, tx, upstreamNode.ID, tmpl, acquirerType, alias)
			if lh == nil {
				continue
			}
			if err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
				ID:            uuid.New(),
				ClaimHandleID: lh.ID,
				HolderRunID:   cand.DispatchID,
				FrameID:       &frameID,
			}, tx); err != nil {
				return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: inherits: %w", err)
			}
		}
	}
	return nil
}

// findHoldingSubgraphForAcquirer locates the (acquirerType, alias)
// subgraph in the precomputed list.
func findHoldingSubgraphForAcquirer(subgraphs []node.HoldingSubgraph, acquirerType, alias string) (node.HoldingSubgraph, bool) {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg, true
		}
	}
	return node.HoldingSubgraph{}, false
}
