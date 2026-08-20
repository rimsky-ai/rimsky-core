// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @concept: transition-reason
func testPromotionAndReleaseRouteThroughTheSwitch(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	supID := "supervisor-routed"

	runID := seedClaimedGuardRun(ctx, t, d, fix, supID)
	if got := snapshotRun(ctx, t, d, runID).State; got != cascade.NodeStateRunning {
		t.Fatalf("promotion of a claimed stale run must persist the state the switch returns; got %s want %s",
			got, cascade.NodeStateRunning)
	}

	if err := q.ReleaseClaim(ctx, runID, supID); err != nil {
		t.Fatalf("release of a running claimed run must succeed: %v", err)
	}
	if got := snapshotRun(ctx, t, d, runID).State; got != cascade.NodeStateStale {
		t.Fatalf("release must persist the state the switch returns; got %s want %s",
			got, cascade.NodeStateStale)
	}
	owner, err := q.GetClaimedBy(ctx, runID)
	if err != nil {
		t.Fatalf("GetClaimedBy after release: %v", err)
	}
	if owner.Kind != persistence.ClaimOwnershipKindUnclaimed {
		t.Fatalf("release must clear the claimant; got %s/%s", owner.Kind, owner.SupervisorID)
	}
}

// @concept: transition-reason
func testStateWriterRefusesAPairTheSwitchRejects(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	settledSig := "terminal/success"
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		return d.Tables().Nodes().UpdateState(ctx, runID,
			cascade.NodeStateFresh, cascade.ReasonAcquirePass, &settledSig, tx)
	}); err != nil {
		t.Fatalf("settle the run: %v", err)
	}

	err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		return d.Tables().NodeRunTree().UpdateAggregateState(ctx, runID,
			cascade.ReasonSubGraphInternalCascadeFired, nil, false, tx)
	})
	if !errors.Is(err, cascade.ErrIllegalTransition) {
		t.Fatalf("a reason the switch models only from running must be refused from a settled run; got %v", err)
	}
	after := snapshotRun(ctx, t, d, runID)
	if after.State != cascade.NodeStateFresh || after.SettlingSignalType != settledSig {
		t.Fatalf("a refused write must write nothing; got %s/%q", after.State, after.SettlingSignalType)
	}
}

// @concept: transition-reason
func testAggregateWritePersistsTheStateTheSwitchReturns(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	aggregateSig := "terminal/error/aggregate/strict_failed"
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		return d.Tables().NodeRunTree().UpdateAggregateState(ctx, runID,
			cascade.ReasonAggregateSettledFailure, &aggregateSig, true, tx)
	}); err != nil {
		t.Fatalf("aggregate write onto an in-flight parent: %v", err)
	}
	got := snapshotRun(ctx, t, d, runID)
	if got.State != cascade.NodeStateFailed {
		t.Fatalf("the aggregate write must persist the state the switch returns; got %s want %s",
			got.State, cascade.NodeStateFailed)
	}
	if got.SettlingSignalType != aggregateSig {
		t.Fatalf("the aggregate write must persist its settling signal; got %q want %q",
			got.SettlingSignalType, aggregateSig)
	}

	reprojectedSig := "terminal/success"
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		return d.Tables().NodeRunTree().UpdateAggregateState(ctx, runID,
			cascade.ReasonAggregateSettledSuccess, &reprojectedSig, false, tx)
	}); err != nil {
		t.Fatalf("a late child transition must re-project a settled parent: %v", err)
	}
	got = snapshotRun(ctx, t, d, runID)
	if got.State != cascade.NodeStateFresh || got.SettlingSignalType != reprojectedSig {
		t.Fatalf("the re-projection must persist the switch's target and its signal; got %s/%q",
			got.State, got.SettlingSignalType)
	}
}
