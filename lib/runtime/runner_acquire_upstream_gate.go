// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Upstream-gating eligibility condition.
//
// A stale receiver is not dispatch-eligible while any subscribed
// upstream has an in-flight run in the same (frame, run scope) —
// regardless of which propagation path made the receiver stale. The
// invalidation walk seeds pessimistic wait-set rows, but the
// settlement walk marks direct subscribers stale without seeding
// next-tier gates, so a multi-parent receiver whose upstreams went
// stale via settlement could otherwise dispatch after the FIRST
// parent settles, racing the still-in-flight rest. Enforcing the
// condition here — at the dispatch-eligibility chokepoint, from the
// template's subscription edges — means no current or future
// stale-transition site has to remember per-path wait-set seeding.
//
// This is an eligibility condition, NOT a wait-set write: it seeds no
// rows, and the wait-set's drained-rows substitution role is
// untouched. A gated candidate is simply skipped this cycle (its row
// stays pending); the in-flight sender's own settlement re-triggers
// selection.
//
// @blessed-invariant: stale-run-not-dispatch-eligible — a stale run is not dispatch-eligible while any
// subscribed upstream has an in-flight run in the same frame,
// propagation-path-independent. Enforced by candidateGatedByInFlightUpstream
// at the tryAcquire pre-claim site; the persistence half is
// Queue.ListInFlightRunPhases (both drivers; driver-parity suite
// area ListInFlightRunPhases).
//
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

// candidateGatedByInFlightUpstream reports whether the candidate must
// be skipped this cycle because a subscribed upstream node has an
// in-flight run in the candidate's (frame, run scope).
//
// Sender resolution comes from the template's subscription-edge map
// (the same derivation the cascade walker uses), restricted to named
// senders — cross-cutting (`instance: true`) edges name no upstream
// node-type and are excluded (see SenderNodeTypesForReceiver). The
// candidate's own node id is excluded from the sender set so the
// self-edge "drain my own queue" idiom keeps working: a node's own
// pending row must not gate itself.
//
// Pending-cycle tie-breaker (documented decision): subscription cycles
// are legal at registration, and `pending` counts as in-flight — so
// the nodes of a subscription cycle all holding pending rows in one
// (frame, scope) would gate each other forever (an N-cycle
// generalization of the self-edge case; the 2-cycle is the mutual-
// subscription shape, and longer cycles — A→B→C→A — have NO mutual
// pair, so a pairwise tie-break alone deadlocks them). The tie-breaker:
// when EVERY gating sender is MERELY-PENDING (its only in-flight rows
// are pending — it is itself gated, not progressing), compute the
// pending-only gating cycle containing the candidate (the set of
// pending nodes mutually reachable with the candidate through
// pending-gating subscription edges). If every gating sender belongs to
// that cycle and the candidate's node id is byte-wise lowest in it, the
// candidate dispatches; every other cycle member remains gated until
// the winner resolves, preserving deterministic serialization instead
// of a silent deadlock. The rule is computed locally per candidate from
// the same persisted state, so every contender reaches the same
// verdict. A sender with any progressing row (active / held / parked),
// or a merely-pending sender OUTSIDE the candidate's pending cycle,
// always gates, exactly like any other upstream. The tie-breaker
// applies only when the CANDIDATE's own in-flight rows are also
// merely-pending — a candidate with any progressing row (e.g. a parked
// run alongside the pending one) stays gated, so every contender
// evaluates cycle membership with the same uniform predicate. Liveness
// consequence: if the elected winner's dispatch is itself blocked
// post-gate (lock unavailable, producer fault, etc.), the whole cycle
// stalls behind it — the tie-break is deterministic serialization with
// no rotation, so a stuck winner holds the cycle until it resolves
// rather than ceding to the next-lowest member.
//
// Errors surface to the caller (the candidate is not claimed and its
// row stays pending) rather than degrading to a fail-open skip —
// protects the all-upstreams-resolve-first guarantee over dispatch
// throughput on a faulting database.
func candidateGatedByInFlightUpstream(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	nd *persistence.NodeRow, inst *persistence.InstanceRow,
	cand persistence.Candidate, runScopeID shared.UUID,
) (bool, error) {
	if inst == nil {
		// No instance row → no template to derive edges from; the
		// caller already treats this as a degraded candidate.
		return false, nil
	}
	if runScopeID == (shared.UUID{}) {
		// The run-scope key is load-bearing for the gate: a zero scope
		// would match nothing and silently dispatch ungated. Fail
		// closed — consistent with the edges/nodes lookups below and
		// the doc above (the guarantee wins over throughput on a
		// faulting database). The caller's run-tree lookup surfaces its
		// own error before this is ever reached; this guards the
		// remaining no-row / partial-wiring shapes.
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
			// Self-edge: a node subscribed to itself drains its own
			// queue; its own in-flight row must not gate it.
			continue
		}
		if _, ok := senderTypeSet[n.NodeType]; ok {
			senderIDs = append(senderIDs, n.ID)
		}
	}
	if len(senderIDs) == 0 {
		return false, nil
	}
	// One phase query for the whole instance node set: the gate's own
	// sender check reads the sender subset, and the pending-cycle
	// tie-breaker (when it applies) reuses the same map instead of
	// re-querying a superset in the same tx.
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
			// A progressing upstream (active / held / parked) always
			// gates, cycle or not.
			return true, nil
		}
		gatingSenders = append(gatingSenders, sid)
	}
	if len(gatingSenders) == 0 {
		return false, nil
	}
	// Every gating sender is merely-pending — the pending-cycle
	// tie-breaker MAY apply (see the package doc). It applies only when
	// the candidate's OWN in-flight rows are also merely-pending: a
	// candidate with a progressing row (e.g. a parked run alongside the
	// pending one) stays gated, so every contender computes cycle
	// membership over the same uniform predicate and the same persisted
	// state — no contender-dependent SCC, no dual-pass.
	if !phasesPendingOnly(phasesAll[cand.NodeID]) {
		return true, nil
	}
	// Compute the pending-only gating cycle containing the candidate;
	// the candidate passes only when every gating sender belongs to that
	// cycle AND the candidate's node id is byte-wise lowest in it.
	cycle := pendingGatingCycleMembers(edges, instNodes, cand, phasesAll)
	for _, senderID := range gatingSenders {
		if _, inCycle := cycle[senderID]; !inCycle {
			// A merely-pending sender outside the candidate's pending
			// cycle gates like any other upstream: its own gates resolve
			// independently, and its settlement re-triggers selection.
			return true, nil
		}
	}
	for member := range cycle {
		if bytes.Compare(member[:], cand.NodeID[:]) < 0 {
			// A lower-id cycle member wins the tie-break: it dispatches
			// first, we stay gated.
			return true, nil
		}
	}
	return false, nil
}

// phasesPendingOnly reports whether an in-flight phase list contains
// only `pending` rows (the merely-pending shape the tie-breaker keys
// on). An empty list is NOT pending-only — it means no in-flight rows
// at all.
func phasesPendingOnly(phases []string) bool {
	for _, ph := range phases {
		if ph != "pending" {
			return false
		}
	}
	return len(phases) > 0
}

// pendingGatingCycleMembers computes the pending-only gating cycle
// containing the candidate: the set of instance nodes that are mutually
// reachable with the candidate through pending-gating subscription
// edges in the candidate's (frame, run scope).
//
// Graph shape: vertices are the candidate plus every instance node
// whose in-flight rows in (frame, scope) are merely-pending; a directed
// edge receiver→sender exists when the template's subscription-edge map
// names sender's node-type in the receiver's sender set (self-edges
// excluded, consistent with the gate's self-edge rule). The returned
// set is fwd-reachable ∩ bwd-reachable from the candidate — the
// strongly-connected component containing it. When the candidate sits
// on no pending cycle, the set is just {candidate}.
//
// The computation is deterministic over persisted state only (node ids,
// template edges, in-flight phases), so every contender evaluating its
// own candidacy in the same database state derives the same cycle and
// the same byte-wise-lowest winner. phasesAll is the caller's
// whole-instance phase map (queried once in the gate, same tx).
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
	// Vertex predicate — UNIFORM across every node, the candidate
	// included: a node is a member iff its in-flight rows in
	// (frame, scope) are merely-pending. The caller has already
	// verified the candidate's own rows are pending-only before the
	// tie-breaker runs (a candidate with a progressing row is gated
	// outright), and reachableFrom seeds the candidate unconditionally,
	// so no candidate-by-definition special case is needed — two
	// contenders evaluating the same state always derive the same SCC.
	isMember := func(id shared.UUID) bool {
		return phasesPendingOnly(phasesAll[id])
	}
	// senderMembersOf resolves a member's gating-edge successors: the
	// member nodes whose node-type appears in the receiver's sender set
	// (self-edges excluded).
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
	// Reverse edges: receiver r points at sender s, so s's predecessors
	// are every member r whose sender set contains s's type.
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

// reachableFrom returns the set of vertices reachable from start
// (inclusive) following next-edge expansion. Plain BFS; the graph is
// bounded by the instance's node count.
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
