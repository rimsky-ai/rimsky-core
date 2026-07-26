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
)

const reaperLivenessSup = "reaper-liveness-supervisor"

func insertExpiredClaimForRun(
	ctx context.Context, t *testing.T, store persistence.Tables,
	nodeID, runID shared.UUID, lockName string,
) shared.UUID {
	t.Helper()
	id := shared.UUID(uuid.New())
	run := runID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 id,
			NodeRunID:          &run,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: reaperLivenessSup,
			HolderNodeID:       nodeID,
			ExpiresAt:          time.Now().Add(-1 * time.Hour),
		}, tx)
	}); err != nil {
		t.Fatalf("insertExpiredClaimForRun: %v", err)
	}
	return id
}

func listExpiredContains(ctx context.Context, t *testing.T, store persistence.Tables, id shared.UUID) bool {
	t.Helper()
	var expired []persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.ClaimHandles().ListExpired(ctx, tx)
		expired = rows
		return err
	}); err != nil {
		t.Fatalf("ListExpired: %v", err)
	}
	for _, r := range expired {
		if r.ID == id {
			return true
		}
	}
	return false
}

// @concept: orphan-reaper
// @concept: parked-state
func testReaperSkipsParkedHolder(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, reaperLivenessSup)
	claimID := insertExpiredClaimForRun(ctx, t, store, fix.NodeID, runID, "reaper-parked-lock")

	if !listExpiredContains(ctx, t, store, claimID) {
		t.Fatalf("expired claim held by a running run must be reapable (ListExpired should surface it)")
	}

	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID:         runID,
		ExpectedClaimedBy: reaperLivenessSup,
		ParkedAt:          time.Now(),
		ResumeAt:          time.Now().Add(24 * time.Hour),
	})

	if listExpiredContains(ctx, t, store, claimID) {
		t.Fatalf("a claim whose holder run is parked must NOT be reaped (parked runs hold their claim across the park)")
	}

	deleted := false
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		deleted, err = store.ClaimHandles().DeleteIfExpired(ctx, claimID, reaperLivenessSup, tx)
		return err
	}); err != nil {
		t.Fatalf("DeleteIfExpired: %v", err)
	}
	if deleted {
		t.Fatalf("DeleteIfExpired must refuse to delete a parked holder's claim")
	}
}

// @concept: orphan-reaper
func testRenewExpiryForHolderRun(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, reaperLivenessSup)
	claimID := insertExpiredClaimForRun(ctx, t, store, fix.NodeID, runID, "reaper-renew-lock")

	if !listExpiredContains(ctx, t, store, claimID) {
		t.Fatalf("precondition: freshly-inserted past-expiry claim must be reapable")
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHandles().RenewExpiryForHolderRun(ctx, runID, time.Now().Add(1*time.Hour), tx)
	}); err != nil {
		t.Fatalf("RenewExpiryForHolderRun: %v", err)
	}

	if listExpiredContains(ctx, t, store, claimID) {
		t.Fatalf("after renewal the claim's expiry is in the future; ListExpired must no longer surface it")
	}
}

func listOrphanedClaimsContains(ctx context.Context, t *testing.T, d persistence.Database, runID shared.UUID) bool {
	t.Helper()
	rows, err := d.Queue().ListOrphanedClaims(ctx)
	if err != nil {
		t.Fatalf("ListOrphanedClaims: %v", err)
	}
	for _, r := range rows {
		if r.ID == runID {
			return true
		}
	}
	return false
}

// @concept: orphan-reaper
// @concept: parked-state
func testSweepExecutorDeadlinesSkipsParkedRow(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, reaperLivenessSup)

	maxQuiet := 30
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RegisterAsyncAck(ctx, runID, "orphan-liveness-ack", time.Now().Add(-1*time.Hour), &maxQuiet, nil, "", "http://supervisor.internal:9099", tx)
	}); err != nil {
		t.Fatalf("RegisterAsyncAck: %v", err)
	}

	if !listOrphanedClaimsContains(ctx, t, d, runID) {
		t.Fatalf("precondition: a claimed run with a registered async ack and no recent progress must be a sweep candidate")
	}

	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runID, ExpectedClaimedBy: reaperLivenessSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(24 * time.Hour),
	})

	if listOrphanedClaimsContains(ctx, t, d, runID) {
		t.Fatalf("a parked run must be un-reapable: SweepExecutorDeadlines must not surface it via ListOrphanedClaims")
	}
}

// @concept: claim-co-holdership
func testCoHolderInsertIdempotent(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, reaperLivenessSup)

	claimID := shared.UUID(uuid.New())
	lockName := "co-holder-idempotent-lock"
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: reaperLivenessSup,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim handle: %v", err)
	}

	insertHolder := func() error {
		return inTx(ctx, store, func(tx persistence.Tx) error {
			return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
				ID:              shared.UUID(uuid.New()),
				ClaimHandleID:   claimID,
				HolderNodeRunID: runID,
			}, tx)
		})
	}

	if err := insertHolder(); err != nil {
		t.Fatalf("first co-holder insert: %v", err)
	}
	if err := insertHolder(); err != nil {
		t.Fatalf("re-inserting the same (claim_handle_id, holder_run_id) co-holder must be idempotent, got: %v", err)
	}

	var holders []persistence.ClaimHolderRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.ClaimHolders().ListByClaimHandleID(ctx, claimID, tx)
		holders = rows
		return err
	}); err != nil {
		t.Fatalf("ListByClaimHandleID: %v", err)
	}
	if len(holders) != 1 {
		t.Fatalf("idempotent co-holder insert must leave exactly one holder row, got %d", len(holders))
	}
}
