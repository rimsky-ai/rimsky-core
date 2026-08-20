// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: node
func seedNodeRunInState(
	ctx context.Context, t *testing.T, d persistence.Database,
	instanceID, nodeID, frameID shared.UUID, state cascade.NodeState,
) shared.UUID {
	t.Helper()
	store := d.Tables()

	runScopeID := shared.UUID(uuid.New())
	runID := shared.UUID(uuid.New())

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: runScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if state == cascade.NodeStatePending {
			created, err := store.Nodes().CreateCascadePending(ctx, nodeID, runScopeID, frameID, tx)
			if err != nil {
				return err
			}
			runID = created
			return nil
		}
		return store.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: runID, NodeID: nodeID, FrameID: frameID, RunScopeID: runScopeID, ExecutorName: "test-executor",
		}, tx)
	}); err != nil {
		t.Fatalf("seedNodeRunInState(%s): %v", state, err)
	}
	if state != cascade.NodeStatePending {
		driveRunToState(ctx, t, d, runID, state)
	}
	return runID
}

func testNodeRunSummaryBucketMapping(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	for _, state := range []cascade.NodeState{
		cascade.NodeStateRunning,
		cascade.NodeStateHeld,
		cascade.NodeStateParked,
		cascade.NodeStatePending,
		cascade.NodeStateStale,
		cascade.NodeStateFresh,
		cascade.NodeStateFailed,
	} {
		seedNodeRunInState(ctx, t, d, fix.InstanceID, fix.NodeID, fix.FrameID, state)
	}

	var summary persistence.NodeRunSummary
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		summary, err = d.Tables().Nodes().GetRunSummary(ctx, fix.NodeID, tx)
		return err
	}); err != nil {
		t.Fatalf("GetRunSummary: %v", err)
	}

	if summary.ActiveCount != 3 {
		t.Fatalf("running+held+parked must bucket to active_count=3, got %+v", summary)
	}
	if summary.PendingCount != 2 {
		t.Fatalf("pending+stale must bucket to pending_count=2, got %+v", summary)
	}
	if summary.FreshCount != 1 {
		t.Fatalf("fresh must bucket to fresh_count=1, got %+v", summary)
	}
	if summary.FailedCount != 1 {
		t.Fatalf("failed must bucket to failed_count=1, got %+v", summary)
	}
	if summary.ActiveCount+summary.PendingCount+summary.FreshCount+summary.FailedCount != 7 {
		t.Fatalf("all 7 seeded runs must be accounted for across exactly 4 buckets, got %+v", summary)
	}
}
