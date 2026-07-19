// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @concept: claim-handle
// @concept: claim-lifetime
func testClaimHandleSurvivesNodeRunDeletion(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	frames := d.Tables().Frames()
	frameID := fix.FrameID

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, frameID)

	h := guardScopeHandleInput(fix, claimQuerySup, time.Now().Add(1*time.Hour))
	h.NodeRunID = &runID
	seedGuardClaimHandle(ctx, t, d, h)

	before := getGuardClaimHandle(ctx, t, d, h.ID)
	if before == nil || before.NodeRunID == nil || *before.NodeRunID != runID {
		t.Fatalf("seeded claim handle node_run_id = %v, want %s", before, runID)
	}

	frameOp(ctx, t, d, "terminate frame", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkFrameEnded(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkFrameEnded: frame did not transition")
		}
		return nil
	})

	deleted, err := frames.PruneTraceForRetention(ctx, 0, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("PruneTraceForRetention: %v", err)
	}
	if deleted == 0 {
		t.Fatalf("PruneTraceForRetention deleted 0 frames; test setup did not exercise the frame-owns-node-run cascade")
	}

	run, err := d.Queue().GetByID(ctx, runID)
	if err != nil {
		t.Fatalf("Queue.GetByID(runID): %v", err)
	}
	if run != nil {
		t.Fatalf("node_run %s survived its frame's prune; test setup invalid (cascade did not fire)", runID)
	}

	after := getGuardClaimHandle(ctx, t, d, h.ID)
	if after == nil {
		t.Fatalf("claim handle %s was deleted when its holding node_run was deleted; "+
			"node_run_id must be ON DELETE SET NULL (held handles outlive the node-run), not a cascading delete", h.ID)
	}
	if after.NodeRunID != nil {
		t.Fatalf("claim handle node_run_id = %v after its node_run was deleted, want nil (ON DELETE SET NULL)", *after.NodeRunID)
	}
}
