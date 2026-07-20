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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testOrphanCutoffTime(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	supID := "orphan-supervisor"
	lockNamePast := "orphan-past"
	lockNameFuture := "orphan-future"

	pastID := uuid.New()
	futureID := uuid.New()
	resolvedPastID := uuid.New()
	lockNameResolvedPast := "orphan-resolved-past"

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 pastID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockNamePast,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(-1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 futureID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockNameFuture,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 resolvedPastID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockNameResolvedPast,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(-1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		return store.ClaimHandles().Promote(ctx, resolvedPastID, supID, spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var expired []persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.ClaimHandles().ListExpired(ctx, tx)
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
		if r.ID == resolvedPastID {
			t.Fatalf("resolved (committed) row with a past expiry must not be in ListExpired; " +
				"the reaper candidate predicate must filter on state='active', not expiry alone")
		}
	}
	if !foundPast {
		t.Fatalf("past-expiring row not returned by ListExpired")
	}
}
