// lock_holders_update_region.go — LockHoldersUpdateRegion conformance area.
//
// Covers LockHoldersStore.UpdateRegion: writes the new region_data inside
// a tx, then verifies (a) the new bytes round-trip via Get, and (b) the
// claimant guard turns a mismatched supervisorID into a no-op.
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/persistence"
)

func testLockHoldersUpdateRegion(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Store()

	lockHolderID := uuid.New()
	supID := "update-region-supervisor"
	storeName := "update-region-store"
	intent := "rw"
	regionA := json.RawMessage(`{"path":"/data/initial"}`)
	regionB := json.RawMessage(`{"path":"/data/updated"}`)

	// Insert the initial region row.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         regionA,
			Intent:             &intent,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx)
	}); err != nil {
		t.Fatalf("insert region row: %v", err)
	}

	// ---- UpdateRegion: matching supervisor writes the new bytes ----
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.LockHolders().UpdateRegion(ctx, lockHolderID, supID, regionB, tx)
	}); err != nil {
		t.Fatalf("UpdateRegion: %v", err)
	}
	got, err := store.LockHolders().Get(ctx, lockHolderID, nil)
	if err != nil {
		t.Fatalf("Get after UpdateRegion: %v", err)
	}
	if got == nil {
		t.Fatalf("Get after UpdateRegion: row missing")
	}
	if !jsonEqual(got.RegionData, regionB) {
		t.Fatalf("UpdateRegion: region_data not updated (got=%q want=%q)",
			string(got.RegionData), string(regionB))
	}

	// ---- UpdateRegion: claimant-guard mismatch is a no-op ----
	otherSup := "different-supervisor"
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.LockHolders().UpdateRegion(ctx, lockHolderID, otherSup, regionA, tx)
	}); err != nil {
		t.Fatalf("UpdateRegion (wrong sup): %v", err)
	}
	got, err = store.LockHolders().Get(ctx, lockHolderID, nil)
	if err != nil {
		t.Fatalf("Get after wrong-sup UpdateRegion: %v", err)
	}
	if got == nil {
		t.Fatalf("Get after wrong-sup UpdateRegion: row missing")
	}
	if !jsonEqual(got.RegionData, regionB) {
		t.Fatalf("UpdateRegion claimant-guard violated: bytes changed under mismatched supervisor (got=%q want unchanged %q)",
			string(got.RegionData), string(regionB))
	}
}
