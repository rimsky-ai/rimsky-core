// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: FrameSettlement conformance area.
// Pins the frame-engine settlement core (graph/frame/engine.go's
// frame-end detection, instance termination, stuck-frame warning, and
// orphan-dispatch reaper) plus the producer-side source-node binding:
//
//   - ListRunningFramesNoPendingNodes: a running frame surfaces only
//     when every run row in its scope has left the unresolved set —
//     parked rows hold the frame open, terminal-failed rows do not.
//   - HasFailedNode: the frame's terminal-flavor read (any failed run
//     row in the (instance, frame) scope).
//   - MarkInstanceTerminatedIfDone: the durable-by-default predicate —
//     only terminate_after_run instances terminate, never while any
//     run is unresolved (stale / running / parked), strict semantics
//     (queued frames do NOT block termination), idempotent set-once.
//     Plus ListQueuedFramesReadyToStart's terminated-instance guard:
//     an orphaned queued frame on a terminated instance never
//     surfaces ready.
//   - MarkSourceNodeStale: fresh source → pending/stale run row bound
//     to the frame (matched=true); idempotent re-entry on the same
//     frame without minting a second in-flight row; out-of-bounds
//     (claimed / other-frame in-flight) returns matched=false.
//   - ListStuckRunningFrames: the no-progress-in-window predicate —
//     claimed dispatches and RefreshProgress both clear the warning;
//     draining the dispatchable work clears it permanently.
//   - ListOrphanFrameDispatches: claimed dispatch rows whose owning
//     frame reached terminal state.
//
// Every one of these queries is hand-mirrored in both drivers (postgres
// uses INTERVAL arithmetic and NOT EXISTS sub-selects; sqlite computes
// the stuck-window in Go over RFC3339 text timestamps) — the
// highest-drift-risk surface in the persistence layer, hence identical
// observable assertions on both drivers.
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

// framePendingForInstance filters ListRunningFramesNoPendingNodes down
// to the test's own instance (the suite shares one database per
// subtest, but defensive filtering keeps the assertions exact).
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

// completeRunAdmin retires a run row via the identity-free admin mode
// (the carve-out pinned in claimant_guard.go).
func completeRunAdmin(ctx context.Context, t *testing.T, d persistence.Database, runID shared.UUID) {
	t.Helper()
	if err := d.Queue().Complete(ctx, runID, ""); err != nil {
		t.Fatalf("Queue.Complete(%s): %v", runID, err)
	}
}

// testFrameSettlementNoPendingNodes covers
// ListRunningFramesNoPendingNodes' unresolved-work predicate: stale and
// parked runs hold the frame open; resolved and terminal-failed runs do
// not.
func testFrameSettlementNoPendingNodes(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	// @constraint: a running frame with no run rows surfaces as drained.
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 1 || got[0].FrameID != fix.FrameID {
		t.Fatalf("empty running frame not surfaced: %+v, want [%s]", got, fix.FrameID)
	}

	// @constraint: a pending/stale run row in the frame's scope holds it open.
	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 0 {
		t.Fatalf("frame surfaced drained while a stale run is pending: %+v", got)
	}

	// @constraint: a parked run holds the frame open — draining while a
	// run sits parked would discard the park's eventual resume.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, runID, frameSettlementSup)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("claim for park failed")
		}
		return nil
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

	// @constraint: resume (parked → pending/stale) MUST keep the
	// frame unresolved and held — the supervisor treats both states
	// as unsettled.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		resumed, err := q.ResumeParkedInTx(ctx, tx, runID)
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

	// @constraint: retiring the run drains the frame.
	completeRunAdmin(ctx, t, d, runID)
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 1 || got[0].FrameID != fix.FrameID {
		t.Fatalf("drained frame not surfaced after run retired: %+v", got)
	}

	// @constraint: a terminal-failed run row does NOT hold the frame —
	// failed is genuinely terminal (the engine picks the frame's failed
	// flavor via HasFailedNode instead).
	nodeB := seedExtraNode(ctx, t, d, fix, "settlement-node-b")
	runB := seedClaimedRunForNode(ctx, t, d, fix, nodeB, frameSettlementSup)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Nodes().UpdateState(ctx, nodeB, fix.MainRunScopeID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx); err != nil {
			return err
		}
		return store.Nodes().UpdateState(ctx, nodeB, fix.MainRunScopeID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, nil, tx)
	}); err != nil {
		t.Fatalf("fail run %s: %v", runB, err)
	}
	if got := framePendingForInstance(ctx, t, d, fix.InstanceID); len(got) != 1 || got[0].FrameID != fix.FrameID {
		t.Fatalf("terminal-failed run held the frame open: %+v", got)
	}
}

// testFrameSettlementHasFailedNode covers the failure-detection read
// the engine uses to pick a drained frame's terminal flavor.
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

	// @constraint: a stale (and then running) run is unresolved, not failed.
	_ = seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, frameSettlementSup)
	if hasFailed(fix.FrameID) {
		t.Fatalf("HasFailedNode = true with only a claimed stale run")
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, fix.NodeID, fix.MainRunScopeID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	}); err != nil {
		t.Fatalf("UpdateState(running): %v", err)
	}
	if hasFailed(fix.FrameID) {
		t.Fatalf("HasFailedNode = true with only a running run")
	}

	// @constraint: running → failed flips the read for THIS frame only.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, fix.NodeID, fix.MainRunScopeID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, nil, tx)
	}); err != nil {
		t.Fatalf("UpdateState(failed): %v", err)
	}
	if !hasFailed(fix.FrameID) {
		t.Fatalf("HasFailedNode = false after running → failed")
	}

	// @constraint: frame-scoped read — a different frame in the same
	// instance reads clean.
	var otherFrame shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		otherFrame, err = frames.InsertFrame(ctx, fix.InstanceID, fix.MessageID, 600000, tx)
		return err
	}); err != nil {
		t.Fatalf("InsertFrame: %v", err)
	}
	if hasFailed(otherFrame) {
		t.Fatalf("HasFailedNode leaked across frames: frame %s has no runs", otherFrame)
	}
}

// seedTerminateAfterRunInstance builds a second instance on the
// fixture's template with terminate_after_run = true, plus its own main
// RunScope, node, and a promoted running frame. Returns a fixtureSet
// shaped for the existing run-seeding helpers.
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
			MainRunScopeID:    out.MainRunScopeID,
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
		// @constraint: synthetic envelope satisfies the
		// rimsky_frames.triggering_message_id NOT NULL FK.
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
		frameID, err := store.Frames().InsertFrame(ctx, instanceID, messageID, 600000, tx)
		if err != nil {
			return err
		}
		out.FrameID = frameID
		_, err = store.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx)
		return err
	}); err != nil {
		t.Fatalf("seedTerminateAfterRunInstance: %v", err)
	}
	return out
}

// testFrameSettlementInstanceTermination covers
// MarkInstanceTerminatedIfDone's full predicate plus
// ListQueuedFramesReadyToStart's terminated-instance guard.
func testFrameSettlementInstanceTermination(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()
	q := d.Queue()

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

	// @constraint: durable instance (the default) — never touched, even
	// fully drained.
	markIfDone(fix.InstanceID)
	if got := terminatedAt(fix.InstanceID); got != nil {
		t.Fatalf("durable instance terminated: terminated_at=%v", got)
	}

	// @constraint: terminate_after_run instance with an unresolved
	// (stale) run — the unresolved-work guard blocks termination.
	tfr := seedTerminateAfterRunInstance(ctx, t, d, fix)
	runID := seedClaimedRunForNode(ctx, t, d, tfr, tfr.NodeID, frameSettlementSup)
	markIfDone(tfr.InstanceID)
	if got := terminatedAt(tfr.InstanceID); got != nil {
		t.Fatalf("instance terminated with an unresolved stale run: %v", got)
	}

	// @constraint: parked run still blocks termination — a later wake
	// must never land on a terminated instance.
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runID, ExpectedClaimedBy: frameSettlementSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "snooze",
	})
	markIfDone(tfr.InstanceID)
	if got := terminatedAt(tfr.InstanceID); got != nil {
		t.Fatalf("instance terminated with a PARKED run: %v", got)
	}

	// @constraint: strict semantics — a queued frame does NOT block
	// termination. Park resolved + run retired → the predicate holds even
	// though a queued frame is waiting.
	var queuedFrame shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		queuedFrame, err = frames.InsertFrame(ctx, tfr.InstanceID, tfr.MessageID, 600000, tx)
		return err
	}); err != nil {
		t.Fatalf("InsertFrame(queued): %v", err)
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := q.ResumeParkedInTx(ctx, tx, runID)
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

	// @constraint: idempotent set-once — a second invocation keeps the stamp.
	markIfDone(tfr.InstanceID)
	if got := terminatedAt(tfr.InstanceID); got == nil || !got.Equal(*first) {
		t.Fatalf("terminated_at re-stamped: first=%v second=%v", first, got)
	}

	// @constraint: terminated-instance guard — the orphaned queued frame
	// never surfaces ready (it must not run against a terminated instance).
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := frames.MarkRunningFrameTerminal(ctx, tfr.FrameID, persistence.FrameStateCompleted, tx); err != nil {
			return err
		}
		ready, err := frames.ListQueuedFramesReadyToStart(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range ready {
			if r.InstanceID == tfr.InstanceID {
				t.Fatalf("queued frame %s surfaced ready on a TERMINATED instance (frame %s)", r.FrameID, queuedFrame)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("terminated-instance ready check: %v", err)
	}
}

// testFrameSettlementMarkSourceNodeStale covers the source-node binding:
// fresh → pending/stale insert, idempotent re-entry, and the
// out-of-bounds (other-frame / claimed) rejections.
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

	// @constraint: fresh source — matched=true, one pending/stale run row
	// exists bound to the frame.
	if !mark(nodeS, fix.FrameID) {
		t.Fatalf("MarkSourceNodeStale on a fresh source returned matched=false")
	}
	runID, found := inFlightRun(nodeS)
	if !found {
		t.Fatalf("no in-flight run row after MarkSourceNodeStale")
	}

	// @constraint: idempotent re-entry (redelivered frame-start under
	// contention) — matched=true again, SAME in-flight row, no sibling minted.
	if !mark(nodeS, fix.FrameID) {
		t.Fatalf("re-entrant MarkSourceNodeStale returned matched=false")
	}
	runID2, found := inFlightRun(nodeS)
	if !found || runID2 != runID {
		t.Fatalf("re-entry changed the in-flight row: first=%s second=%s found=%v", runID, runID2, found)
	}

	// @constraint: out-of-bounds — the source is already in-flight under
	// a DIFFERENT frame → matched=false (the engine must roll back the
	// promotion).
	var otherFrame shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		otherFrame, err = frames.InsertFrame(ctx, fix.InstanceID, fix.MessageID, 600000, tx)
		return err
	}); err != nil {
		t.Fatalf("InsertFrame: %v", err)
	}
	if mark(nodeS, otherFrame) {
		t.Fatalf("MarkSourceNodeStale matched a source already in-flight under another frame")
	}

	// @constraint: out-of-bounds — a claimed (active) run rejects even
	// for its own frame; the source is no longer pending/stale.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, runID, frameSettlementSup)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("claim failed")
		}
		return nil
	}); err != nil {
		t.Fatalf("claim tx: %v", err)
	}
	if mark(nodeS, fix.FrameID) {
		t.Fatalf("MarkSourceNodeStale matched a claimed (active) source")
	}
}

// testFrameSettlementStuckFrames covers ListStuckRunningFrames'
// no-progress-in-window predicate. The window math is the single most
// driver-divergent query in the layer (postgres INTERVAL arithmetic vs
// sqlite Go-side time parsing), so each predicate arm gets its own
// assertion.
//
// The schema enforces frame_timeout_ms >= 60000, so "window elapsed" is
// produced by backdating last_progress_at via rawExec rather than by
// sleeping — deterministic on a loaded machine, and it exercises the
// drivers' actual window arithmetic against a stored timestamp. The
// backdate value is an RFC3339Nano string: sqlite stores exactly that
// text shape (types.go::formatTime) and postgres casts it to
// timestamptz.
func testFrameSettlementStuckFrames(
	t *testing.T, d persistence.Database,
	rawExec func(t *testing.T, d persistence.Database, sql string, args ...any),
) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()
	q := d.Queue()

	// @deliberate: the fixture frame carries the 600000ms timeout; an
	// 11-minute-old last_progress_at puts it past the window.
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

	// @constraint: fresh window — not stuck yet.
	if got := listStuck(); len(got) != 0 {
		t.Fatalf("frame stuck inside a fresh progress window: %+v", got)
	}

	// @constraint: window elapsed with an unclaimed stale run — stuck,
	// carrying the frame's timeout for the warning message.
	backdate()
	got := listStuck()
	if len(got) != 1 || got[0].FrameID != frameID || got[0].FrameTimeoutMs != timeoutMs {
		t.Fatalf("stuck set = %+v, want [{%s %s %d}]", got, frameID, fix.InstanceID, timeoutMs)
	}

	// @constraint: a claimed dispatch means work is being executed — not stuck.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, runID, frameSettlementSup)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("claim failed")
		}
		return nil
	}); err != nil {
		t.Fatalf("claim tx: %v", err)
	}
	if got := listStuck(); len(got) != 0 {
		t.Fatalf("frame stuck while its dispatch is claimed: %+v", got)
	}

	// @constraint: released back to unclaimed — stuck again.
	if err := q.ReleaseClaim(ctx, runID, frameSettlementSup); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if got := listStuck(); len(got) != 1 || got[0].FrameID != frameID {
		t.Fatalf("released frame not stuck: %+v", got)
	}

	// @constraint: RefreshProgress restarts the window (the no-progress
	// metric, not frame age) — not stuck.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return frames.RefreshProgress(ctx, frameID, tx)
	}); err != nil {
		t.Fatalf("RefreshProgress: %v", err)
	}
	if got := listStuck(); len(got) != 0 {
		t.Fatalf("frame stuck immediately after RefreshProgress: %+v", got)
	}

	// @constraint: window elapses again, then the dispatchable work
	// drains — a frame with no stale/running run is not stuck no matter
	// how old.
	backdate()
	if got := listStuck(); len(got) != 1 {
		t.Fatalf("frame not stuck after window re-elapsed: %+v", got)
	}
	completeRunAdmin(ctx, t, d, runID)
	if got := listStuck(); len(got) != 0 {
		t.Fatalf("drained frame reported stuck: %+v", got)
	}
}

// testFrameSettlementOrphanDispatches covers ListOrphanFrameDispatches:
// claimed dispatch rows whose owning frame reached terminal state.
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

	// @deliberate: seed one claimed run + one unclaimed run in the
	// fixture frame.
	claimedRun := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, frameSettlementSup)
	nodeB := seedExtraNode(ctx, t, d, fix, "orphan-node-b")
	_ = seedConformanceRunForNode(ctx, t, d, nodeB, fix.FrameID)

	// @constraint: while the frame is running, nothing is orphaned.
	if got := listOrphans(); len(got) != 0 {
		t.Fatalf("orphans reported under a running frame: %+v", got)
	}

	// @constraint: frame terminal — exactly the claimed dispatch
	// surfaces, carrying the claimant the reaper releases.
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

	// @constraint: releasing the claim clears the orphan (the reaper's
	// fixed point).
	if err := d.Queue().ReleaseClaim(ctx, claimedRun, frameSettlementSup); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if got := listOrphans(); len(got) != 0 {
		t.Fatalf("orphan persisted after claim release: %+v", got)
	}
}
