// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const claimQuerySup = "claim-query-supervisor"

func namedLockHandleInput(fix fixtureSet, lockName string) persistence.ClaimHandleInsertInput {
	name := lockName
	return persistence.ClaimHandleInsertInput{
		ID:                 uuid.New(),
		LockKind:           persistence.LockKindNamed,
		LockName:           &name,
		HolderSupervisorID: claimQuerySup,
		HolderNodeID:       fix.NodeID,
		ExpiresAt:          time.Now().Add(1 * time.Hour),
	}
}

func countNamedLock(ctx context.Context, t *testing.T, d persistence.Database, lockName string) int {
	t.Helper()
	var n int
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		var err error
		n, err = d.Tables().ClaimHandles().CountByNamedLock(ctx, lockName, tx)
		return err
	}); err != nil {
		t.Fatalf("CountByNamedLock(%s): %v", lockName, err)
	}
	return n
}

func testClaimHandleCountByNamedLock(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	capA := namedLockHandleInput(fix, "cap-lock")
	capB := namedLockHandleInput(fix, "cap-lock")
	other := namedLockHandleInput(fix, "other-lock")
	scope := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	for _, in := range []persistence.ClaimHandleInsertInput{capA, capB, other, scope} {
		seedGuardClaimHandle(ctx, t, d, in)
	}

	if got := countNamedLock(ctx, t, d, "cap-lock"); got != 2 {
		t.Fatalf("CountByNamedLock(cap-lock) = %d, want 2", got)
	}
	if got := countNamedLock(ctx, t, d, "other-lock"); got != 1 {
		t.Fatalf("CountByNamedLock(other-lock) = %d, want 1", got)
	}
	if got := countNamedLock(ctx, t, d, "absent-lock"); got != 0 {
		t.Fatalf("CountByNamedLock(absent-lock) = %d, want 0", got)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, capA.ID, claimQuerySup, spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if got := countNamedLock(ctx, t, d, "cap-lock"); got != 1 {
		t.Fatalf("CountByNamedLock(cap-lock) after commit = %d, want 1 (committed rows must not occupy capacity)", got)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHandles().Delete(ctx, capB.ID, claimQuerySup, tx)
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := countNamedLock(ctx, t, d, "cap-lock"); got != 0 {
		t.Fatalf("CountByNamedLock(cap-lock) after delete = %d, want 0", got)
	}
}

func claimHandleIDs(rows []persistence.ClaimHandleRow) []shared.UUID {
	out := make([]shared.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func testClaimHandleAnchorsAndRepoint(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	ch := store.ClaimHandles()

	runA := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	nodeB := seedExtraNode(ctx, t, d, fix, "anchor-node-b")
	runB := seedConformanceRunForNode(ctx, t, d, nodeB, fix.FrameID)

	claimedAtBase := time.Now().UTC()

	h1 := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	h1.NodeRunID = &runA
	h1.ClaimedAtOverride = claimedAtBase
	seedGuardClaimHandle(ctx, t, d, h1)
	h2 := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	h2.NodeRunID = &runA
	h2.ClaimedAtOverride = claimedAtBase.Add(1 * time.Second)
	seedGuardClaimHandle(ctx, t, d, h2)
	h3 := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	h3.HolderNodeID = nodeB
	h3.NodeRunID = &runB
	h3.ClaimedAtOverride = claimedAtBase.Add(2 * time.Second)
	seedGuardClaimHandle(ctx, t, d, h3)

	listByHolder := func(nodeID shared.UUID) []shared.UUID {
		t.Helper()
		var rows []persistence.ClaimHandleRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			rows, err = ch.ListByHolderNode(ctx, nodeID, tx)
			return err
		}); err != nil {
			t.Fatalf("ListByHolderNode: %v", err)
		}
		return claimHandleIDs(rows)
	}
	listByRun := func(runID shared.UUID) []shared.UUID {
		t.Helper()
		var rows []persistence.ClaimHandleRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			rows, err = ch.ListByNodeRun(ctx, runID, tx)
			return err
		}); err != nil {
			t.Fatalf("ListByNodeRun: %v", err)
		}
		return claimHandleIDs(rows)
	}

	if got := listByHolder(fix.NodeID); len(got) != 2 || got[0] != h1.ID || got[1] != h2.ID {
		t.Fatalf("ListByHolderNode(nodeA) = %v, want [%s %s] claimed_at-ascending", got, h1.ID, h2.ID)
	}
	if got := listByHolder(nodeB); len(got) != 1 || got[0] != h3.ID {
		t.Fatalf("ListByHolderNode(nodeB) = %v, want [%s]", got, h3.ID)
	}
	if got := listByRun(runA); len(got) != 2 || got[0] != h1.ID || got[1] != h2.ID {
		t.Fatalf("ListByNodeRun(runA) = %v, want [%s %s] claimed_at-ascending", got, h1.ID, h2.ID)
	}
	if got := listByRun(runB); len(got) != 1 || got[0] != h3.ID {
		t.Fatalf("ListByNodeRun(runB) = %v, want [%s]", got, h3.ID)
	}

	// @concept: claim-handle
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return ch.UpdateNodeRunID(ctx, h2.ID, runB, claimQuerySup, tx)
	}); err != nil {
		t.Fatalf("UpdateNodeRunID: %v", err)
	}
	if got := listByRun(runA); len(got) != 1 || got[0] != h1.ID {
		t.Fatalf("ListByNodeRun(runA) after repoint = %v, want [%s]", got, h1.ID)
	}
	got := listByRun(runB)
	if len(got) != 2 || got[0] != h2.ID || got[1] != h3.ID {
		t.Fatalf("ListByNodeRun(runB) after repoint = %v, want [%s %s] claimed_at-ascending", got, h2.ID, h3.ID)
	}
	row := getGuardClaimHandle(ctx, t, d, h2.ID)
	if row == nil || row.NodeRunID == nil || *row.NodeRunID != runB {
		t.Fatalf("repointed handle node_run_id = %v, want %s", row, runB)
	}
	if got := listByHolder(fix.NodeID); len(got) != 2 {
		t.Fatalf("UpdateNodeRunID mutated the holder-node anchor: %v", got)
	}
}

func testClaimHandleChildWalk(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	ch := store.ClaimHandles()

	parent := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, parent)
	child1 := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	child1.ParentClaimHandleID = &parent.ID
	seedGuardClaimHandle(ctx, t, d, child1)
	child2 := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	child2.ParentClaimHandleID = &parent.ID
	seedGuardClaimHandle(ctx, t, d, child2)
	unrelated := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, unrelated)

	listChildren := func(parentID shared.UUID) map[shared.UUID]bool {
		t.Helper()
		out := map[shared.UUID]bool{}
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			rows, err := ch.ListChildClaimHandles(ctx, parentID, tx)
			if err != nil {
				return err
			}
			for _, r := range rows {
				out[r.ID] = true
			}
			return nil
		}); err != nil {
			t.Fatalf("ListChildClaimHandles: %v", err)
		}
		return out
	}

	got := listChildren(parent.ID)
	if len(got) != 2 || !got[child1.ID] || !got[child2.ID] {
		t.Fatalf("children of parent = %v, want exactly {%s, %s}", got, child1.ID, child2.ID)
	}
	if got := listChildren(child1.ID); len(got) != 0 {
		t.Fatalf("leaf sub-claim reported children: %v", got)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return ch.Delete(ctx, parent.ID, claimQuerySup, tx)
	}); err != nil {
		t.Fatalf("Delete(parent): %v", err)
	}
	if got := listChildren(parent.ID); len(got) != 0 {
		t.Fatalf("children still anchored to the deleted parent: %v", got)
	}
	for _, id := range []shared.UUID{child1.ID, child2.ID} {
		row := getGuardClaimHandle(ctx, t, d, id)
		if row == nil {
			t.Fatalf("sub-claim %s cascaded away with its parent", id)
		}
		if row.ParentClaimHandleID != nil {
			t.Fatalf("sub-claim %s parent_claim_handle_id = %s, want NULL after parent delete", id, *row.ParentClaimHandleID)
		}
	}
}

func testClaimHandleListByInstanceAndState(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fixA := seedFixtureSet(ctx, t, d)
	fixB := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	ch := store.ClaimHandles()

	durableA := guardScopeHandleInput(fixA, claimQuerySup, time.Now().Add(1*time.Hour))
	durableA.Lifetime = spec.ClaimLifetimeDurable
	subgraphA := guardScopeHandleInput(fixA, claimQuerySup, time.Now().Add(1*time.Hour))
	activeDurableA := guardScopeHandleInput(fixA, claimQuerySup, time.Now().Add(1*time.Hour))
	activeDurableA.Lifetime = spec.ClaimLifetimeDurable
	durableB := guardScopeHandleInput(fixB, claimQuerySup, time.Now().Add(1*time.Hour))
	durableB.Lifetime = spec.ClaimLifetimeDurable
	for _, in := range []persistence.ClaimHandleInsertInput{durableA, subgraphA, activeDurableA, durableB} {
		seedGuardClaimHandle(ctx, t, d, in)
	}
	for _, id := range []shared.UUID{durableA.ID, subgraphA.ID, durableB.ID} {
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return ch.Promote(ctx, id, claimQuerySup, spec.ClaimHandleStateCommitted, tx)
		}); err != nil {
			t.Fatalf("Promote(%s): %v", id, err)
		}
	}

	list := func(instanceID shared.UUID, state spec.ClaimHandleState, lifetime spec.ClaimLifetime) []shared.UUID {
		t.Helper()
		var rows []persistence.ClaimHandleRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			rows, err = ch.ListByInstanceAndState(ctx, instanceID, state, lifetime, tx)
			return err
		}); err != nil {
			t.Fatalf("ListByInstanceAndState: %v", err)
		}
		return claimHandleIDs(rows)
	}

	if got := list(fixA.InstanceID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable); len(got) != 1 || got[0] != durableA.ID {
		t.Fatalf("assets(A) = %v, want [%s] only (subgraph/active/other-instance rows excluded)", got, durableA.ID)
	}
	if got := list(fixB.InstanceID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable); len(got) != 1 || got[0] != durableB.ID {
		t.Fatalf("assets(B) = %v, want [%s]", got, durableB.ID)
	}
	if got := list(fixA.InstanceID, spec.ClaimHandleStateActive, spec.ClaimLifetimeDurable); len(got) != 1 || got[0] != activeDurableA.ID {
		t.Fatalf("active-durable(A) = %v, want [%s]", got, activeDurableA.ID)
	}
	if got := list(fixA.InstanceID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeSubgraph); len(got) != 1 || got[0] != subgraphA.ID {
		t.Fatalf("committed-subgraph(A) = %v, want [%s]", got, subgraphA.ID)
	}
}

func testClaimHandleProducerLeaseTokenRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	in := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	in.ProducerLeaseToken = "lease-" + in.ID.String()
	seedGuardClaimHandle(ctx, t, d, in)
	row := getGuardClaimHandle(ctx, t, d, in.ID)
	if row == nil {
		t.Fatalf("claim handle %s not found after insert", in.ID)
	}
	if row.ProducerLeaseToken != in.ProducerLeaseToken {
		t.Fatalf("producer_lease_token round-trip: got %q, want %q", row.ProducerLeaseToken, in.ProducerLeaseToken)
	}
}
