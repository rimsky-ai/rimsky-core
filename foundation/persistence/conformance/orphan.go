// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// orphan.go — OrphanCutoffTime conformance area.
//
// Inv 6: orphan-claim cutoff (5× heartbeat). Here we validate that
// LockHoldersStore.ListExpired returns rows with expires_at < now() and
// not those with future expires_at.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

func testOrphanCutoffTime(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Store()
	supID := "orphan-supervisor"
	lockNamePast := "orphan-past"
	lockNameFuture := "orphan-future"

	pastID := uuid.New()
	futureID := uuid.New()

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		// Past expires_at.
		if err := store.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{
			ID:                 pastID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockNamePast,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(-1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		// Future expires_at.
		if err := store.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{
			ID:                 futureID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockNameFuture,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var expired []persistence.LockHolderRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.LockHolders().ListExpired(ctx, tx)
		expired = rows
		return err
	}); err != nil {
		t.Fatalf("ListExpired: %v", err)
	}
	foundPast := false
	for _, r := range expired {
		if r.ID == pastID {
			foundPast = true
		}
		if r.ID == futureID {
			t.Fatalf("future-expiring row should not be in ListExpired")
		}
	}
	if !foundPast {
		t.Fatalf("past-expiring row not returned by ListExpired")
	}
}
