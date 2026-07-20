// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const frameSettlementSup = "frame-settlement-supervisor"

func framePendingForInstance(
	ctx context.Context, t *testing.T, d persistence.Database, instanceID shared.UUID,
) []persistence.FramePending {
	t.Helper()
	var mine []persistence.FramePending
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		all, err := d.Tables().Frames().ListRunningFramesNoPendingNodes(ctx, tx)
		if err != nil {
			return err
		}
		for _, p := range all {
			if p.InstanceID == instanceID {
				mine = append(mine, p)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("ListRunningFramesNoPendingNodes: %v", err)
	}
	return mine
}

func completeRunAdmin(ctx context.Context, t *testing.T, d persistence.Database, runID shared.UUID) {
	t.Helper()
	store := d.Tables()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return forceRunStateToFresh(ctx, tx, store, runID)
	}); err != nil {
		t.Fatalf("completeRunAdmin: forceRunStateToFresh(%s): %v", runID, err)
	}
	if err := d.Queue().ForceComplete(ctx, runID); err != nil {
		t.Fatalf("Queue.ForceComplete(%s): %v", runID, err)
	}
}

func testFrameSettlementNoPendingNodes(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 1 || got[0].FrameID != fix.FrameID {
		t.Fatalf("empty running frame not surfaced: %+v, want [%s]", got, fix.FrameID)
	}

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 0 {
		t.Fatalf("frame surfaced drained while a stale run is pending: %+v", got)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, runID, frameSettlementSup)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("claim for park failed")
		}
		_, err = q.PromoteClaimedToRunning(ctx, tx, runID, frameSettlementSup)
		return err
	}); err != nil {
		t.Fatalf("claim tx: %v", err)
	}
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runID, ExpectedClaimedBy: frameSettlementSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
	})
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 0 {
		t.Fatalf("frame surfaced drained while a run is PARKED: %+v", got)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		resumed, err := resumeRunInTx(ctx, d, tx, runID)
		if err != nil {
			return err
		}
		if !resumed {
			t.Fatalf("ResumeParkedInTx did not resume")
		}
		return nil
	}); err != nil {
		t.Fatalf("resume tx: %v", err)
	}
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 0 {
		t.Fatalf("frame surfaced drained while the resumed run is stale: %+v", got)
	}

	completeRunAdmin(ctx, t, d, runID)
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 1 || got[0].FrameID != fix.FrameID {
		t.Fatalf("drained frame not surfaced after run retired: %+v", got)
	}

	nodeB := seedExtraNode(ctx, t, d, fix, "settlement-node-b")
	runB := seedClaimedRunForNode(ctx, t, d, fix, nodeB, frameSettlementSup)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, runB,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, nil, tx)
	}); err != nil {
		t.Fatalf("fail run %s: %v", runB, err)
	}
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 1 || got[0].FrameID != fix.FrameID {
		t.Fatalf("terminal-failed run held the frame open: %+v", got)
	}
}

func testFrameSettlementHasFailedNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()

	hasFailed := func(frameID shared.UUID) bool {
		t.Helper()
		var failed bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			failed, err = frames.HasFailedNode(ctx, fix.InstanceID, frameID, tx)
			return err
		}); err != nil {
			t.Fatalf("HasFailedNode: %v", err)
		}
		return failed
	}

	runToFail := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, frameSettlementSup)
	if hasFailed(fix.FrameID) {
		t.Fatalf("HasFailedNode = true with only a claimed running run")
	}

	parkedNodeID := seedExtraNode(ctx, t, d, fix, "frame-settlement-parked-node")
	parkedRunID := seedClaimedRunForNode(ctx, t, d, fix, parkedNodeID, frameSettlementSup)
	now := time.Now().UTC()
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: parkedRunID, ExpectedClaimedBy: frameSettlementSup,
		ParkedAt: now, ResumeAt: now.Add(time.Hour),
	})
	if hasFailed(fix.FrameID) {
		t.Fatalf("HasFailedNode = true with a parked run present: parked is neither failed nor a reason to fail the frame")
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, runToFail,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, nil, tx)
	}); err != nil {
		t.Fatalf("UpdateState(failed): %v", err)
	}
	if !hasFailed(fix.FrameID) {
		t.Fatalf("HasFailedNode = false after running → failed")
	}

	var otherFrame shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := frames.MarkFrameEnded(ctx, fix.FrameID, tx); err != nil {
			return err
		}
		otherScope := seedMainRunScopeForInstance(ctx, t, tx, store, fix.InstanceID)
		var err error
		otherFrame, err = frames.InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, otherScope, tx)
		return err
	}); err != nil {
		t.Fatalf("InsertFrame: %v", err)
	}
	if hasFailed(otherFrame) {
		t.Fatalf("HasFailedNode leaked across frames: frame %s has no runs", otherFrame)
	}
}

func testFrameSettlementMarkSourceNodeStale(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()
	q := d.Queue()

	nodeS := seedExtraNode(ctx, t, d, fix, "settlement-source-node")

	mark := func(nodeID, frameID shared.UUID) bool {
		t.Helper()
		var matched bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			matched, err = frames.MarkSourceNodeStale(ctx, fix.InstanceID, nodeID, frameID, tx)
			return err
		}); err != nil {
			t.Fatalf("MarkSourceNodeStale: %v", err)
		}
		return matched
	}
	inFlightRun := func(nodeID shared.UUID) (shared.UUID, bool) {
		t.Helper()
		var id shared.UUID
		var found bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			id, found, err = q.GetInFlightRunForNode(ctx, tx, nodeID, fix.MainRunScopeID)
			return err
		}); err != nil {
			t.Fatalf("GetInFlightRunForNode: %v", err)
		}
		return id, found
	}

	if !mark(nodeS, fix.FrameID) {
		t.Fatalf("MarkSourceNodeStale on a fresh source returned matched=false")
	}
	runID, found := inFlightRun(nodeS)
	if !found {
		t.Fatalf("no in-flight run row after MarkSourceNodeStale")
	}

	if !mark(nodeS, fix.FrameID) {
		t.Fatalf("re-entrant MarkSourceNodeStale returned matched=false")
	}
	runID2, found := inFlightRun(nodeS)
	if !found || runID2 != runID {
		t.Fatalf("re-entry changed the in-flight row: first=%s second=%s found=%v", runID, runID2, found)
	}

	var otherFrame shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := frames.MarkFrameEnded(ctx, fix.FrameID, tx); err != nil {
			return err
		}
		otherScope := seedMainRunScopeForInstance(ctx, t, tx, store, fix.InstanceID)
		var err error
		otherFrame, err = frames.InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, otherScope, tx)
		return err
	}); err != nil {
		t.Fatalf("InsertFrame: %v", err)
	}
	if mark(nodeS, otherFrame) {
		t.Fatalf("MarkSourceNodeStale matched a source already in-flight under another frame")
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, runID, frameSettlementSup)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("claim failed")
		}
		_, err = q.PromoteClaimedToRunning(ctx, tx, runID, frameSettlementSup)
		return err
	}); err != nil {
		t.Fatalf("claim tx: %v", err)
	}
	if mark(nodeS, fix.FrameID) {
		t.Fatalf("MarkSourceNodeStale matched a claimed (active) source")
	}
}

func testFrameSettlementOrphanDispatches(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()

	listOrphans := func() []persistence.OrphanFrameDispatch {
		t.Helper()
		var mine []persistence.OrphanFrameDispatch
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			all, err := frames.ListOrphanFrameDispatches(ctx, tx)
			if err != nil {
				return err
			}
			for _, o := range all {
				if o.FrameID == fix.FrameID {
					mine = append(mine, o)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("ListOrphanFrameDispatches: %v", err)
		}
		return mine
	}

	claimedRun := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, frameSettlementSup)
	nodeB := seedExtraNode(ctx, t, d, fix, "orphan-node-b")
	_ = seedConformanceRunForNode(ctx, t, d, nodeB, fix.FrameID)

	if got := listOrphans(); len(got) != 0 {
		t.Fatalf("orphans reported under a running frame: %+v", got)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		transitioned, err := frames.MarkFrameEnded(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkFrameEnded did not transition")
		}
		return nil
	}); err != nil {
		t.Fatalf("terminal tx: %v", err)
	}
	got := listOrphans()
	if len(got) != 1 || got[0].NodeRunID != claimedRun || got[0].ClaimedBy != frameSettlementSup {
		t.Fatalf("orphan set = %+v, want exactly [{%s %s %s}]", got, claimedRun, frameSettlementSup, fix.FrameID)
	}

	if err := d.Queue().ReleaseClaim(ctx, claimedRun, frameSettlementSup); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if got := listOrphans(); len(got) != 0 {
		t.Fatalf("orphan persisted after claim release: %+v", got)
	}
}
