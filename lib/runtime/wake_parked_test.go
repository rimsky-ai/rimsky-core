// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestSweepParkedNodes_WakesCorrectScopeDespiteNewerRunInAnotherScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	args, acq, tables := seedRunningNodeForParkFixture(t)

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalPark(ctx, args, acq, terminalEvent{
			Kind:         terminalKindPark,
			ParkResumeAt: time.Now().Add(-time.Minute),
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalPark: %v", err)
	}

	otherScopeID := shared.UUID(uuid.New())
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         otherScopeID,
			GraphName:  "sub",
			InstanceID: acq.InstanceID,
		}, tx); err != nil {
			return err
		}
		return args.Queue.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 acq.NodeID,
			ExecutorName:           acq.Executor,
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now(),
			FrameID:                acq.FrameID,
			RunScopeID:             otherScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed competing in-flight run in another scope: %v", err)
	}

	if err := WakeParkedNode(ctx, WakeParkedArgs{
		Persist:      tables,
		Queue:        args.Queue,
		Logger:       shared.SilentLogger{},
		TargetNodeID: acq.NodeID,
		SupervisorID: args.SupervisorID,
	}, WakeDeadlineElapsed); err != nil {
		t.Fatalf("WakeParkedNode (no NodeRunID hint): %v", err)
	}
	var stillParked *persistence.NodeRunTreeRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.NodeRunTree().GetByID(ctx, acq.NodeRunID, tx)
		stillParked = r
		return err
	}); err != nil {
		t.Fatalf("NodeRunTree.GetByID: %v", err)
	}
	if stillParked == nil {
		t.Fatalf("parked run row missing after unhinted wake attempt")
	}
	if stillParked.State != cascade.NodeStateParked {
		t.Fatalf("unhinted WakeParkedNode (deriving scope via GetLatestRunForNode) state = %q, want still %q "+
			"(this demonstrates the trap the NodeRunID hint closes: without it, the newer in-flight run "+
			"in another scope wins the scope lookup and the parked run is silently missed)",
			stillParked.State, cascade.NodeStateParked)
	}

	if err := SweepParkedNodes(ctx, ParkedSweepArgs{
		Persist:      tables,
		Queue:        args.Queue,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		SupervisorID: args.SupervisorID,
	}); err != nil {
		t.Fatalf("SweepParkedNodes: %v", err)
	}

	var row *persistence.NodeRunTreeRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.NodeRunTree().GetByID(ctx, acq.NodeRunID, tx)
		row = r
		return err
	}); err != nil {
		t.Fatalf("NodeRunTree.GetByID: %v", err)
	}
	if row == nil {
		t.Fatalf("parked run row missing after sweep")
	}
	if row.State != cascade.NodeStateStale {
		t.Fatalf("parked run state = %q, want %q "+
			"(the deadline sweep must resume the parked run's own scope even when a newer "+
			"in-flight run exists for the same node_id in a different scope)",
			row.State, cascade.NodeStateStale)
	}
}
