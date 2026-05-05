// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// fk.go — ForeignKeyCascade conformance area.
//
// Inv 13 (auto-terminal cleanup); also exercises _foreign_keys=ON under
// SQLite (without it the cascade silently fails).
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

func testForeignKeyCascade(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Store()

	lockHolderID := uuid.New()
	claimHolderID := uuid.New()
	supID := "fk-supervisor"
	expires := time.Now().Add(1 * time.Hour)
	lockName := "fk-test-lock"
	// The address payload is opaque bytes returned by Store.Open; for
	// the FK conformance test we just want a non-zero value to round-
	// trip through the cascade-delete path. Pinning a real
	// json.RawMessage keeps the column type honest without violating
	// the named-vs-scope check constraint (scope_data must remain
	// NULL on a named-kind row).
	address := json.RawMessage(`{"k":"fk-conformance"}`)

	// Insert lock_holder + claim_holder inside a tx (Insert requires tx).
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{
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
			ID:           claimHolderID,
			LockHolderID: lockHolderID,
			HolderNodeID: fix.NodeID,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed lock+claim: %v", err)
	}

	// Verify both rows present.
	got, err := store.LockHolders().Get(ctx, lockHolderID, nil)
	if err != nil || got == nil {
		t.Fatalf("lock-holder Get: row=%v err=%v", got, err)
	}
	gotClaims, err := store.ClaimHolders().ListByLockHolderID(ctx, lockHolderID, nil)
	if err != nil {
		t.Fatalf("claim-holder list: %v", err)
	}
	if len(gotClaims) != 1 {
		t.Fatalf("expected 1 claim-holder; got %d", len(gotClaims))
	}

	// Delete the lock-holder row inside a tx.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.LockHolders().Delete(ctx, lockHolderID, supID, tx)
	}); err != nil {
		t.Fatalf("Delete lock-holder: %v", err)
	}

	// Cascade should have removed the claim-holder.
	gotClaims2, err := store.ClaimHolders().ListByLockHolderID(ctx, lockHolderID, nil)
	if err != nil {
		t.Fatalf("claim-holder list post-delete: %v", err)
	}
	if len(gotClaims2) != 0 {
		t.Fatalf("FK cascade failed: claim-holder rows still present (%d)", len(gotClaims2))
	}
}
