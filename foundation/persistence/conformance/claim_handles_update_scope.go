// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claim_handles_update_scope.go — ClaimHandlesUpdateScope conformance area.
//
// Covers ClaimHandleTable.UpdateScope: writes the new scope_data inside
// a tx, then verifies (a) the new bytes round-trip via Get, and (b) the
// claimant guard turns a mismatched supervisorID into a no-op.
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

func testClaimHandlesUpdateScope(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	lockHolderID := uuid.New()
	supID := "update-scope-supervisor"
	producerName := "update-scope-store"
	intent := "rw"
	scopeA := json.RawMessage(`{"path":"/data/initial"}`)
	scopeB := json.RawMessage(`{"path":"/data/updated"}`)

	// Insert the initial scope row.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ScopeData:          scopeA,
			Intent:             &intent,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx)
	}); err != nil {
		t.Fatalf("insert scope row: %v", err)
	}

	// ---- UpdateScope: matching supervisor writes the new bytes ----
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().UpdateScope(ctx, lockHolderID, supID, scopeB, tx)
	}); err != nil {
		t.Fatalf("UpdateScope: %v", err)
	}
	var got *persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, lockHolderID, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get after UpdateScope: %v", err)
	}
	if got == nil {
		t.Fatalf("Get after UpdateScope: row missing")
	}
	if !jsonEqual(got.ScopeData, scopeB) {
		t.Fatalf("UpdateScope: scope_data not updated (got=%q want=%q)",
			string(got.ScopeData), string(scopeB))
	}

	// ---- UpdateScope: claimant-guard mismatch is a no-op ----
	otherSup := "different-supervisor"
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().UpdateScope(ctx, lockHolderID, otherSup, scopeA, tx)
	}); err != nil {
		t.Fatalf("UpdateScope (wrong sup): %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, lockHolderID, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get after wrong-sup UpdateScope: %v", err)
	}
	if got == nil {
		t.Fatalf("Get after wrong-sup UpdateScope: row missing")
	}
	if !jsonEqual(got.ScopeData, scopeB) {
		t.Fatalf("UpdateScope claimant-guard violated: bytes changed under mismatched supervisor (got=%q want unchanged %q)",
			string(got.ScopeData), string(scopeB))
	}
}
