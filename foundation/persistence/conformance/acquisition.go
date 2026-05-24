// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// acquisition.go — AcquisitionTxAtomicity conformance area.
//
// Inv 10: lock acquisition is atomic with dispatch claim. The supervisor's
// §7.3 acquisition transaction either claims dispatch + INSERTs all
// required lock-holder rows + UPDATEs lock-holder addresses, or none of
// these.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

func testAcquisitionTxAtomicity(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	store := d.Tables()

	if err := q.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:         fix.NodeID,
		ExecutorName:   "test-executor",
		RequiredStores: []string{},
		EnqueuedAt:     time.Now().Add(-1 * time.Second),
		FrameID:        fix.FrameID,
		RunScopeID:     fix.MainRunScopeID,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	supID := "acquisition-supervisor"
	lockHolderID := uuid.New()
	lockName := "acquisition-lock"
	rollbackErr := errors.New("rollback the whole acquisition")

	// Roll back: claim dispatch + insert lock-holder + update address all
	// inside a tx that returns an error. None should land.
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             10,
		})
		if err != nil {
			return err
		}
		if len(cands) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(cands))
		}
		ok, err := q.ClaimDispatchRow(ctx, tx, cands[0].DispatchID, supID)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow: not claimed")
		}
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		if err := store.ClaimHandles().UpdateAddress(ctx, lockHolderID, supID,
			json.RawMessage(`{"addr":"x"}`), tx); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("expected rollback err, got %v", err)
	}

	// Verify nothing landed.
	var got *persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, lockHolderID, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get lock-holder: %v", err)
	}
	if got != nil {
		t.Fatalf("rollback failed: lock-holder %s present", lockHolderID)
	}
	// Find dispatch row + verify unclaimed.
	rows, err := q.ListOrphanedClaims(ctx, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("ListOrphanedClaims: %v", err)
	}
	for _, r := range rows {
		if r.NodeID == fix.NodeID && r.ClaimedBy != nil {
			t.Fatalf("rollback failed: dispatch is claimed by %v", *r.ClaimedBy)
		}
	}

	// Commit: same operations, but return nil. All three must land.
	addressBytes := json.RawMessage(`{"addr":"committed"}`)
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             10,
		})
		if err != nil {
			return err
		}
		ok, err := q.ClaimDispatchRow(ctx, tx, cands[0].DispatchID, supID)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow #2: not claimed")
		}
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		if err := store.ClaimHandles().UpdateAddress(ctx, lockHolderID, supID, addressBytes, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	var got2 *persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, lockHolderID, tx)
		got2 = r
		return err
	}); err != nil {
		t.Fatalf("Get lock-holder #2: %v", err)
	}
	if got2 == nil {
		t.Fatalf("commit failed: lock-holder %s absent", lockHolderID)
	}
	if !jsonEqual(got2.Address, addressBytes) {
		t.Fatalf("address mismatch: got %q want %q", string(got2.Address), string(addressBytes))
	}
}
