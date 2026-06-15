// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: conformance area conformance area.
// conformance area.
//
// Covers ClaimHandleTable.UpdateClaimScope: writes the new claim_scope_data
// inside a tx, then verifies (a) the new bytes round-trip via Get, and (b) the
// claimant guard turns a mismatched supervisorID into a no-op.
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testClaimHandlesUpdateClaimScope(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	lockHolderID := uuid.New()
	supID := "update-scope-supervisor"
	producerName := "update-scope-store"
	intent := "rw"
	scopeA := json.RawMessage(`{"path":"/data/initial"}`)
	scopeB := json.RawMessage(`{"path":"/data/updated"}`)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     scopeA,
			Intent:             &intent,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx)
	}); err != nil {
		t.Fatalf("insert claim-scope row: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().UpdateClaimScope(ctx, lockHolderID, supID, scopeB, tx)
	}); err != nil {
		t.Fatalf("UpdateClaimScope: %v", err)
	}
	var got *persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, lockHolderID, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get after UpdateClaimScope: %v", err)
	}
	if got == nil {
		t.Fatalf("Get after UpdateClaimScope: row missing")
	}
	if !jsonEqual(got.ClaimScopeData, scopeB) {
		t.Fatalf("UpdateClaimScope: claim_scope_data not updated (got=%q want=%q)",
			string(got.ClaimScopeData), string(scopeB))
	}

	// @constraint: Inv 4 (claimant-guarded release) — UpdateClaimScope under a
	// mismatched supervisor must be a no-op (bytes unchanged).
	otherSup := "different-supervisor"
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().UpdateClaimScope(ctx, lockHolderID, otherSup, scopeA, tx)
	}); err != nil {
		t.Fatalf("UpdateClaimScope (wrong sup): %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, lockHolderID, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get after wrong-sup UpdateClaimScope: %v", err)
	}
	if got == nil {
		t.Fatalf("Get after wrong-sup UpdateClaimScope: row missing")
	}
	if !jsonEqual(got.ClaimScopeData, scopeB) {
		t.Fatalf("UpdateClaimScope claimant-guard violated: bytes changed under mismatched supervisor (got=%q want unchanged %q)",
			string(got.ClaimScopeData), string(scopeB))
	}
}
