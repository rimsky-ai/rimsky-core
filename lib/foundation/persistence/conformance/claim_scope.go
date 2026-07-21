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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

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
	if string(rows[0].ClaimScopeData) != string(rows[1].ClaimScopeData) {
		t.Fatalf("byte-equal claim-scopes did not round-trip equal:\n  %q\n  %q",
			string(rows[0].ClaimScopeData), string(rows[1].ClaimScopeData))
	}
	if !jsonEqual(rows[0].ClaimScopeData, scopeA) {
		t.Fatalf("stored claim-scope not semantically equal to input:\n  stored=%q input=%q",
			string(rows[0].ClaimScopeData), string(scopeA))
	}

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

func testClaimScopeCommittedDurableStillConflicts(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	scopeBytes := []byte(`"shared-scope"`)
	producer := "p-x"
	intent := "rw"

	idA := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 idA,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     scopeBytes,
			Address:            []byte(`"addr-A"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A",
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			Lifetime:           spec.ClaimLifetimeDurable,
		}, tx)
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, idA, "sup-A", spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	var rowA *persistence.ClaimHandleRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.ClaimHandles().Get(ctx, idA, tx)
		rowA = r
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rowA == nil {
		t.Fatalf("committed row missing")
	}
	if rowA.State != spec.ClaimHandleStateCommitted {
		t.Fatalf("state = %v, want %v", rowA.State, spec.ClaimHandleStateCommitted)
	}
	if rowA.Lifetime != spec.ClaimLifetimeDurable {
		t.Fatalf("lifetime = %v, want %v", rowA.Lifetime, spec.ClaimLifetimeDurable)
	}
	if rowA.HolderSupervisorID != nil && *rowA.HolderSupervisorID != "" {
		t.Fatalf("committed row must have holder_supervisor_id NULL, got %q", *rowA.HolderSupervisorID)
	}

	var hits []persistence.ClaimHandleRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := store.ClaimHandles().ListByProducerClaimScope(ctx, producer, tx)
		hits = rows
		return err
	}); err != nil {
		t.Fatalf("ListByProducerClaimScope: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("ListByProducerClaimScope must surface the committed-durable row for conflict detection, got %d rows", len(hits))
	}
	if hits[0].ID != idA {
		t.Fatalf("hits[0].ID = %v, want %v", hits[0].ID, idA)
	}
	if hits[0].State != spec.ClaimHandleStateCommitted {
		t.Fatalf("hits[0].State = %v, want %v", hits[0].State, spec.ClaimHandleStateCommitted)
	}
	if hits[0].Lifetime != spec.ClaimLifetimeDurable {
		t.Fatalf("hits[0].Lifetime = %v, want %v", hits[0].Lifetime, spec.ClaimLifetimeDurable)
	}
	if string(hits[0].ClaimScopeData) != string(scopeBytes) {
		t.Fatalf("surfaced row must carry the byte-equal claim-scope: got %q want %q",
			string(hits[0].ClaimScopeData), string(scopeBytes))
	}
}

func testClaimScopeCommittedSubgraphDoesNotConflict(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	scopeBytes := []byte(`"shared-scope-sg"`)
	producer := "p-sg"
	intent := "rw"

	idA := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 idA,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     scopeBytes,
			Address:            []byte(`"addr-A"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A",
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			Lifetime:           spec.ClaimLifetimeSubgraph,
		}, tx); err != nil {
			return err
		}
		return store.ClaimHandles().Promote(ctx, idA, "sup-A", spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("Insert+Promote: %v", err)
	}

	var hits []persistence.ClaimHandleRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := store.ClaimHandles().ListByProducerClaimScope(ctx, producer, tx)
		hits = rows
		return err
	}); err != nil {
		t.Fatalf("ListByProducerClaimScope: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("committed-subgraph row must NOT participate in scope-conflict detection (producer Released the scope at Commit), got %d rows", len(hits))
	}
}
