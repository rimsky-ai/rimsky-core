// scope.go — ScopeByteEquality conformance area.
//
// Inv 14: scope conflict is byte-equal.
//
// Per spec §7.7 the store canonicalises scope bytes before handing them
// to rimsky, so two scopes that should conflict produce byte-equal
// scope_data on the wire. For round-trip equality across drivers the
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

func testScopeByteEquality(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Store()

	storeName := "scope-conformance-store"
	intent := "rw"
	scopeA := json.RawMessage(`{"path":"/data/a"}`)
	scopeB := json.RawMessage(`{"path":"/data/b"}`)
	supID := "scope-supervisor"

	insert := func(t *testing.T, scope json.RawMessage) {
		t.Helper()
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return store.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{
				ID:                 uuid.New(),
				LockKind:           persistence.LockKindScope,
				StoreName:          &storeName,
				ScopeData:          scope,
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
	// rimsky_lock_holders table doesn't unique-constrain scope; the
	// supervisor's in-go conflict predicate is what catches the conflict).
	// We verify that ListByStoreScope returns both, and that the rows'
	// scope_data bytes round-trip equal.
	insert(t, scopeA)
	insert(t, scopeA)

	var rows []persistence.LockHolderRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = store.LockHolders().ListByStoreScope(ctx, storeName, tx)
		return err
	}); err != nil {
		t.Fatalf("ListByStoreScope: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 scope rows, got %d", len(rows))
	}
	// Both stored rows must be byte-equal to each other (the store
	// canonicalisation guarantee), and semantically equal to scopeA.
	if string(rows[0].ScopeData) != string(rows[1].ScopeData) {
		t.Fatalf("byte-equal scopes did not round-trip equal:\n  %q\n  %q",
			string(rows[0].ScopeData), string(rows[1].ScopeData))
	}
	if !jsonEqual(rows[0].ScopeData, scopeA) {
		t.Fatalf("stored scope not semantically equal to input:\n  stored=%q input=%q",
			string(rows[0].ScopeData), string(scopeA))
	}

	// Insert a byte-different scope; rows for the store now: 3, with the
	// new one not byte-equal to either of the others.
	insert(t, scopeB)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = store.LockHolders().ListByStoreScope(ctx, storeName, tx)
		return err
	}); err != nil {
		t.Fatalf("ListByStoreScope #2: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 scope rows, got %d", len(rows))
	}
	matchA := 0
	matchB := 0
	for _, r := range rows {
		switch {
		case jsonEqual(r.ScopeData, scopeA):
			matchA++
		case jsonEqual(r.ScopeData, scopeB):
			matchB++
		default:
			t.Fatalf("unexpected scope bytes: %q", string(r.ScopeData))
		}
	}
	if matchA != 2 || matchB != 1 {
		t.Fatalf("scope byte-tally wrong: A=%d B=%d (want 2,1)", matchA, matchB)
	}
}
