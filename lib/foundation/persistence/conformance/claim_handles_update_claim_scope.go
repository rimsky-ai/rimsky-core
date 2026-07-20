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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testClaimHandlesUpdateClaimScope(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	claimHandleID := uuid.New()
	supID := "update-scope-supervisor"
	producerName := "update-scope-store"
	intent := "rw"
	scopeA := json.RawMessage(`{"path":"/data/initial"}`)
	scopeB := json.RawMessage(`{"path":"/data/updated"}`)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
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
		return store.ClaimHandles().UpdateClaimScope(ctx, claimHandleID, supID, scopeB, tx)
	}); err != nil {
		t.Fatalf("UpdateClaimScope: %v", err)
	}
	var got *persistence.ClaimHandleRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, claimHandleID, tx)
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

	otherSup := "different-supervisor"
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().UpdateClaimScope(ctx, claimHandleID, otherSup, scopeA, tx)
	})
	if !errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
		t.Fatalf("UpdateClaimScope (wrong sup): got err %v, want ErrIllegalClaimHandleTransition", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, claimHandleID, tx)
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
