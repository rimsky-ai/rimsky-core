// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: wait-set
// @concept: cascade
package runtime

import (
	"bytes"
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func candidateGatedByInFlightUpstream(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	nd *persistence.NodeRow, inst *persistence.InstanceRow,
	cand persistence.Candidate, runScopeID shared.UUID,
) (bool, error) {
	if inst == nil {
		return false, nil
	}
	if runScopeID == (shared.UUID{}) {
		return false, fmt.Errorf("candidateGatedByInFlightUpstream: run scope unresolved for run %s; failing closed", cand.DispatchID)
	}
	edges, err := subscriptionEdgesForTemplate(ctx, args, inst.TemplateHash, tx)
	if err != nil {
		return false, fmt.Errorf("candidateGatedByInFlightUpstream: edges: %w", err)
	}
	senderTypes := edges.SenderNodeTypesForReceiver(nd.NodeType)
	if len(senderTypes) == 0 {
		return false, nil
	}
	senderTypeSet := make(map[string]struct{}, len(senderTypes))
	for _, st := range senderTypes {
		senderTypeSet[st] = struct{}{}
	}
	instNodes, err := args.Persist.Nodes().ListByInstance(ctx, nd.InstanceID, tx)
	if err != nil {
		return false, fmt.Errorf("candidateGatedByInFlightUpstream: list instance nodes: %w", err)
	}
	senderIDs := make([]shared.UUID, 0, len(instNodes))
	allIDs := make([]shared.UUID, 0, len(instNodes))
	for _, n := range instNodes {
		allIDs = append(allIDs, n.ID)
		if n.ID == cand.NodeID {
			continue
		}
		if _, ok := senderTypeSet[n.NodeType]; ok {
			senderIDs = append(senderIDs, n.ID)
		}
	}
	if len(senderIDs) == 0 {
		return false, nil
	}
	phasesAll, err := args.Queue.ListInFlightRunPhases(ctx, tx, allIDs, cand.FrameID, runScopeID)
	if err != nil {
		return false, fmt.Errorf("candidateGatedByInFlightUpstream: %w", err)
	}
	gatingSenders := make([]shared.UUID, 0, len(senderIDs))
	for _, sid := range senderIDs {
		phases, inFlight := phasesAll[sid]
		if !inFlight {
			continue
		}
		if !phasesPendingOnly(phases) {
			return true, nil
		}
		gatingSenders = append(gatingSenders, sid)
	}
	if len(gatingSenders) == 0 {
		return false, nil
	}
	if !phasesPendingOnly(phasesAll[cand.NodeID]) {
		return true, nil
	}
	cycle := pendingGatingCycleMembers(edges, instNodes, cand, phasesAll)
	for _, senderID := range gatingSenders {
		if _, inCycle := cycle[senderID]; !inCycle {
			return true, nil
		}
	}
	for member := range cycle {
		if bytes.Compare(member[:], cand.NodeID[:]) < 0 {
			return true, nil
		}
	}
	return false, nil
}

func phasesPendingOnly(phases []string) bool {
	for _, ph := range phases {
		if ph != "pending" {
			return false
		}
	}
	return len(phases) > 0
}

func pendingGatingCycleMembers(
	edges *node.SubscriptionEdgeMap, instNodes []persistence.NodeRow,
	cand persistence.Candidate, phasesAll map[shared.UUID][]string,
) map[shared.UUID]struct{} {
	typeByID := make(map[shared.UUID]string, len(instNodes))
	idsByType := make(map[string][]shared.UUID, len(instNodes))
	for _, n := range instNodes {
		typeByID[n.ID] = n.NodeType
		idsByType[n.NodeType] = append(idsByType[n.NodeType], n.ID)
	}
	isMember := func(id shared.UUID) bool {
		return phasesPendingOnly(phasesAll[id])
	}
	senderMembersOf := func(id shared.UUID) []shared.UUID {
		var out []shared.UUID
		for _, st := range edges.SenderNodeTypesForReceiver(typeByID[id]) {
			for _, sid := range idsByType[st] {
				if sid == id {
					continue
				}
				if isMember(sid) {
					out = append(out, sid)
				}
			}
		}
		return out
	}
	fwd := reachableFrom(cand.NodeID, senderMembersOf)
	receiverMembersOf := func(id shared.UUID) []shared.UUID {
		var out []shared.UUID
		for _, r := range instNodes {
			if r.ID == id || !isMember(r.ID) {
				continue
			}
			for _, st := range edges.SenderNodeTypesForReceiver(r.NodeType) {
				if st == typeByID[id] {
					out = append(out, r.ID)
					break
				}
			}
		}
		return out
	}
	bwd := reachableFrom(cand.NodeID, receiverMembersOf)
	cycle := make(map[shared.UUID]struct{})
	for id := range fwd {
		if _, ok := bwd[id]; ok {
			cycle[id] = struct{}{}
		}
	}
	return cycle
}

func reachableFrom(start shared.UUID, next func(shared.UUID) []shared.UUID) map[shared.UUID]struct{} {
	seen := map[shared.UUID]struct{}{start: {}}
	queue := []shared.UUID{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range next(cur) {
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			queue = append(queue, n)
		}
	}
	return seen
}
