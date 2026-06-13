// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claim_handle_queries.go — ClaimHandleQueries conformance area.
//
// Pins the runtime-consumed rimsky_claim_handles read/repoint surface
// that the claimant-guard area (claimant_guard.go) does not touch:
//
//   - CountByNamedLock: the named-lock counting-mode capacity gate
//     (the supervisor's acquisition tx) — counts state='active' rows
//     of lock_kind='named' for the name ONLY; committed rows released
//     at terminal must not occupy capacity.
//   - ListByHolderNode / ListByNodeRun: the anchor walks the orphan
//     reaper, lock-release path, and fan-out leaf dispatch resolve
//     claims by, ordered claimed_at ASC.
//   - UpdateNodeRunID: the fan-out dispatch repoint — a sub-claim
//     moves from the parent run's anchor to its own child leaf run so
//     the leaf resolves its candidate handle by node_run_id.
//   - ListChildClaimHandles: the recursive claim-tree walk
//     (auto-terminal aggregation + cancel cascade), including the
//     ON DELETE SET NULL detach that lets sub-claim rows outlive a
//     deleted parent during auto-terminal staging.
//   - ListByInstanceAndState: the asset query's
//     holder_node_id → rimsky_nodes JOIN filtered by
//     instance + state + lifetime.
//
// Each query is hand-mirrored in both drivers (postgres JSONB/UUID
// columns vs sqlite TEXT, qualified-column JOINs); identical observable
// assertions on both drivers pin against drift.
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

// namedLockHandleInput builds a named-lock insert input owned by
// claimQuerySup.
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

// countNamedLock wraps CountByNamedLock in its own tx.
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

// testClaimHandleCountByNamedLock covers the named-lock capacity gate.
func testClaimHandleCountByNamedLock(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// Two active holders on "cap-lock", one on "other-lock", plus a
	// claim-scope-kind row (never counted regardless of name).
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

	// A committed row released the lock at terminal — it must NOT count
	// against the capacity limit.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, capA.ID, claimQuerySup, spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if got := countNamedLock(ctx, t, d, "cap-lock"); got != 1 {
		t.Fatalf("CountByNamedLock(cap-lock) after commit = %d, want 1 (committed rows must not occupy capacity)", got)
	}

	// Deleting the remaining holder frees the name entirely.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHandles().Delete(ctx, capB.ID, claimQuerySup, tx)
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := countNamedLock(ctx, t, d, "cap-lock"); got != 0 {
		t.Fatalf("CountByNamedLock(cap-lock) after delete = %d, want 0", got)
	}
}

// claimHandleIDs projects a row slice to its ids for exact-set asserts.
func claimHandleIDs(rows []persistence.ClaimHandleRow) []shared.UUID {
	out := make([]shared.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// testClaimHandleAnchorsAndRepoint covers ListByHolderNode, ListByNodeRun
// (both claimed_at-ascending), and the UpdateNodeRunID fan-out repoint.
func testClaimHandleAnchorsAndRepoint(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	ch := store.ClaimHandles()

	runA := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	nodeB := seedExtraNode(ctx, t, d, fix, "anchor-node-b")
	runB := seedConformanceRunForNode(ctx, t, d, nodeB, fix.FrameID)

	// h1, h2 anchored to (fix.NodeID, runA) — the sleep guarantees a
	// strictly-older claimed_at for h1 at both drivers' stored
	// precision. h3 anchored to (nodeB, runB).
	h1 := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	h1.NodeRunID = &runA
	seedGuardClaimHandle(ctx, t, d, h1)
	time.Sleep(20 * time.Millisecond)
	h2 := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	h2.NodeRunID = &runA
	seedGuardClaimHandle(ctx, t, d, h2)
	time.Sleep(20 * time.Millisecond)
	h3 := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	h3.HolderNodeID = nodeB
	h3.NodeRunID = &runB
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

	// Fan-out repoint: h2 moves from the parent run (runA) to the child
	// leaf run (runB). The leaf then resolves it by its OWN dispatch id;
	// the parent's anchor no longer carries it.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return ch.UpdateNodeRunID(ctx, h2.ID, runB, tx)
	}); err != nil {
		t.Fatalf("UpdateNodeRunID: %v", err)
	}
	if got := listByRun(runA); len(got) != 1 || got[0] != h1.ID {
		t.Fatalf("ListByNodeRun(runA) after repoint = %v, want [%s]", got, h1.ID)
	}
	// claimed_at ordering follows insert time (h2 before h3), not the
	// repoint time — UpdateNodeRunID moves the anchor only.
	got := listByRun(runB)
	if len(got) != 2 || got[0] != h2.ID || got[1] != h3.ID {
		t.Fatalf("ListByNodeRun(runB) after repoint = %v, want [%s %s] claimed_at-ascending", got, h2.ID, h3.ID)
	}
	row := getGuardClaimHandle(ctx, t, d, h2.ID)
	if row == nil || row.NodeRunID == nil || *row.NodeRunID != runB {
		t.Fatalf("repointed handle node_run_id = %v, want %s", row, runB)
	}
	// The repoint moves the run anchor ONLY; the holder-node anchor is
	// untouched.
	if got := listByHolder(fix.NodeID); len(got) != 2 {
		t.Fatalf("UpdateNodeRunID mutated the holder-node anchor: %v", got)
	}
}

// testClaimHandleChildWalk covers ListChildClaimHandles plus the
// parent-delete SET NULL detach the auto-terminal staging relies on.
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

	// Parent delete detaches (ON DELETE SET NULL): the sub-claim rows
	// outlive the parent during auto-terminal staging rather than
	// cascading away with it.
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

// testClaimHandleListByInstanceAndState covers the asset query's
// instance + state + lifetime JOIN filter.
func testClaimHandleListByInstanceAndState(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fixA := seedFixtureSet(ctx, t, d)
	fixB := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	ch := store.ClaimHandles()

	// Instance A: a committed-durable asset row, a committed-subgraph
	// row (lifetime filter), and a still-active durable row (state
	// filter). Instance B: a committed-durable row (instance filter).
	durableA := guardScopeHandleInput(fixA, claimQuerySup, time.Now().Add(1*time.Hour))
	durableA.Lifetime = spec.ClaimLifetimeDurable
	subgraphA := guardScopeHandleInput(fixA, claimQuerySup, time.Now().Add(1*time.Hour))
	activeDurableA := guardScopeHandleInput(fixA, claimQuerySup, time.Now().Add(1*time.Hour))
	activeDurableA.Lifetime = spec.ClaimLifetimeDurable
	durableB := guardScopeHandleInput(fixB, claimQuerySup, time.Now().Add(1*time.Hour))
	durableB.HolderNodeID = fixB.NodeID
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

	// The asset query shape: exactly the committed-durable row of THIS
	// instance.
	if got := list(fixA.InstanceID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable); len(got) != 1 || got[0] != durableA.ID {
		t.Fatalf("assets(A) = %v, want [%s] only (subgraph/active/other-instance rows excluded)", got, durableA.ID)
	}
	if got := list(fixB.InstanceID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable); len(got) != 1 || got[0] != durableB.ID {
		t.Fatalf("assets(B) = %v, want [%s]", got, durableB.ID)
	}
	// State arm: the active-durable row surfaces under state=active only.
	if got := list(fixA.InstanceID, spec.ClaimHandleStateActive, spec.ClaimLifetimeDurable); len(got) != 1 || got[0] != activeDurableA.ID {
		t.Fatalf("active-durable(A) = %v, want [%s]", got, activeDurableA.ID)
	}
	// Lifetime arm: the committed-subgraph row surfaces under
	// lifetime=subgraph only.
	if got := list(fixA.InstanceID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeSubgraph); len(got) != 1 || got[0] != subgraphA.ID {
		t.Fatalf("committed-subgraph(A) = %v, want [%s]", got, subgraphA.ID)
	}
}
