// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: node

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testGetRunSummaryForNodes(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	var idleNodeID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		idleNodeID, err = seedSecondNode(ctx, store, fix.InstanceID, tx)
		return err
	}); err != nil {
		t.Fatalf("seed idle node: %v", err)
	}

	var got map[shared.UUID]persistence.NodeRunSummary
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		m, err := store.Nodes().GetRunSummaryForNodes(ctx, []shared.UUID{fix.NodeID, idleNodeID}, tx)
		got = m
		return err
	}); err != nil {
		t.Fatalf("GetRunSummaryForNodes: %v", err)
	}

	var single persistence.NodeRunSummary
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		s, err := store.Nodes().GetRunSummary(ctx, fix.NodeID, tx)
		single = s
		return err
	}); err != nil {
		t.Fatalf("GetRunSummary: %v", err)
	}

	if got[fix.NodeID] != single {
		t.Errorf("GetRunSummaryForNodes[fix.NodeID]=%+v want %+v (must match the per-node GetRunSummary result for run %s)", got[fix.NodeID], single, runID)
	}
	if s, ok := got[idleNodeID]; ok && s != (persistence.NodeRunSummary{}) {
		t.Errorf("GetRunSummaryForNodes[idleNodeID]=%+v want zero-value or absent for a node with no runs", s)
	}

	if empty, err := func() (map[shared.UUID]persistence.NodeRunSummary, error) {
		var m map[shared.UUID]persistence.NodeRunSummary
		err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			m, err = store.Nodes().GetRunSummaryForNodes(ctx, nil, tx)
			return err
		})
		return m, err
	}(); err != nil || len(empty) != 0 {
		t.Errorf("GetRunSummaryForNodes(nil) = %+v, %v; want empty map, nil error", empty, err)
	}
}

func testGetLatestRunForNodes(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	var idleNodeID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		idleNodeID, err = seedSecondNode(ctx, store, fix.InstanceID, tx)
		return err
	}); err != nil {
		t.Fatalf("seed idle node: %v", err)
	}

	var got map[shared.UUID]persistence.NodeRunLatest
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		m, err := store.Nodes().GetLatestRunForNodes(ctx, []shared.UUID{fix.NodeID, idleNodeID}, tx)
		got = m
		return err
	}); err != nil {
		t.Fatalf("GetLatestRunForNodes: %v", err)
	}

	latest, ok := got[fix.NodeID]
	if !ok || latest.NodeRunID != runID {
		t.Errorf("GetLatestRunForNodes[fix.NodeID]=%+v (ok=%v) want NodeRunID=%s", latest, ok, runID)
	}
	if _, ok := got[idleNodeID]; ok {
		t.Errorf("GetLatestRunForNodes[idleNodeID] present=%+v want absent for a node with no runs", got[idleNodeID])
	}

	if empty, err := func() (map[shared.UUID]persistence.NodeRunLatest, error) {
		var m map[shared.UUID]persistence.NodeRunLatest
		err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			m, err = store.Nodes().GetLatestRunForNodes(ctx, nil, tx)
			return err
		})
		return m, err
	}(); err != nil || len(empty) != 0 {
		t.Errorf("GetLatestRunForNodes(nil) = %+v, %v; want empty map, nil error", empty, err)
	}
}

func seedSecondNode(ctx context.Context, store persistence.Tables, instanceID shared.UUID, tx persistence.Tx) (shared.UUID, error) {
	id := shared.UUID(uuid.New())
	_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID:         id,
		InstanceID: instanceID,
		NodeType:   "fixture-node-type-2",
		Executor:   "test-executor",
	}, tx)
	return id, err
}
