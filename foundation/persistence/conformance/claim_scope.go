// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claim_scope.go — ClaimScopeByteEquality conformance area.
//
// Inv 14: claim-scope conflict is byte-equal.
//
// Per spec §7.7 the store canonicalises claim-scope bytes before handing
// them to rimsky, so two claim-scopes that should conflict produce byte-equal
// claim_scope_data on the wire. For round-trip equality across drivers the
// test compares semantic JSON equality (decode-and-compare), since
// Postgres JSONB normalises whitespace at storage time while SQLite TEXT
// preserves the exact bytes — both behaviours satisfy "the bytes come
// back in a stable canonical form" per the store contract.
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// jsonEqual returns true when a and b are JSON values with the same
// semantic content (whitespace and key-order insensitive).
func jsonEqual(a, b []byte) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return jsonValueEqual(av, bv)
}

func jsonValueEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, vA := range av {
			vB, ok := bv[k]
			if !ok || !jsonValueEqual(vA, vB) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonValueEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func testClaimScopeByteEquality(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	producerName := "scope-conformance-store"
	intent := "rw"
	scopeA := json.RawMessage(`{"path":"/data/a"}`)
	scopeB := json.RawMessage(`{"path":"/data/b"}`)
	supID := "scope-supervisor"

	insert := func(t *testing.T, scope json.RawMessage) {
		t.Helper()
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID:                 uuid.New(),
				LockKind:           persistence.LockKindScope,
				ProducerName:       &producerName,
				ClaimScopeData:     scope,
				Intent:             &intent,
				HolderSupervisorID: supID,
				HolderNodeID:       fix.NodeID,
				ExpiresAt:          time.Now().Add(1 * time.Hour),
			}, tx)
		}); err != nil {
			t.Fatalf("insert scope row: %v", err)
		}
	}

	// Two byte-equal scopes: both rows land successfully (the
	// rimsky_claim_handles table doesn't unique-constrain scope; the
	// supervisor's in-go conflict predicate is what catches the conflict).
	// We verify that ListByProducerClaimScope returns both, and that the rows'
	// scope_data bytes round-trip equal.
	insert(t, scopeA)
	insert(t, scopeA)

	var rows []persistence.ClaimHandleRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = store.ClaimHandles().ListByProducerClaimScope(ctx, producerName, tx)
		return err
	}); err != nil {
		t.Fatalf("ListByProducerClaimScope: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 scope rows, got %d", len(rows))
	}
	// Both stored rows must be byte-equal to each other (the store
	// canonicalisation guarantee), and semantically equal to scopeA.
	if string(rows[0].ClaimScopeData) != string(rows[1].ClaimScopeData) {
		t.Fatalf("byte-equal claim-scopes did not round-trip equal:\n  %q\n  %q",
			string(rows[0].ClaimScopeData), string(rows[1].ClaimScopeData))
	}
	if !jsonEqual(rows[0].ClaimScopeData, scopeA) {
		t.Fatalf("stored claim-scope not semantically equal to input:\n  stored=%q input=%q",
			string(rows[0].ClaimScopeData), string(scopeA))
	}

	// Insert a byte-different scope; rows for the store now: 3, with the
	// new one not byte-equal to either of the others.
	insert(t, scopeB)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = store.ClaimHandles().ListByProducerClaimScope(ctx, producerName, tx)
		return err
	}); err != nil {
		t.Fatalf("ListByProducerClaimScope #2: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 scope rows, got %d", len(rows))
	}
	matchA := 0
	matchB := 0
	for _, r := range rows {
		switch {
		case jsonEqual(r.ClaimScopeData, scopeA):
			matchA++
		case jsonEqual(r.ClaimScopeData, scopeB):
			matchB++
		default:
			t.Fatalf("unexpected scope bytes: %q", string(r.ClaimScopeData))
		}
	}
	if matchA != 2 || matchB != 1 {
		t.Fatalf("scope byte-tally wrong: A=%d B=%d (want 2,1)", matchA, matchB)
	}
}
