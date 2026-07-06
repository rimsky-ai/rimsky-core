// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

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
	if err := d.Queue().Complete(ctx, runID, ""); err != nil {
		t.Fatalf("Queue.Complete(%s): %v", runID, err)
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
		DispatchID: runID, ExpectedClaimedBy: frameSettlementSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "snooze",
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
		if _, err := frames.MarkRunningFrameTerminal(ctx, fix.FrameID, persistence.FrameStateFailed, tx); err != nil {
			return err
		}
		otherScope := seedMainRunScopeForInstance(ctx, t, tx, store, fix.InstanceID)
		var err error
		otherFrame, err = frames.InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, otherScope, 600000, tx)
		return err
	}); err != nil {
		t.Fatalf("InsertFrame: %v", err)
	}
	if hasFailed(otherFrame) {
		t.Fatalf("HasFailedNode leaked across frames: frame %s has no runs", otherFrame)
	}
}

func seedTerminateAfterRunInstance(ctx context.Context, t *testing.T, d persistence.Database, fix fixtureSet) fixtureSet {
	t.Helper()
	store := d.Tables()
	out := fixtureSet{TemplateHash: fix.TemplateHash}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		instanceID := shared.UUID(uuid.New())
		out.InstanceID = instanceID
		out.MainRunScopeID = seedMainRunScopeForInstance(ctx, t, tx, store, instanceID)
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:                instanceID,
			TemplateHash:      fix.TemplateHash,
			TerminateAfterRun: true,
		}, tx); err != nil {
			return err
		}
		nodeID := shared.UUID(uuid.New())
		out.NodeID = nodeID
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nodeID,
			InstanceID: instanceID,
			NodeType:   "fixture-node-type",
			Executor:   "test-executor",
		}, tx); err != nil {
			return err
		}
		messageID := shared.UUID(uuid.New())
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         messageID,
			InstanceID: instanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		out.MessageID = messageID
		frameID, err := store.Frames().InsertRunningFrame(ctx, instanceID, messageID, out.MainRunScopeID, 600000, tx)
		if err != nil {
			return err
		}
		out.FrameID = frameID
		return nil
	}); err != nil {
		t.Fatalf("seedTerminateAfterRunInstance: %v", err)
	}
	return out
}

func testFrameSettlementInstanceTermination(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()

	markIfDone := func(instanceID shared.UUID) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return frames.MarkInstanceTerminatedIfDone(ctx, instanceID, tx)
		}); err != nil {
			t.Fatalf("MarkInstanceTerminatedIfDone: %v", err)
		}
	}
	terminatedAt := func(instanceID shared.UUID) *time.Time {
		t.Helper()
		var ts *time.Time
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			row, err := store.Instances().Get(ctx, instanceID, tx)
			if err != nil {
				return err
			}
			if row == nil {
				t.Fatalf("instance %s missing", instanceID)
			}
			ts = row.TerminatedAt
			return nil
		}); err != nil {
			t.Fatalf("Instances.Get: %v", err)
		}
		return ts
	}

	markIfDone(fix.InstanceID)
	if got := terminatedAt(fix.InstanceID); got != nil {
		t.Fatalf("durable instance terminated: terminated_at=%v", got)
	}

	tfr := seedTerminateAfterRunInstance(ctx, t, d, fix)
	runID := seedClaimedRunForNode(ctx, t, d, tfr, tfr.NodeID, frameSettlementSup)
	markIfDone(tfr.InstanceID)
	if got := terminatedAt(tfr.InstanceID); got != nil {
		t.Fatalf("instance terminated with an unresolved stale run: %v", got)
	}

	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runID, ExpectedClaimedBy: frameSettlementSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "snooze",
	})
	markIfDone(tfr.InstanceID)
	if got := terminatedAt(tfr.InstanceID); got != nil {
		t.Fatalf("instance terminated with a PARKED run: %v", got)
	}

	pendingMsgID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         pendingMsgID,
			InstanceID: tfr.InstanceID,
			Type:       "fixture/pending",
			Sender:     "operator",
			SenderKind: "operator",
		})
	}); err != nil {
		t.Fatalf("insert pending message: %v", err)
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := resumeRunInTx(ctx, d, tx, runID)
		return err
	}); err != nil {
		t.Fatalf("ResumeParkedInTx: %v", err)
	}
	completeRunAdmin(ctx, t, d, runID)
	markIfDone(tfr.InstanceID)
	first := terminatedAt(tfr.InstanceID)
	if first == nil {
		t.Fatalf("terminate_after_run instance did not terminate once drained")
	}

	markIfDone(tfr.InstanceID)
	if got := terminatedAt(tfr.InstanceID); got == nil || !got.Equal(*first) {
		t.Fatalf("terminated_at re-stamped: first=%v second=%v", first, got)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := frames.MarkRunningFrameTerminal(ctx, tfr.FrameID, persistence.FrameStateCompleted, tx); err != nil {
			return err
		}
		picks, err := store.Messages().PickPendingMessagesForIdleInstances(ctx, tx)
		if err != nil {
			return err
		}
		for _, p := range picks {
			if p.InstanceID == tfr.InstanceID {
				t.Fatalf("pending message %s for terminated instance %s was picked for frame open; pending %s must never wake a terminated instance", p.MessageID, tfr.InstanceID, pendingMsgID)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("terminated-instance pickup check: %v", err)
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
		if _, err := frames.MarkRunningFrameTerminal(ctx, fix.FrameID, persistence.FrameStateCompleted, tx); err != nil {
			return err
		}
		otherScope := seedMainRunScopeForInstance(ctx, t, tx, store, fix.InstanceID)
		var err error
		otherFrame, err = frames.InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, otherScope, 600000, tx)
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

func testFrameSettlementStuckFrames(
	t *testing.T, d persistence.Database,
	rawExec func(t *testing.T, d persistence.Database, sql string, args ...any),
) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()
	q := d.Queue()

	const timeoutMs = int64(600000)
	frameID := fix.FrameID
	backdate := func() {
		t.Helper()
		rawExec(t, d,
			`UPDATE rimsky_frames SET last_progress_at = ? WHERE frame_id = ?`,
			time.Now().UTC().Add(-11*time.Minute).Format(time.RFC3339Nano),
			frameID.String(),
		)
	}

	listStuck := func() []persistence.FrameStuck {
		t.Helper()
		var mine []persistence.FrameStuck
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			all, err := frames.ListStuckRunningFrames(ctx, tx)
			if err != nil {
				return err
			}
			for _, s := range all {
				if s.InstanceID == fix.InstanceID {
					mine = append(mine, s)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("ListStuckRunningFrames: %v", err)
		}
		return mine
	}

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, frameID)

	if got := listStuck(); len(got) != 0 {
		t.Fatalf("frame stuck inside a fresh progress window: %+v", got)
	}

	backdate()
	got := listStuck()
	if len(got) != 1 || got[0].FrameID != frameID || got[0].FrameTimeoutMs != timeoutMs {
		t.Fatalf("stuck set = %+v, want [{%s %s %d}]", got, frameID, fix.InstanceID, timeoutMs)
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
	if got := listStuck(); len(got) != 0 {
		t.Fatalf("frame stuck while its dispatch is claimed: %+v", got)
	}

	if err := q.ReleaseClaim(ctx, runID, frameSettlementSup); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if got := listStuck(); len(got) != 1 || got[0].FrameID != frameID {
		t.Fatalf("released frame not stuck: %+v", got)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return frames.RefreshProgress(ctx, frameID, tx)
	}); err != nil {
		t.Fatalf("RefreshProgress: %v", err)
	}
	if got := listStuck(); len(got) != 0 {
		t.Fatalf("frame stuck immediately after RefreshProgress: %+v", got)
	}

	backdate()
	if got := listStuck(); len(got) != 1 {
		t.Fatalf("frame not stuck after window re-elapsed: %+v", got)
	}
	completeRunAdmin(ctx, t, d, runID)
	if got := listStuck(); len(got) != 0 {
		t.Fatalf("drained frame reported stuck: %+v", got)
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
		transitioned, err := frames.MarkRunningFrameTerminal(ctx, fix.FrameID, persistence.FrameStateCompleted, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkRunningFrameTerminal did not transition")
		}
		return nil
	}); err != nil {
		t.Fatalf("terminal tx: %v", err)
	}
	got := listOrphans()
	if len(got) != 1 || got[0].DispatchID != claimedRun || got[0].ClaimedBy != frameSettlementSup {
		t.Fatalf("orphan set = %+v, want exactly [{%s %s %s}]", got, claimedRun, frameSettlementSup, fix.FrameID)
	}

	if err := d.Queue().ReleaseClaim(ctx, claimedRun, frameSettlementSup); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if got := listOrphans(); len(got) != 0 {
		t.Fatalf("orphan persisted after claim release: %+v", got)
	}
}
