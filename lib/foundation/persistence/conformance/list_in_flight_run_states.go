// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: wait-set
// @concept: cascade

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testListInFlightRunStates(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	list := func(name string, nodeIDs []shared.UUID, frameID, scopeID shared.UUID) map[shared.UUID][]string {
		t.Helper()
		var got map[shared.UUID][]string
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			m, err := q.ListInFlightRunStates(ctx, tx, nodeIDs, frameID, scopeID)
			got = m
			return err
		}); err != nil {
			t.Fatalf("%s: ListInFlightRunStates: %v", name, err)
		}
		return got
	}

	m := list("match", []shared.UUID{fix.NodeID}, fix.FrameID, fix.MainRunScopeID)
	states, ok := m[fix.NodeID]
	if !ok || len(states) != 1 || states[0] != "stale" {
		t.Errorf("match: states for node = %v (present=%v), want [stale]", states, ok)
	}

	otherNode := shared.UUID(uuid.New())
	otherFrame := shared.UUID(uuid.New())
	otherScope := shared.UUID(uuid.New())
	if m := list("wrong-frame", []shared.UUID{fix.NodeID}, otherFrame, fix.MainRunScopeID); len(m) != 0 {
		t.Errorf("wrong-frame: got %v, want empty", m)
	}
	if m := list("wrong-scope", []shared.UUID{fix.NodeID}, fix.FrameID, otherScope); len(m) != 0 {
		t.Errorf("wrong-scope: got %v, want empty", m)
	}
	if m := list("other-node", []shared.UUID{otherNode}, fix.FrameID, fix.MainRunScopeID); len(m) != 0 {
		t.Errorf("other-node: got %v, want empty", m)
	}

	if m := list("empty-set", nil, fix.FrameID, fix.MainRunScopeID); len(m) != 0 {
		t.Errorf("empty-set: got %v, want empty", m)
	}

	if err := q.Complete(ctx, runID, ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return forceRunStateToFresh(ctx, tx, store, runID)
	}); err != nil {
		t.Fatalf("settle to fresh: %v", err)
	}
	if m := list("after-settle", []shared.UUID{fix.NodeID}, fix.FrameID, fix.MainRunScopeID); len(m) != 0 {
		t.Errorf("after-settle: got %v, want empty (settled rows must not gate)", m)
	}
}
