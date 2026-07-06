// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func seedGuardClaimHandle(ctx context.Context, t *testing.T, d persistence.Database, in persistence.ClaimHandleInsertInput) {
	t.Helper()
	store := d.Tables()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, in, tx)
	}); err != nil {
		t.Fatalf("seedGuardClaimHandle: %v", err)
	}
}

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

func seedClaimedGuardRun(ctx context.Context, t *testing.T, d persistence.Database, fix fixtureSet, supID string) shared.UUID {
	t.Helper()
	q := d.Queue()
	var dispatchID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 fix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  10,
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
			if _, err := q.PromoteClaimedToRunning(ctx, tx, c.DispatchID, supID); err != nil {
				return err
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

func testClaimantGuardHandlePromote(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)

	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, in.ID, guardSupB, spec.ClaimHandleStateCommitted, tx)
	})
	if !errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		t.Fatalf("wrong-claimant Promote: got err %v, want ErrIllegalClaimHandleTransition", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "Promote")

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

func testClaimantGuardHandleReassignHolder(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)

	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().ReassignHolderSupervisor(ctx, in.ID, guardSupB, "guard-supervisor-C", tx)
	})
	if !errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		t.Fatalf("wrong-from ReassignHolderSupervisor: got err %v, want ErrIllegalClaimHandleTransition", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "ReassignHolderSupervisor")

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().ReassignHolderSupervisor(ctx, in.ID, guardSupA, "", tx)
	}); err == nil {
		t.Fatalf("empty-to ReassignHolderSupervisor must be rejected")
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "ReassignHolderSupervisor(empty to)")

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

func testClaimantGuardHandleDelete(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	in := guardScopeHandleInput(fix, guardSupA, time.Now().Add(1*time.Hour))
	seedGuardClaimHandle(ctx, t, d, in)
	before := getGuardClaimHandle(ctx, t, d, in.ID)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Delete(ctx, in.ID, guardSupB, tx)
	}); err != nil {
		t.Fatalf("wrong-claimant Delete: %v", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "Delete")

	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().DeleteResolved(ctx, in.ID, tx)
	})
	if !errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		t.Fatalf("DeleteResolved on active owned row: got err %v, want ErrIllegalClaimHandleTransition", err)
	}
	assertHandleIntact(t, getGuardClaimHandle(ctx, t, d, in.ID), *before, "DeleteResolved")

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Delete(ctx, in.ID, guardSupA, tx)
	}); err != nil {
		t.Fatalf("owner Delete: %v", err)
	}
	if got := getGuardClaimHandle(ctx, t, d, in.ID); got != nil {
		t.Fatalf("owner Delete did not remove the row")
	}
}

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

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().FailAllActiveByClaimHandle(ctx, in.ID, guardSupB, tx)
	}); err != nil {
		t.Fatalf("wrong-claimant FailAllActiveByClaimHandle: %v", err)
	}
	h := getHolder()
	if h.State != persistence.ClaimHolderStateActive || h.CompletedAt != nil {
		t.Fatalf("wrong-claimant holder release mutated the row: state=%q completed_at=%v", h.State, h.CompletedAt)
	}

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

func testClaimantGuardRunClaimSelfIdempotent(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	var dispatchID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 fix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  10,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID != fix.NodeID {
				continue
			}
			ok, err := q.ClaimDispatchRow(ctx, tx, c.DispatchID, guardSupA)
			if err != nil {
				return err
			}
			if !ok {
				t.Fatalf("seed: first claim must succeed")
			}
			dispatchID = c.DispatchID
			return nil
		}
		t.Fatalf("seed: candidate not surfaced for node %s", fix.NodeID)
		return nil
	}); err != nil {
		t.Fatalf("seed tx: %v", err)
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, dispatchID, guardSupA)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("same-supervisor re-claim must return ok=true (in-place retry path requires self-idempotency: row stays stale + claimed-by-self between Open retries)")
		}
		return nil
	}); err != nil {
		t.Fatalf("self-reclaim tx: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "ClaimDispatchRow self-reclaim")
}

func testClaimantGuardRunReleaseClaim(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	beforeRow, err := q.GetByID(ctx, dispatchID)
	if err != nil || beforeRow == nil {
		t.Fatalf("GetByID before: row=%v err=%v", beforeRow, err)
	}

	if err := q.ReleaseClaim(ctx, dispatchID, guardSupB); err != nil {
		t.Fatalf("wrong-claimant ReleaseClaim: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "ReleaseClaim")
	afterRow, err := q.GetByID(ctx, dispatchID)
	if err != nil || afterRow == nil {
		t.Fatalf("GetByID after wrong-claimant release: row=%v err=%v", afterRow, err)
	}
	if (afterRow.LastProgressAt == nil) != (beforeRow.LastProgressAt == nil) ||
		(afterRow.LastProgressAt != nil && !afterRow.LastProgressAt.Equal(*beforeRow.LastProgressAt)) {
		t.Fatalf("wrong-claimant ReleaseClaim mutated last_progress_at")
	}

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

func testClaimantGuardRunComplete(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	if err := q.Complete(ctx, dispatchID, guardSupB); err != nil {
		t.Fatalf("wrong-claimant Complete: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "Complete")

	if err := q.Complete(ctx, dispatchID, guardSupA); err != nil {
		t.Fatalf("owner Complete: %v", err)
	}
	owner, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetClaimedBy after owner Complete: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("owner Complete did not release claim: %s/%s", owner.Kind, owner.SupervisorID)
	}
}

func testClaimantGuardRunRemoveForNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, fix.NodeID, fix.MainRunScopeID, guardSupB, tx)
	}); err != nil {
		t.Fatalf("wrong-claimant RemoveForNodeInTx: %v", err)
	}
	assertRunOwnedBy(ctx, t, d, dispatchID, guardSupA, "RemoveForNodeInTx")

	if err := q.RemoveForNode(ctx, fix.NodeID, fix.MainRunScopeID, guardSupA); err != nil {
		t.Fatalf("owner RemoveForNode: %v", err)
	}
	owner, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetClaimedBy after owner remove: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("owner RemoveForNode did not release claim: %s/%s", owner.Kind, owner.SupervisorID)
	}
}

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

	parkRun(ctx, t, d, parkInput(guardSupA))
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

func testClaimantGuardRunEmptyClaimantCarveOut(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	dispatchID := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)

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

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, dispatchID, guardSupA)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("re-claim as A failed")
		}
		_, err = q.PromoteClaimedToRunning(ctx, tx, dispatchID, guardSupA)
		return err
	}); err != nil {
		t.Fatalf("re-claim tx: %v", err)
	}
	if err := q.Complete(ctx, dispatchID, ""); err != nil {
		t.Fatalf("empty-claimant Complete: %v", err)
	}
	cowner, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetClaimedBy after empty Complete: %v", err)
	}
	if cowner.Kind != "unclaimed" {
		t.Fatalf("empty-claimant Complete did not release A's row: %s/%s", cowner.Kind, cowner.SupervisorID)
	}
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Tables().Nodes().UpdateState(ctx, dispatchID,
			cascade.NodeStateFresh, cascade.ReasonHandlerComplete, nil, tx)
	}); err != nil {
		t.Fatalf("settle row after empty Complete: %v", err)
	}

	dispatchID2 := seedClaimedGuardRun(ctx, t, d, fix, guardSupA)
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, fix.NodeID, fix.MainRunScopeID, "", tx)
	}); err != nil {
		t.Fatalf("empty-claimant RemoveForNodeInTx: %v", err)
	}
	rowner, err := q.GetClaimedBy(ctx, dispatchID2)
	if err != nil {
		t.Fatalf("GetClaimedBy after empty remove: %v", err)
	}
	if rowner.Kind != "unclaimed" {
		t.Fatalf("empty-claimant RemoveForNodeInTx did not release A's row: %s/%s", rowner.Kind, rowner.SupervisorID)
	}
}

func testClaimantGuardUnguardedMutationCarveOuts(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

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
