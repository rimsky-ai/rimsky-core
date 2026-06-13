// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claimant_guard.go — ClaimantGuard conformance area.
//
// Inv 4: claimant-guarded release. Every ownership mutation against
// rimsky_claim_handles / rimsky_claim_holders (holder_supervisor_id
// guard) and rimsky_node_runs (claimed_by guard) must be a provable
// no-op for the wrong supervisor on BOTH drivers. Each test seeds a row
// owned by supervisor A, performs the mutation as supervisor B
// (asserting the row is byte-for-byte intact: state, holder, heartbeat,
// payload columns), then performs it as A and asserts it succeeded.
//
// Operation families covered (one test func per family):
//   - claim-handle guarded column UPDATEs (UpdateAddress, UpdatePayload,
//     UpdateRealizedWriteSemantics, UpdateClaimScope, SetVersionID,
//     SetAggregationPolicy)
//   - claim-handle counter bumps (BumpExpectedChildrenCount,
//     BumpChildOutcomeCount)
//   - claim-handle Promote
//   - claim-handle ReassignHolderSupervisor (the cross-supervisor
//     handoff CAS: wrong-from rejection, empty-to rejection, the
//     state='active' gate)
//   - claim-handle Delete (+ DeleteResolved's absence-guard rejecting
//     an active owned row)
//   - claim-handle DeleteIfExpired (claimant guard AND expiry gate)
//   - claim-handle ExtendHeartbeat
//   - claim-holder release (FailAllActiveByClaimHandle's EXISTS guard)
//   - node-run claim steal (ClaimDispatchRow's claimed_by IS NULL gate)
//   - node-run ReleaseClaim
//   - node-run Complete
//   - node-run RemoveForNode / RemoveForNodeInTx
//   - node-run ParkActiveInTx
//   - node-run RefreshHeartbeat
//   - node-run Nodes().UpdateHeartbeat (claimed_by stamp + refresh)
//
// Named carve-out (pinned, not exempted silently): Queue.Complete /
// ReleaseClaim / RemoveForNode(InTx) each accept expectedClaimedBy==""
// as an identity-free admin mode that mutates regardless of the current
// owner — used by the park-timeout sweep and the conductor's
// stale-cleanup, which act on rows they never claimed, and by the
// scheduler's pure-cascade settle
// (lib/graph/scheduler/pure_cascade.go::transitionPureCascade), which
// calls RemoveForNodeInTx(..., "") to retire the node's run row in the
// same tx as the fresh settle — that row is pending+unclaimed by
// construction (nothing on the scheduler path claims pure-cascade
// rows), so there is no owner identity to assert. That mode is
// intentionally unguarded; testClaimantGuardRunEmptyClaimantCarveOut
// pins it so a future change cannot flip its semantics unnoticed.
// Queue.ResumeParkedInTx clears claimed_by but is gated on
// phase='parked', and parked rows are unclaimed by construction
// (ParkActiveInTx clears the claim — asserted in
// testClaimantGuardRunPark), so it carries no ownership to steal.
//
// Two further named carve-outs (pinned by
// testClaimantGuardUnguardedMutationCarveOuts):
//   - ClaimHandles.UpdateNodeRunID carries no claimant guard: the
//     fan-out dispatch path calls it inside the same tx as the
//     child-run INSERT, before any other supervisor can observe the
//     sub-claim, so ownership cannot yet be contested.
//   - ClaimHolders.Complete / CompleteByClaimHandleAndRun carry no
//     claimant guard (only the state='active' idempotency gate): a
//     holder row is retired by the run that holds it, addressed by
//     holder id / (handle, run) pair rather than supervisor identity.
//
// Both mutate ownership-adjacent rows without a guard; the pin test
// keeps them named exceptions rather than silent holes in the "every
// ownership mutation is provably guarded" claim.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const (
	guardSupA = "guard-supervisor-A"
	guardSupB = "guard-supervisor-B"
)

// seedGuardClaimHandle inserts a claim-handle row in its own tx and
// fails the test on error.
func seedGuardClaimHandle(ctx context.Context, t *testing.T, d persistence.Database, in persistence.ClaimHandleInsertInput) {
	t.Helper()
	store := d.Tables()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, in, tx)
	}); err != nil {
		t.Fatalf("seedGuardClaimHandle: %v", err)
	}
}

// guardScopeHandleInput builds a claim-scope-kind insert input owned by
// supID against the fixture node. Scope kind carries every guarded
// column the UPDATE family touches.
func guardScopeHandleInput(fix fixtureSet, supID string, expiresAt time.Time) persistence.ClaimHandleInsertInput {
	producer := "guard-conformance-producer"
	intent := "rw"
	return persistence.ClaimHandleInsertInput{
		ID:                 uuid.New(),
		LockKind:           persistence.LockKindScope,
		ProducerName:       &producer,
		ClaimScopeData:     json.RawMessage(`{"path":"/guard/a"}`),
		Address:            json.RawMessage(`{"addr":"original"}`),
		Payload:            json.RawMessage(`{"payload":"original"}`),
		Intent:             &intent,
		HolderSupervisorID: supID,
		HolderNodeID:       fix.NodeID,
		ExpiresAt:          expiresAt,
	}
}

// getGuardClaimHandle re-reads a claim-handle row; nil when absent.
func getGuardClaimHandle(ctx context.Context, t *testing.T, d persistence.Database, id shared.UUID) *persistence.ClaimHandleRow {
	t.Helper()
	store := d.Tables()
	var row *persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, id, tx)
		row = r
		return err
	}); err != nil {
		t.Fatalf("getGuardClaimHandle: %v", err)
	}
	return row
}

// assertHandleIntact compares the post-wrong-claimant row against the
// pre-mutation snapshot: state, holder, heartbeat, expiry, and every
// guarded payload column must be untouched.
func assertHandleIntact(t *testing.T, got *persistence.ClaimHandleRow, want persistence.ClaimHandleRow, op string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: wrong-claimant mutation removed the row", op)
	}
	if got.State != want.State {
		t.Fatalf("%s: state mutated by wrong claimant: got %q want %q", op, got.State, want.State)
	}
	switch {
	case (got.HolderSupervisorID == nil) != (want.HolderSupervisorID == nil):
		t.Fatalf("%s: holder nullability mutated by wrong claimant", op)
	case got.HolderSupervisorID != nil && *got.HolderSupervisorID != *want.HolderSupervisorID:
		t.Fatalf("%s: holder mutated by wrong claimant: got %q want %q", op, *got.HolderSupervisorID, *want.HolderSupervisorID)
	}
	if !got.LastHeartbeatAt.Equal(want.LastHeartbeatAt) {
		t.Fatalf("%s: last_heartbeat_at mutated by wrong claimant", op)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("%s: expires_at mutated by wrong claimant", op)
	}
	if string(got.Address) != string(want.Address) {
		t.Fatalf("%s: address mutated by wrong claimant: got %s want %s", op, got.Address, want.Address)
	}
	if string(got.Payload) != string(want.Payload) {
		t.Fatalf("%s: payload mutated by wrong claimant: got %s want %s", op, got.Payload, want.Payload)
	}
	if string(got.ClaimScopeData) != string(want.ClaimScopeData) {
		t.Fatalf("%s: claim_scope_data mutated by wrong claimant", op)
	}
	if got.RealizedWriteSemantics != want.RealizedWriteSemantics {
		t.Fatalf("%s: realized_write_semantics mutated by wrong claimant", op)
	}
	if got.VersionID != want.VersionID {
		t.Fatalf("%s: version_id mutated by wrong claimant", op)
	}
	if string(got.AggregationPolicy) != string(want.AggregationPolicy) {
		t.Fatalf("%s: aggregation_policy mutated by wrong claimant", op)
	}
	if got.ExpectedChildrenCount != want.ExpectedChildrenCount ||
		got.CommittedChildrenCount != want.CommittedChildrenCount ||
		got.AbandonedChildrenCount != want.AbandonedChildrenCount {
		t.Fatalf("%s: children counters mutated by wrong claimant", op)
	}
	if (got.ResolvedAt == nil) != (want.ResolvedAt == nil) {
		t.Fatalf("%s: resolved_at mutated by wrong claimant", op)
	}
}

// guardJSONEq compares two JSON documents semantically — postgres
// JSONB normalizes whitespace/key order, so byte equality is too
// strict for asserting that an owner write landed. (The intact
// assertions compare two reads of the same stored value, so byte
// equality is correct there.)
func guardJSONEq(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("guardJSONEq: bad want literal %q: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}

// seedClaimedGuardRun enqueues a run row for nodeID and claims it as
// supID, returning the dispatch id. The row leaves the helper in
// phase='active' claimed_by=supID.
func seedClaimedGuardRun(ctx context.Context, t *testing.T, d persistence.Database, fix fixtureSet, supID string) shared.UUID {
	t.Helper()
	q := d.Queue()
	var dispatchID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         fix.NodeID,
			ExecutorName:   "test-executor",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-1 * time.Second),
			FrameID:        fix.FrameID,
			RunScopeID:     fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             10,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID != fix.NodeID {
				continue
			}
			ok, err := q.ClaimDispatchRow(ctx, tx, c.DispatchID, supID)
			if err != nil {
				return err
			}
			if !ok {
				t.Fatalf("seedClaimedGuardRun: claim was not successful")
			}
			dispatchID = c.DispatchID
			return nil
		}
		t.Fatalf("seedClaimedGuardRun: candidate not surfaced for node %s", fix.NodeID)
		return nil
	}); err != nil {
		t.Fatalf("seedClaimedGuardRun: %v", err)
	}
	return dispatchID
}

// assertRunOwnedBy asserts the dispatch row is still claimed by supID.
func assertRunOwnedBy(ctx context.Context, t *testing.T, d persistence.Database, dispatchID shared.UUID, supID string, op string) {
	t.Helper()
	owner, err := d.Queue().GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("%s: GetClaimedBy: %v", op, err)
	}
	if owner.Kind != "claimed_by" || owner.SupervisorID != supID {
		t.Fatalf("%s: ownership mutated by wrong claimant: got %s/%s want claimed_by/%s",
			op, owner.Kind, owner.SupervisorID, supID)
	}
}

// ---- claim-handle families ----

// testClaimantGuardHandleUpdates covers the guarded column-UPDATE
// family: UpdateAddress, UpdatePayload, UpdateRealizedWriteSemantics,
// UpdateClaimScope, SetVersionID, SetAggregationPolicy.
func testClaimantGuardHandleUpdates(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)
	if before == nil {
		t.Fatalf("seeded handle missing")
	}

	// Wrong claimant: every guarded UPDATE is a no-op.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ch := store.ClaimHandles()
		if err := ch.UpdateAddress(ctx, in.ID, guardSupB, json.RawMessage(`{"addr":"stolen"}`), tx); err != nil {
			return err
		}
		if err := ch.UpdatePayload(ctx, in.ID, guardSupB, json.RawMessage(`{"payload":"stolen"}`), tx); err != nil {
			return err
		}
		if err := ch.UpdateRealizedWriteSemantics(ctx, in.ID, guardSupB, "exclusive", tx); err != nil {
			return err
		}
		if err := ch.UpdateClaimScope(ctx, in.ID, guardSupB, json.RawMessage(`{"path":"/guard/stolen"}`), tx); err != nil {
			return err
		}
		if err := ch.SetVersionID(ctx, in.ID, guardSupB, "v-stolen", tx); err != nil {
			return err
		}
		return ch.SetAggregationPolicy(ctx, in.ID, guardSupB, json.RawMessage(`{"mode":"stolen"}`), tx)
	}); err != nil {
		t.Fatalf("wrong-claimant updates: %v", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "guarded UPDATEs")

	// Owning claimant: every UPDATE lands.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ch := store.ClaimHandles()
		if err := ch.UpdateAddress(ctx, in.ID, guardSupA, json.RawMessage(`{"addr":"updated"}`), tx); err != nil {
			return err
		}
		if err := ch.UpdatePayload(ctx, in.ID, guardSupA, json.RawMessage(`{"payload":"updated"}`), tx); err != nil {
			return err
		}
		if err := ch.UpdateRealizedWriteSemantics(ctx, in.ID, guardSupA, "exclusive", tx); err != nil {
			return err
		}
		if err := ch.UpdateClaimScope(ctx, in.ID, guardSupA, json.RawMessage(`{"path":"/guard/b"}`), tx); err != nil {
			return err
		}
		if err := ch.SetVersionID(ctx, in.ID, guardSupA, "v-2", tx); err != nil {
			return err
		}
		return ch.SetAggregationPolicy(ctx, in.ID, guardSupA, json.RawMessage(`{"mode":"all"}`), tx)
	}); err != nil {
		t.Fatalf("owner updates: %v", err)
	}
	after := getGuardClaimHandle(ctx, t, d, in.ID)
	if after == nil {
		t.Fatalf("handle missing after owner updates")
	}
	if !guardJSONEq(t, after.Address, `{"addr":"updated"}`) {
		t.Fatalf("owner UpdateAddress did not land: %s", after.Address)
	}
	if !guardJSONEq(t, after.Payload, `{"payload":"updated"}`) {
		t.Fatalf("owner UpdatePayload did not land: %s", after.Payload)
	}
	if after.RealizedWriteSemantics != "exclusive" {
		t.Fatalf("owner UpdateRealizedWriteSemantics did not land: %q", after.RealizedWriteSemantics)
	}
	if !guardJSONEq(t, after.ClaimScopeData, `{"path":"/guard/b"}`) {
		t.Fatalf("owner UpdateClaimScope did not land: %s", after.ClaimScopeData)
	}
	if after.VersionID != "v-2" {
		t.Fatalf("owner SetVersionID did not land: %q", after.VersionID)
	}
	if !guardJSONEq(t, after.AggregationPolicy, `{"mode":"all"}`) {
		t.Fatalf("owner SetAggregationPolicy did not land: %s", after.AggregationPolicy)
	}
}

// testClaimantGuardHandleCounterBumps covers BumpExpectedChildrenCount
// and BumpChildOutcomeCount (both outcomes).
func testClaimantGuardHandleCounterBumps(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	lockName := "guard-counter-lock"
	in := persistence.ClaimHandleInsertInput{
		ID:                 uuid.New(),
		LockKind:           persistence.LockKindNamed,
		LockName:           &lockName,
		HolderSupervisorID: guardSupA,
		HolderNodeID:       fix.NodeID,
		ExpiresAt:          time.Now().Add(1 * time.Hour),
	}
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)

	// Wrong claimant: counters stay zero.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ch := store.ClaimHandles()
		if err := ch.BumpExpectedChildrenCount(ctx, in.ID, guardSupB, 3, tx); err != nil {
			return err
		}
		if err := ch.BumpChildOutcomeCount(ctx, in.ID, guardSupB, "commit", 2, tx); err != nil {
			return err
		}
		return ch.BumpChildOutcomeCount(ctx, in.ID, guardSupB, "abandon", 1, tx)
	}); err != nil {
		t.Fatalf("wrong-claimant bumps: %v", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "counter bumps")

	// Owning claimant: counters move.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ch := store.ClaimHandles()
		if err := ch.BumpExpectedChildrenCount(ctx, in.ID, guardSupA, 3, tx); err != nil {
			return err
		}
		if err := ch.BumpChildOutcomeCount(ctx, in.ID, guardSupA, "commit", 2, tx); err != nil {
			return err
		}
		return ch.BumpChildOutcomeCount(ctx, in.ID, guardSupA, "abandon", 1, tx)
	}); err != nil {
		t.Fatalf("owner bumps: %v", err)
	}
	after := getGuardClaimHandle(ctx, t, d, in.ID)
	if after.ExpectedChildrenCount != 3 || after.CommittedChildrenCount != 2 || after.AbandonedChildrenCount != 1 {
		t.Fatalf("owner bumps did not land: expected=%d committed=%d abandoned=%d",
			after.ExpectedChildrenCount, after.CommittedChildrenCount, after.AbandonedChildrenCount)
	}
}

// testClaimantGuardHandlePromote covers Promote.
func testClaimantGuardHandlePromote(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)

	// Wrong claimant: Promote rejects with the illegal-transition error
	// and the row stays active under A.
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, in.ID, guardSupB, spec.ClaimHandleStateCommitted, tx)
	})
	if !errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		t.Fatalf("wrong-claimant Promote: got err %v, want ErrIllegalClaimHandleTransition", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "Promote")

	// Owning claimant: Promote lands — state flips, holder nulls,
	// resolved_at stamps.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, in.ID, guardSupA, spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("owner Promote: %v", err)
	}
	after := getGuardClaimHandle(ctx, t, d, in.ID)
	if after.State != spec.ClaimHandleStateCommitted {
		t.Fatalf("owner Promote did not land: state=%q", after.State)
	}
	if after.HolderSupervisorID != nil {
		t.Fatalf("owner Promote left holder_supervisor_id=%q, want NULL", *after.HolderSupervisorID)
	}
	if after.ResolvedAt == nil {
		t.Fatalf("owner Promote did not stamp resolved_at")
	}
}

// testClaimantGuardHandleReassignHolder covers the
// ReassignHolderSupervisor CAS (the cross-supervisor claim-handoff
// primitive). The guard is a compare-and-swap on the observed holder:
// a wrong `from` rejects with the illegal-transition error and leaves
// the row intact; the right `from` moves the holder; a non-active row
// rejects; an empty `to` is rejected outright.
func testClaimantGuardHandleReassignHolder(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)

	// Wrong `from` (B does not hold the row): CAS rejects, row intact.
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().ReassignHolderSupervisor(ctx, in.ID, guardSupB, "guard-supervisor-C", tx)
	})
	if !errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		t.Fatalf("wrong-from ReassignHolderSupervisor: got err %v, want ErrIllegalClaimHandleTransition", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "ReassignHolderSupervisor")

	// Empty `to`: rejected outright (active rows must carry a holder).
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().ReassignHolderSupervisor(ctx, in.ID, guardSupA, "", tx)
	}); err == nil {
		t.Fatalf("empty-to ReassignHolderSupervisor must be rejected")
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "ReassignHolderSupervisor(empty to)")

	// Correct `from`: the holder moves A → B; nothing else changes.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().ReassignHolderSupervisor(ctx, in.ID, guardSupA, guardSupB, tx)
	}); err != nil {
		t.Fatalf("owner ReassignHolderSupervisor: %v", err)
	}
	after := getGuardClaimHandle(ctx, t, d, in.ID)
	if after.HolderSupervisorID == nil || *after.HolderSupervisorID != guardSupB {
		t.Fatalf("ReassignHolderSupervisor did not move the holder: got %v, want %q", after.HolderSupervisorID, guardSupB)
	}
	if after.State != spec.ClaimHandleStateActive {
		t.Fatalf("ReassignHolderSupervisor must not change state: got %q", after.State)
	}

	// Non-active row: Promote under the new holder, then a further CAS
	// rejects (the state='active' gate).
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, in.ID, guardSupB, spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("Promote under new holder: %v", err)
	}
	err = store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().ReassignHolderSupervisor(ctx, in.ID, guardSupB, guardSupA, tx)
	})
	if !errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		t.Fatalf("non-active ReassignHolderSupervisor: got err %v, want ErrIllegalClaimHandleTransition", err)
	}
}

// testClaimantGuardHandleDelete covers Delete, plus DeleteResolved's
// absence-guard (an active owned row cannot be deleted via the
// non-active path either).
func testClaimantGuardHandleDelete(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)

	// Wrong claimant: Delete is a silent no-op; the row survives intact.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Delete(ctx, in.ID, guardSupB, tx)
	}); err != nil {
		t.Fatalf("wrong-claimant Delete: %v", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "Delete")

	// Absence-guard companion: DeleteResolved must reject the still-
	// active owned row (the holder IS NULL predicate cannot match).
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().DeleteResolved(ctx, in.ID, tx)
	})
	if !errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		t.Fatalf("DeleteResolved on active owned row: got err %v, want ErrIllegalClaimHandleTransition", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "DeleteResolved")

	// Owning claimant: Delete removes the row.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Delete(ctx, in.ID, guardSupA, tx)
	}); err != nil {
		t.Fatalf("owner Delete: %v", err)
	}
	if got := getGuardClaimHandle(ctx, t, d, in.ID); got != nil {
		t.Fatalf("owner Delete did not remove the row")
	}
}

// testClaimantGuardHandleDeleteIfExpired covers DeleteIfExpired: the
// claimant guard AND the expiry gate both individually block the
// delete.
func testClaimantGuardHandleDeleteIfExpired(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	expired := guardScopeHandleInput(fix, guardSupA, time.Now().Add(-1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, expired)
	beforeExpired := getGuardClaimHandle(ctx, t, d, expired.ID)

	lockName := "guard-fresh-lock"
	fresh := persistence.ClaimHandleInsertInput{
		ID:                 uuid.New(),
		LockKind:           persistence.LockKindNamed,
		LockName:           &lockName,
		HolderSupervisorID: guardSupA,
		HolderNodeID:       fix.NodeID,
		ExpiresAt:          time.Now().Add(1 * time.Hour),
	}
	seedGuardClaimHandle(ctx, t, d, fresh)

	// Wrong claimant on the expired row: no-op (deleted=false).
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		deleted, err := store.ClaimHandles().DeleteIfExpired(ctx, expired.ID, guardSupB, tx)
		if err != nil {
			return err
		}
		if deleted {
			t.Fatalf("wrong-claimant DeleteIfExpired reported deleted=true")
		}
		return nil
	}); err != nil {
		t.Fatalf("wrong-claimant DeleteIfExpired: %v", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, expired.ID), *beforeExpired, "DeleteIfExpired")

	// Right claimant but un-expired row: the expiry gate blocks it (a
	// fresh heartbeat must defeat the reaper).
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		deleted, err := store.ClaimHandles().DeleteIfExpired(ctx, fresh.ID, guardSupA, tx)
		if err != nil {
			return err
		}
		if deleted {
			t.Fatalf("DeleteIfExpired deleted an un-expired row")
		}
		return nil
	}); err != nil {
		t.Fatalf("owner DeleteIfExpired (fresh): %v", err)
	}
	if got := getGuardClaimHandle(ctx, t, d, fresh.ID); got == nil {
		t.Fatalf("un-expired row removed by DeleteIfExpired")
	}

	// Right claimant + expired: the delete lands.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		deleted, err := store.ClaimHandles().DeleteIfExpired(ctx, expired.ID, guardSupA, tx)
		if err != nil {
			return err
		}
		if !deleted {
			t.Fatalf("owner DeleteIfExpired on expired row reported deleted=false")
		}
		return nil
	}); err != nil {
		t.Fatalf("owner DeleteIfExpired (expired): %v", err)
	}
	if got := getGuardClaimHandle(ctx, t, d, expired.ID); got != nil {
		t.Fatalf("owner DeleteIfExpired did not remove the expired row")
	}
}

// testClaimantGuardHandleExtendHeartbeat covers ExtendHeartbeat: the
// wrong supervisor cannot refresh expiry on rows it does not hold.
func testClaimantGuardHandleExtendHeartbeat(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// The heartbeat predicate requires a running node-run claimed by
	// the supervisor against the handle's holder node — seed it for A.
	_ = seedClaimedGuardRun(ctx, t, d, fix, guardSupA)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, fix.NodeID, fix.MainRunScopeID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	}); err != nil {
		t.Fatalf("UpdateState(running): %v", err)
	}

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Minute))
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)

	// Wrong claimant: expiry unchanged.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().ExtendHeartbeat(ctx, guardSupB, time.Now().Add(1*time.Hour), tx)
	}); err != nil {
		t.Fatalf("wrong-claimant ExtendHeartbeat: %v", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "ExtendHeartbeat")

	// Owning claimant: expiry extends past the seeded one-minute window.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().ExtendHeartbeat(ctx, guardSupA, time.Now().Add(1*time.Hour), tx)
	}); err != nil {
		t.Fatalf("owner ExtendHeartbeat: %v", err)
	}
	after := getGuardClaimHandle(ctx, t, d, in.ID)
	if !after.ExpiresAt.After(before.ExpiresAt) {
		t.Fatalf("owner ExtendHeartbeat did not extend expires_at: before=%v after=%v",
			before.ExpiresAt, after.ExpiresAt)
	}
	// The seeded heartbeat comes from the Go clock while ExtendHeartbeat
	// stamps the database server's now() — on postgres those are
	// different clock sources, so allow small skew rather than requiring
	// strict monotonicity. The owner-success proof is the expires_at
	// extension above; this only guards against gross regression.
	if after.LastHeartbeatAt.Before(before.LastHeartbeatAt.Add(-2 * time.Second)) {
		t.Fatalf("owner ExtendHeartbeat moved last_heartbeat_at backwards: before=%v after=%v",
			before.LastHeartbeatAt, after.LastHeartbeatAt)
	}
}

// testClaimantGuardHolderRelease covers
// ClaimHolderTable.FailAllActiveByClaimHandle — the bulk holder release
// is guarded through the parent handle's ownership (EXISTS sub-query).
func testClaimantGuardHolderRelease(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	in.IsHeld = true
	seedGuardClaimHandle(ctx, t, d, in)

	holderID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            holderID,
			ClaimHandleID: in.ID,
			HolderRunID:   runID,
			FrameID:       &fix.FrameID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim holder: %v", err)
	}

	getHolder := func() persistence.ClaimHolderRow {
		t.Helper()
		var row *persistence.ClaimHolderRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.ClaimHolders().Get(ctx, holderID, tx)
			row = r
			return err
		}); err != nil {
			t.Fatalf("get holder: %v", err)
		}
		if row == nil {
			t.Fatalf("holder row missing")
		}
		return *row
	}

	// Wrong claimant: every holder row stays active, completed_at NULL.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().FailAllActiveByClaimHandle(ctx, in.ID, guardSupB, tx)
	}); err != nil {
		t.Fatalf("wrong-claimant FailAllActiveByClaimHandle: %v", err)
	}
	h := getHolder()
	if h.State != persistence.ClaimHolderStateActive || h.CompletedAt != nil {
		t.Fatalf("wrong-claimant holder release mutated the row: state=%q completed_at=%v", h.State, h.CompletedAt)
	}

	// Owning claimant: the holder flips to failed with completed_at set.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().FailAllActiveByClaimHandle(ctx, in.ID, guardSupA, tx)
	}); err != nil {
		t.Fatalf("owner FailAllActiveByClaimHandle: %v", err)
	}
	h = getHolder()
	if h.State != persistence.ClaimHolderStateFailed || h.CompletedAt == nil {
		t.Fatalf("owner holder release did not land: state=%q completed_at=%v", h.State, h.CompletedAt)
	}
}

// ---- node-run families ----

// testClaimantGuardRunClaimSteal covers ClaimDispatchRow's
// claimed_by-IS-NULL gate: a second supervisor cannot steal an
// already-claimed dispatch row.
func testClaimantGuardRunClaimSteal(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, dispatchID, guardSupB)
		if err != nil {
			return err
		}
		if ok {
			t.Fatalf("ClaimDispatchRow stole an already-claimed row")
		}
		return nil
	}); err != nil {
		t.Fatalf("steal tx: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "ClaimDispatchRow")
}

// testClaimantGuardRunReleaseClaim covers ReleaseClaim.
func testClaimantGuardRunReleaseClaim(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	beforeRow, err := q.GetByID(ctx, dispatchID)
	if err != nil || beforeRow == nil {
		t.Fatalf("GetByID before: row=%v err=%v", beforeRow, err)
	}

	// Wrong claimant: ownership AND heartbeat untouched.
	if err := q.ReleaseClaim(ctx, dispatchID, guardSupB); err != nil {
		t.Fatalf("wrong-claimant ReleaseClaim: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "ReleaseClaim")
	afterRow, err := q.GetByID(ctx, dispatchID)
	if err != nil || afterRow == nil {
		t.Fatalf("GetByID after wrong-claimant release: row=%v err=%v", afterRow, err)
	}
	if (afterRow.LastHeartbeatAt == nil) != (beforeRow.LastHeartbeatAt == nil) ||
		(afterRow.LastHeartbeatAt != nil && !afterRow.LastHeartbeatAt.Equal(*beforeRow.LastHeartbeatAt)) {
		t.Fatalf("wrong-claimant ReleaseClaim mutated last_heartbeat_at")
	}

	// Owning claimant: the row releases back to unclaimed/pending.
	if err := q.ReleaseClaim(ctx, dispatchID, guardSupA); err != nil {
		t.Fatalf("owner ReleaseClaim: %v", err)
	}
	owner, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetClaimedBy after owner release: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("owner ReleaseClaim did not release: %s/%s", owner.Kind, owner.SupervisorID)
	}
}

// testClaimantGuardRunComplete covers Complete (the terminal-phase
// flip).
func testClaimantGuardRunComplete(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	// Wrong claimant: the row stays in-flight, claimed by A, heartbeat
	// intact.
	if err := q.Complete(ctx, dispatchID, guardSupB); err != nil {
		t.Fatalf("wrong-claimant Complete: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "Complete")
	row, err := q.GetByID(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetByID after wrong-claimant Complete: %v", err)
	}
	if row == nil {
		t.Fatalf("wrong-claimant Complete retired the row")
	}
	if row.LastHeartbeatAt == nil {
		t.Fatalf("wrong-claimant Complete cleared last_heartbeat_at")
	}

	// Owning claimant: the row leaves the in-flight phases.
	if err := q.Complete(ctx, dispatchID, guardSupA); err != nil {
		t.Fatalf("owner Complete: %v", err)
	}
	row, err = q.GetByID(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetByID after owner Complete: %v", err)
	}
	if row != nil {
		t.Fatalf("owner Complete did not retire the row")
	}
}

// testClaimantGuardRunRemoveForNode covers RemoveForNode /
// RemoveForNodeInTx.
func testClaimantGuardRunRemoveForNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	// Wrong claimant (in-tx variant): the row stays in-flight under A.
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, fix.NodeID, fix.MainRunScopeID, guardSupB, tx)
	}); err != nil {
		t.Fatalf("wrong-claimant RemoveForNodeInTx: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "RemoveForNodeInTx")
	row, err := q.GetByID(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetByID after wrong-claimant remove: %v", err)
	}
	if row == nil {
		t.Fatalf("wrong-claimant RemoveForNodeInTx retired the row")
	}
	if row.LastHeartbeatAt == nil {
		t.Fatalf("wrong-claimant RemoveForNodeInTx cleared last_heartbeat_at")
	}

	// Owning claimant (auto-commit wrapper): the row retires.
	if err := q.RemoveForNode(ctx, fix.NodeID, fix.MainRunScopeID, guardSupA); err != nil {
		t.Fatalf("owner RemoveForNode: %v", err)
	}
	row, err = q.GetByID(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetByID after owner remove: %v", err)
	}
	if row != nil {
		t.Fatalf("owner RemoveForNode did not retire the row")
	}
}

// testClaimantGuardRunPark covers ParkActiveInTx.
func testClaimantGuardRunPark(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	parkInput := func(sup string) persistence.ParkActiveInput {
		return persistence.ParkActiveInput{
			DispatchID:        dispatchID,
			ExpectedClaimedBy: sup,
			ParkedAt:          time.Now(),
			Reason:            "snooze",
			ResumeAt:          time.Now().Add(1 * time.Hour),
		}
	}

	// Wrong claimant: both drivers reject (RowsAffected != 1 is an
	// error for this transition) and the row stays active under A.
	err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.ParkActiveInTx(ctx, tx, parkInput(guardSupB))
	})
	if err == nil {
		t.Fatalf("wrong-claimant ParkActiveInTx did not error")
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "ParkActiveInTx")
	parked, perr := q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID)
	if perr != nil {
		t.Fatalf("GetParkedByNode after wrong-claimant park: %v", perr)
	}
	if parked != nil {
		t.Fatalf("wrong-claimant ParkActiveInTx parked the row")
	}

	// Owning claimant: the park lands (phase parked, claim cleared).
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.ParkActiveInTx(ctx, tx, parkInput(guardSupA))
	}); err != nil {
		t.Fatalf("owner ParkActiveInTx: %v", err)
	}
	parked, perr = q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID)
	if perr != nil {
		t.Fatalf("GetParkedByNode after owner park: %v", perr)
	}
	if parked == nil || parked.DispatchID != dispatchID {
		t.Fatalf("owner ParkActiveInTx did not park the row: %v", parked)
	}
	owner, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetClaimedBy after park: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("park did not clear claimed_by: %s/%s", owner.Kind, owner.SupervisorID)
	}
}

// testClaimantGuardRunRefreshHeartbeat covers RefreshHeartbeat: the
// supervisor-scoped WHERE means another supervisor's refresh never
// touches A's rows.
func testClaimantGuardRunRefreshHeartbeat(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	before, err := q.GetByID(ctx, dispatchID)
	if err != nil || before == nil || before.LastHeartbeatAt == nil {
		t.Fatalf("GetByID before refresh: row=%v err=%v", before, err)
	}

	// Wrong supervisor: A's heartbeat is untouched.
	if err := q.RefreshHeartbeat(ctx, guardSupB); err != nil {
		t.Fatalf("wrong-claimant RefreshHeartbeat: %v", err)
	}
	mid, err := q.GetByID(ctx, dispatchID)
	if err != nil || mid == nil || mid.LastHeartbeatAt == nil {
		t.Fatalf("GetByID after wrong-claimant refresh: row=%v err=%v", mid, err)
	}
	if !mid.LastHeartbeatAt.Equal(*before.LastHeartbeatAt) {
		t.Fatalf("wrong-claimant RefreshHeartbeat mutated last_heartbeat_at: before=%v after=%v",
			before.LastHeartbeatAt, mid.LastHeartbeatAt)
	}

	// Owning supervisor: the heartbeat advances. The short sleep
	// guarantees a strictly-later timestamp at the drivers' stored
	// precision (microseconds on postgres).
	time.Sleep(20 * time.Millisecond)
	if err := q.RefreshHeartbeat(ctx, guardSupA); err != nil {
		t.Fatalf("owner RefreshHeartbeat: %v", err)
	}
	after, err := q.GetByID(ctx, dispatchID)
	if err != nil || after == nil || after.LastHeartbeatAt == nil {
		t.Fatalf("GetByID after owner refresh: row=%v err=%v", after, err)
	}
	if !after.LastHeartbeatAt.After(*before.LastHeartbeatAt) {
		t.Fatalf("owner RefreshHeartbeat did not advance last_heartbeat_at: before=%v after=%v",
			before.LastHeartbeatAt, after.LastHeartbeatAt)
	}
}

// testClaimantGuardNodeUpdateHeartbeat covers Nodes().UpdateHeartbeat —
// the node-keyed heartbeat write that also stamps claimed_by. The wrong
// supervisor must neither overwrite the owner's claimed_by (an
// ownership steal) nor refresh the heartbeat (which would defeat the
// orphan reaper); the owner's refresh lands; an unclaimed in-flight row
// remains stampable (the post-acquisition initial-stamp path).
func testClaimantGuardNodeUpdateHeartbeat(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	before, err := q.GetByID(ctx, dispatchID)
	if err != nil || before == nil || before.LastHeartbeatAt == nil {
		t.Fatalf("GetByID before: row=%v err=%v", before, err)
	}

	// Wrong supervisor: claimed_by stays A, last_heartbeat_at untouched
	// — even with a deliberately-future timestamp.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateHeartbeat(ctx, fix.NodeID, fix.MainRunScopeID,
			before.LastHeartbeatAt.Add(1*time.Hour), guardSupB, tx)
	}); err != nil {
		t.Fatalf("wrong-claimant Nodes.UpdateHeartbeat: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "Nodes.UpdateHeartbeat")
	mid, err := q.GetByID(ctx, dispatchID)
	if err != nil || mid == nil || mid.LastHeartbeatAt == nil {
		t.Fatalf("GetByID after wrong-claimant heartbeat: row=%v err=%v", mid, err)
	}
	if !mid.LastHeartbeatAt.Equal(*before.LastHeartbeatAt) {
		t.Fatalf("wrong-claimant Nodes.UpdateHeartbeat mutated last_heartbeat_at: before=%v after=%v",
			before.LastHeartbeatAt, mid.LastHeartbeatAt)
	}

	// Owning supervisor: the refresh lands and ownership is unchanged.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateHeartbeat(ctx, fix.NodeID, fix.MainRunScopeID,
			before.LastHeartbeatAt.Add(1*time.Hour), guardSupA, tx)
	}); err != nil {
		t.Fatalf("owner Nodes.UpdateHeartbeat: %v", err)
	}
	after, err := q.GetByID(ctx, dispatchID)
	if err != nil || after == nil || after.LastHeartbeatAt == nil {
		t.Fatalf("GetByID after owner heartbeat: row=%v err=%v", after, err)
	}
	if !after.LastHeartbeatAt.After(*before.LastHeartbeatAt) {
		t.Fatalf("owner Nodes.UpdateHeartbeat did not advance last_heartbeat_at: before=%v after=%v",
			before.LastHeartbeatAt, after.LastHeartbeatAt)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "owner Nodes.UpdateHeartbeat")

	// Unclaimed row: the initial-stamp arm (claimed_by IS NULL) still
	// works — release as A, then B stamps the now-unclaimed row.
	if err := q.ReleaseClaim(ctx, dispatchID, guardSupA); err != nil {
		t.Fatalf("ReleaseClaim(A): %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateHeartbeat(ctx, fix.NodeID, fix.MainRunScopeID,
			time.Now(), guardSupB, tx)
	}); err != nil {
		t.Fatalf("stamp-unclaimed Nodes.UpdateHeartbeat: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupB, "stamp-unclaimed Nodes.UpdateHeartbeat")
}

// testClaimantGuardRunEmptyClaimantCarveOut pins the named carve-out:
// Queue.ReleaseClaim / Complete / RemoveForNodeInTx with
// expectedClaimedBy=="" are identity-free admin modes that mutate
// regardless of the current owner (the park-timeout sweep and the
// conductor's stale-cleanup act on rows they never claimed; the
// scheduler's pure-cascade settle retires a pending+unclaimed row that
// has no owner to assert). This test
// exists so the carve-out is a pinned, named exception rather than a
// silent hole in the "every ownership mutation is guarded" claim — if
// the empty mode ever grows a guard (or a guarded path ever loses one),
// this fails loudly on both drivers.
func testClaimantGuardRunEmptyClaimantCarveOut(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	// ReleaseClaim(""): releases A's claim despite carrying no identity.
	if err := q.ReleaseClaim(ctx, dispatchID, ""); err != nil {
		t.Fatalf("empty-claimant ReleaseClaim: %v", err)
	}
	owner, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetClaimedBy after empty release: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("empty-claimant ReleaseClaim did not release A's row: %s/%s", owner.Kind, owner.SupervisorID)
	}

	// Complete(""): retires A's re-claimed row despite carrying no
	// identity.
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, dispatchID, guardSupA)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("re-claim as A failed")
		}
		return nil
	}); err != nil {
		t.Fatalf("re-claim tx: %v", err)
	}
	if err := q.Complete(ctx, dispatchID, ""); err != nil {
		t.Fatalf("empty-claimant Complete: %v", err)
	}
	row, err := q.GetByID(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetByID after empty Complete: %v", err)
	}
	if row != nil {
		t.Fatalf("empty-claimant Complete did not retire A's row")
	}

	// RemoveForNodeInTx(""): retires a fresh claimed row despite
	// carrying no identity (the sweep-path shape).
	dispatchID2 := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, fix.NodeID, fix.MainRunScopeID, "", tx)
	}); err != nil {
		t.Fatalf("empty-claimant RemoveForNodeInTx: %v", err)
	}
	row, err = q.GetByID(ctx, dispatchID2)
	if err != nil {
		t.Fatalf("GetByID after empty remove: %v", err)
	}
	if row != nil {
		t.Fatalf("empty-claimant RemoveForNodeInTx did not retire A's row")
	}
}

// testClaimantGuardUnguardedMutationCarveOuts pins the two named
// unguarded mutation families (see the package header): a mutation by
// a process that is NOT the handle's holder must still land, because
// these operations are addressed by row identity rather than
// supervisor identity. If either ever grows a claimant guard (or the
// suite's coverage claim changes), this fails loudly on both drivers.
func testClaimantGuardUnguardedMutationCarveOuts(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	// ClaimHandles.UpdateNodeRunID: repoints the FK on a handle owned by
	// A without any claimant identity.
	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, in)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHandles().UpdateNodeRunID(ctx, in.ID, runID, tx)
	}); err != nil {
		t.Fatalf("UpdateNodeRunID carve-out: %v", err)
	}
	h := getGuardClaimHandle(ctx, t, d, in.ID)
	if h == nil || h.NodeRunID == nil || *h.NodeRunID != runID {
		t.Fatalf("UpdateNodeRunID carve-out did not repoint node_run_id: %+v", h)
	}

	// ClaimHolders.Complete: retires an active holder under A's handle
	// without any claimant identity.
	holderID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            holderID,
			ClaimHandleID: in.ID,
			HolderRunID:   runID,
			FrameID:       &fix.FrameID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim holder: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().Complete(ctx, holderID, persistence.ClaimHolderStateCompleted, tx)
	}); err != nil {
		t.Fatalf("Complete carve-out: %v", err)
	}
	var holder *persistence.ClaimHolderRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHolders().Get(ctx, holderID, tx)
		holder = r
		return err
	}); err != nil {
		t.Fatalf("get holder after Complete: %v", err)
	}
	if holder == nil || holder.State != persistence.ClaimHolderStateCompleted || holder.CompletedAt == nil {
		t.Fatalf("Complete carve-out did not retire the holder: %+v", holder)
	}

	// ClaimHolders.CompleteByClaimHandleAndRun: same family, addressed
	// by the (handle, run) pair. Seeded under a second handle —
	// (claim_handle_id, holder_run_id) is unique.
	in2 := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	in2.ClaimScopeData = json.RawMessage(`{"path":"/guard/b"}`)
	seedGuardClaimHandle(ctx, t, d, in2)
	holderID2 := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            holderID2,
			ClaimHandleID: in2.ID,
			HolderRunID:   runID,
			FrameID:       &fix.FrameID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed second claim holder: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().CompleteByClaimHandleAndRun(ctx, in2.ID, runID, persistence.ClaimHolderStateFailed, tx)
	}); err != nil {
		t.Fatalf("CompleteByClaimHandleAndRun carve-out: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHolders().Get(ctx, holderID2, tx)
		holder = r
		return err
	}); err != nil {
		t.Fatalf("get holder after CompleteByClaimHandleAndRun: %v", err)
	}
	if holder == nil || holder.State != persistence.ClaimHolderStateFailed || holder.CompletedAt == nil {
		t.Fatalf("CompleteByClaimHandleAndRun carve-out did not retire the holder: %+v", holder)
	}
}
