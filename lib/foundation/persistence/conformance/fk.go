// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testForeignKeyCascade(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	store := d.Tables()

	lockHolderID := uuid.New()
	claimHolderID := uuid.New()
	supID := "fk-supervisor"
	expires := time.Now().Add(1 * time.Hour)
	lockName := "fk-test-lock"
	address := json.RawMessage(`{"k":"fk-conformance"}`)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			Address:            address,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          expires,
		}, tx); err != nil {
			return err
		}
		if err := store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            claimHolderID,
			ClaimHandleID: lockHolderID,
			HolderRunID:   runID,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed lock+claim: %v", err)
	}

	var got *persistence.ClaimHandleRow
	var gotClaims []persistence.ClaimHolderRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, lockHolderID, tx)
		got = r
		if err != nil {
			return err
		}
		c, err := store.ClaimHolders().ListByClaimHandleID(ctx, lockHolderID, tx)
		gotClaims = c
		return err
	}); err != nil || got == nil {
		t.Fatalf("lock-holder Get / claim-holder list: row=%v err=%v", got, err)
	}
	if len(gotClaims) != 1 {
		t.Fatalf("expected 1 claim-holder; got %d", len(gotClaims))
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Delete(ctx, lockHolderID, supID, tx)
	}); err != nil {
		t.Fatalf("Delete lock-holder: %v", err)
	}

	var gotClaims2 []persistence.ClaimHolderRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		c, err := store.ClaimHolders().ListByClaimHandleID(ctx, lockHolderID, tx)
		gotClaims2 = c
		return err
	}); err != nil {
		t.Fatalf("claim-holder list post-delete: %v", err)
	}
	if len(gotClaims2) != 0 {
		t.Fatalf("FK cascade failed: claim-holder rows still present (%d)", len(gotClaims2))
	}
}
