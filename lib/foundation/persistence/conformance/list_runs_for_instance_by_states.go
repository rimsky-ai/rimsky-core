// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: node-run

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testListRunsForInstanceByStates(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	list := func(name string, instanceID shared.UUID, states []cascade.NodeState) []persistence.NodeRunLatest {
		t.Helper()
		var got []persistence.NodeRunLatest
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			m, err := store.Nodes().ListRunsForInstanceByStates(ctx, tx, instanceID, states)
			got = m
			return err
		}); err != nil {
			t.Fatalf("%s: ListRunsForInstanceByStates: %v", name, err)
		}
		return got
	}

	rows := list("stale-state", fix.InstanceID, []cascade.NodeState{cascade.NodeStateStale})
	if len(rows) != 1 || rows[0].NodeRunID != runID {
		t.Errorf("stale-state: rows=%+v want one row for runID=%s", rows, runID)
	}

	if rows := list("running-only", fix.InstanceID, []cascade.NodeState{cascade.NodeStateRunning}); len(rows) != 0 {
		t.Errorf("running-only: rows=%+v want empty (seeded run is stale)", rows)
	}

	if rows := list("multi-state", fix.InstanceID, []cascade.NodeState{
		cascade.NodeStateRunning, cascade.NodeStateStale, cascade.NodeStateHeld, cascade.NodeStateParked,
	}); len(rows) != 1 || rows[0].NodeRunID != runID {
		t.Errorf("multi-state: rows=%+v want one row for runID=%s", rows, runID)
	}

	otherInstance := shared.UUID(uuid.New())
	if rows := list("other-instance", otherInstance, []cascade.NodeState{cascade.NodeStateStale}); len(rows) != 0 {
		t.Errorf("other-instance: rows=%+v want empty", rows)
	}

	if rows := list("empty-states", fix.InstanceID, nil); len(rows) != 0 {
		t.Errorf("empty-states: rows=%+v want empty", rows)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return forceRunStateToFresh(ctx, tx, store, runID)
	}); err != nil {
		t.Fatalf("settle to fresh: %v", err)
	}
	if rows := list("after-settle", fix.InstanceID, []cascade.NodeState{
		cascade.NodeStateRunning, cascade.NodeStateStale, cascade.NodeStateHeld, cascade.NodeStateParked,
	}); len(rows) != 0 {
		t.Errorf("after-settle: rows=%+v want empty (settled rows must not surface)", rows)
	}
}
