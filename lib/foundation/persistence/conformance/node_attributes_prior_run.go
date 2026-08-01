// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @concept: attribute
func testNodeAttributesGetPriorRunData(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	runA := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	var priorForFirstRun map[string]any
	sawKey := false
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		p, err := store.NodeAttributes().GetPriorRunData(ctx, runA, tx)
		priorForFirstRun = p
		sawKey = true
		return err
	}); err != nil {
		t.Fatalf("GetPriorRunData (no earlier run): %v", err)
	}
	if !sawKey {
		t.Fatalf("GetPriorRunData (no earlier run): call did not execute")
	}
	if priorForFirstRun != nil {
		t.Fatalf("GetPriorRunData (no earlier run): got %#v, want nil (distinguishes "+
			"true first-run from a prior row with empty data)", priorForFirstRun)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runA, fix.NodeID, map[string]any{"k": "v1"}, tx)
	}); err != nil {
		t.Fatalf("Upsert runA: %v", err)
	}
	completeRunAdmin(ctx, t, d, runA)

	runB := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if runB == runA {
		t.Fatalf("seedConformanceRunForNode returned the same run twice (%v); "+
			"the prior-run row must be distinct and settled before the next run is seeded", runB)
	}
	var priorForSecondRun map[string]any
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		p, err := store.NodeAttributes().GetPriorRunData(ctx, runB, tx)
		priorForSecondRun = p
		return err
	}); err != nil {
		t.Fatalf("GetPriorRunData (prior run has data): %v", err)
	}
	if v, _ := priorForSecondRun["k"].(string); v != "v1" {
		t.Fatalf("GetPriorRunData (prior run has data): got %#v, want {k: v1}", priorForSecondRun)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runB, fix.NodeID, map[string]any{}, tx)
	}); err != nil {
		t.Fatalf("Upsert runB (empty data): %v", err)
	}
	completeRunAdmin(ctx, t, d, runB)

	runC := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if runC == runB {
		t.Fatalf("seedConformanceRunForNode returned the same run twice (%v); "+
			"the prior-run row must be distinct and settled before the next run is seeded", runC)
	}
	var priorForThirdRun map[string]any
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		p, err := store.NodeAttributes().GetPriorRunData(ctx, runC, tx)
		priorForThirdRun = p
		return err
	}); err != nil {
		t.Fatalf("GetPriorRunData (prior run has empty data): %v", err)
	}
	if priorForThirdRun == nil {
		t.Fatalf("GetPriorRunData (prior run has empty data): got nil, want non-nil empty " +
			"map (the prior run row exists, it just wrote no attributes)")
	}
	if len(priorForThirdRun) != 0 {
		t.Fatalf("GetPriorRunData (prior run has empty data): got %#v, want empty", priorForThirdRun)
	}
}
