// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testAcquisitionTxAtomicity(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	store := d.Tables()

	if err := q.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:                 fix.NodeID,
		ExecutorName:           "test-executor",
		RequiredClaimProducers: []string{},
		EnqueuedAt:             time.Now().Add(-1 * time.Second),
		FrameID:                fix.FrameID,
		RunScopeID:             fix.MainRunScopeID,
	}, nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	supID := "acquisition-supervisor"
	claimHandleID := uuid.New()
	lockName := "acquisition-lock"
	rollbackErr := errors.New("rollback the whole acquisition")

	var claimedNodeRunID shared.UUID
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  10,
		}, tx)
		if err != nil {
			return err
		}
		if len(cands) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(cands))
		}
		claimedNodeRunID = cands[0].NodeRunID
		ok, err := q.ClaimDispatchRow(ctx, cands[0].NodeRunID, supID, tx)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow: not claimed")
		}
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		if err := store.ClaimHandles().UpdateAddress(ctx, claimHandleID, supID,
			json.RawMessage(`{"addr":"x"}`), tx); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("expected rollback err, got %v", err)
	}

	var got *persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, claimHandleID, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get claim-handle: %v", err)
	}
	if got != nil {
		t.Fatalf("rollback failed: claim-handle %s present", claimHandleID)
	}
	ownership, err := q.GetClaimedBy(ctx, claimedNodeRunID)
	if err != nil {
		t.Fatalf("GetClaimedBy: %v", err)
	}
	if ownership.Kind != persistence.ClaimOwnershipKindUnclaimed {
		t.Fatalf("rollback failed: dispatch row %s has ownership %+v, want unclaimed", claimedNodeRunID, ownership)
	}

	addressBytes := json.RawMessage(`{"addr":"committed"}`)
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  10,
		}, tx)
		if err != nil {
			return err
		}
		if len(cands) != 1 {
			t.Fatalf("expected 1 candidate after rollback released the claim, got %d", len(cands))
		}
		ok, err := q.ClaimDispatchRow(ctx, cands[0].NodeRunID, supID, tx)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow #2: not claimed")
		}
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		if err := store.ClaimHandles().UpdateAddress(ctx, claimHandleID, supID, addressBytes, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	var got2 *persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, claimHandleID, tx)
		got2 = r
		return err
	}); err != nil {
		t.Fatalf("Get claim-handle #2: %v", err)
	}
	if got2 == nil {
		t.Fatalf("commit failed: claim-handle %s absent", claimHandleID)
	}
	if !jsonEqual(got2.Address, addressBytes) {
		t.Fatalf("address mismatch: got %q want %q", string(got2.Address), string(addressBytes))
	}
}
