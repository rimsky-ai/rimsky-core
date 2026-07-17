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

func seedClaimedRunForNode(
	ctx context.Context, t *testing.T, d persistence.Database,
	fix fixtureSet, nodeID shared.UUID, supID string,
) shared.UUID {
	t.Helper()
	q := d.Queue()
	var nodeRunID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  32,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID != nodeID {
				continue
			}
			ok, err := q.ClaimDispatchRow(ctx, tx, c.NodeRunID, supID)
			if err != nil {
				return err
			}
			if !ok {
				t.Fatalf("seedClaimedRunForNode: claim was not successful")
			}
			if _, err := q.PromoteClaimedToRunning(ctx, tx, c.NodeRunID, supID); err != nil {
				return err
			}
			nodeRunID = c.NodeRunID
			return nil
		}
		t.Fatalf("seedClaimedRunForNode: candidate not surfaced for node %s", nodeID)
		return nil
	}); err != nil {
		t.Fatalf("seedClaimedRunForNode: %v", err)
	}
	return nodeRunID
}

func seedExtraNode(ctx context.Context, t *testing.T, d persistence.Database, fix fixtureSet, nodeType string) shared.UUID {
	t.Helper()
	store := d.Tables()
	id := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: id, InstanceID: fix.InstanceID,
			NodeType: nodeType, Executor: "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seedExtraNode: %v", err)
	}
	return id
}

func parkRun(ctx context.Context, t *testing.T, d persistence.Database, in persistence.ParkActiveInput) {
	t.Helper()
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := d.Queue().ParkActiveInTx(ctx, tx, in); err != nil {
			return err
		}
		row, err := d.Tables().Nodes().GetRunForGate(ctx, tx, in.NodeRunID)
		if err != nil {
			return err
		}
		if row == nil {
			return nil
		}
		return d.Tables().Nodes().UpdateState(ctx, in.NodeRunID,
			cascade.NodeStateParked, cascade.ReasonHandlerPark, nil, tx)
	}); err != nil {
		t.Fatalf("ParkActiveInTx(%s): %v", in.NodeRunID, err)
	}
}

func resumeRunInTx(ctx context.Context, d persistence.Database, tx persistence.Tx, nodeRunID shared.UUID) (bool, error) {
	resumed, err := d.Queue().ResumeParkedInTx(ctx, tx, nodeRunID)
	if err != nil || !resumed {
		return resumed, err
	}
	row, err := d.Tables().Nodes().GetRunForGate(ctx, tx, nodeRunID)
	if err != nil {
		return false, err
	}
	if row == nil {
		return true, nil
	}
	if err := d.Tables().Nodes().UpdateState(ctx, nodeRunID,
		cascade.NodeStateStale, cascade.ReasonDeadlineResume, nil, tx); err != nil {
		return false, err
	}
	return true, nil
}

const parkResumeSup = "park-resume-supervisor"

func testParkResumeSweepSelection(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	now := time.Now()

	nodeA := fix.NodeID
	nodeB := seedExtraNode(ctx, t, d, fix, "park-node-b")
	nodeC := seedExtraNode(ctx, t, d, fix, "park-node-c")
	runA := seedClaimedRunForNode(ctx, t, d, fix, nodeA, parkResumeSup)
	runB := seedClaimedRunForNode(ctx, t, d, fix, nodeB, parkResumeSup)
	runC := seedClaimedRunForNode(ctx, t, d, fix, nodeC, parkResumeSup)

	maxPark := 60
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.UpdateDispatchTuningInTx(ctx, tx, runC, &maxPark, nil)
	}); err != nil {
		t.Fatalf("UpdateDispatchTuningInTx: %v", err)
	}

	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runA, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-3 * time.Hour), ResumeAt: now.Add(-2 * time.Hour),
		Reason: "snooze",
	})
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-3 * time.Hour), ResumeAt: now.Add(-1 * time.Hour),
		Reason: "snooze",
	})
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runC, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-1 * time.Hour), ResumeAt: now.Add(24 * time.Hour),
		Reason: "await_callback",
	})

	ready, err := q.ListParkedReadyForResume(ctx, now.Add(-90*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(mid cutoff): %v", err)
	}
	if len(ready) != 1 || ready[0].NodeRunID != runA {
		t.Fatalf("mid-cutoff ready set = %+v, want exactly [runA=%s]", ready, runA)
	}

	ready, err = q.ListParkedReadyForResume(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(now): %v", err)
	}
	if len(ready) != 2 || ready[0].NodeRunID != runA || ready[1].NodeRunID != runB {
		t.Fatalf("ready set = %+v, want [runA=%s, runB=%s] ascending", ready, runA, runB)
	}

	ready, err = q.ListParkedReadyForResume(ctx, now, 1)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(limit 1): %v", err)
	}
	if len(ready) != 1 || ready[0].NodeRunID != runA {
		t.Fatalf("limit-1 ready set = %+v, want [runA=%s]", ready, runA)
	}

	overdue, err := q.ListParkedOverdue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListParkedOverdue: %v", err)
	}
	if len(overdue) != 1 || overdue[0].NodeRunID != runC {
		t.Fatalf("overdue set = %+v, want exactly [runC=%s]", overdue, runC)
	}

	counts, err := q.CountParkedByReason(ctx)
	if err != nil {
		t.Fatalf("CountParkedByReason: %v", err)
	}
	if counts["snooze"] != 2 || counts["await_callback"] != 1 {
		t.Fatalf("CountParkedByReason = %v, want snooze=2 await_callback=1", counts)
	}
}

func testParkResumeParkedDiagnostic(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	now := time.Now()

	nodeA := fix.NodeID
	nodeB := seedExtraNode(ctx, t, d, fix, "diag-node-b")
	nodeC := seedExtraNode(ctx, t, d, fix, "diag-node-c")
	runA := seedClaimedRunForNode(ctx, t, d, fix, nodeA, parkResumeSup)
	runB := seedClaimedRunForNode(ctx, t, d, fix, nodeB, parkResumeSup)
	_ = seedClaimedRunForNode(ctx, t, d, fix, nodeC, parkResumeSup)

	resumeA := now.Add(2 * time.Hour)
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runA, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-3 * time.Hour), ResumeAt: resumeA,
		Reason: "snooze", ReasonNote: "diag note A",
	})
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-1 * time.Hour), ResumeAt: now.Add(4 * time.Hour),
		Reason: "await_callback",
	})

	listDiag := func(reasonFilter string) []persistence.ParkedDiagnosticRow {
		t.Helper()
		var out []persistence.ParkedDiagnosticRow
		if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
			rows, err := q.ListParkedDiagnostic(ctx, tx, reasonFilter)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if r.InstanceID == fix.InstanceID.String() {
					out = append(out, r)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("ListParkedDiagnostic(%q): %v", reasonFilter, err)
		}
		return out
	}

	got := listDiag("")
	if len(got) != 2 || got[0].NodeRunID != runA || got[1].NodeRunID != runB {
		t.Fatalf("diagnostic set = %+v, want [runA=%s, runB=%s] parked_at-ascending", got, runA, runB)
	}
	a := got[0]
	if a.NodeID != nodeA.String() || a.FrameID != fix.FrameID.String() {
		t.Fatalf("diagnostic row anchors = node %q frame %q, want %s / %s", a.NodeID, a.FrameID, nodeA, fix.FrameID)
	}
	if a.Reason != "snooze" || a.ReasonNote != "diag note A" {
		t.Fatalf("diagnostic row metadata = %+v, want reason=snooze note=\"diag note A\"", a)
	}
	if diff := a.ResumeAt.Sub(resumeA); diff < -time.Second || diff > time.Second {
		t.Fatalf("diagnostic resume_at = %v drifted from %v", a.ResumeAt, resumeA)
	}

	if got := listDiag("snooze"); len(got) != 1 || got[0].NodeRunID != runA {
		t.Fatalf("snooze-filtered set = %+v, want exactly [runA=%s]", got, runA)
	}
	if got := listDiag("await_callback"); len(got) != 1 || got[0].NodeRunID != runB {
		t.Fatalf("await_callback-filtered set = %+v, want exactly [runB=%s]", got, runB)
	}
	if got := listDiag("no_such_reason"); len(got) != 0 {
		t.Fatalf("unknown-reason filter returned rows: %+v", got)
	}
}

func testParkResumeHeldFrameCount(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	countHeld := func() int {
		t.Helper()
		var n int
		if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
			var err error
			n, err = d.Tables().Frames().CountHeldFrames(ctx, tx)
			return err
		}); err != nil {
			t.Fatalf("CountHeldFrames: %v", err)
		}
		return n
	}

	runA := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)
	nodeB := seedExtraNode(ctx, t, d, fix, "held-node-b")
	runB := seedClaimedRunForNode(ctx, t, d, fix, nodeB, parkResumeSup)
	if got := countHeld(); got != 0 {
		t.Fatalf("CountHeldFrames = %d with no parked runs, want 0", got)
	}

	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runA, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "snooze",
	})
	if got := countHeld(); got != 1 {
		t.Fatalf("CountHeldFrames = %d with one parked run, want 1", got)
	}

	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "await_callback",
	})
	if got := countHeld(); got != 1 {
		t.Fatalf("CountHeldFrames = %d with two parked runs in one frame, want 1", got)
	}

	for _, runID := range []shared.UUID{runA, runB} {
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := resumeRunInTx(ctx, d, tx, runID)
			return err
		}); err != nil {
			t.Fatalf("ResumeParkedInTx(%s): %v", runID, err)
		}
	}
	if got := countHeld(); got != 0 {
		t.Fatalf("CountHeldFrames = %d after both resumes, want 0", got)
	}
}

func testParkResumeMetadataRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	now := time.Now()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)
	parkedAt := now.Add(-30 * time.Minute)
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID:         runID,
		ExpectedClaimedBy: parkResumeSup,
		ParkedAt:          parkedAt,
		ResumeAt:          now.Add(-1 * time.Minute),
		Reason:            "snooze",
		ReasonNote:        "free-form note",
	})

	parked, err := q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID)
	if err != nil {
		t.Fatalf("GetParkedByNode: %v", err)
	}
	if parked == nil || parked.NodeRunID != runID {
		t.Fatalf("GetParkedByNode = %+v, want run %s", parked, runID)
	}
	if parked.Reason != "snooze" || parked.ReasonNote != "free-form note" {
		t.Fatalf("parked metadata mismatch: %+v", parked)
	}

	resume := func() bool {
		var resumed bool
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			resumed, err = resumeRunInTx(ctx, d, tx, runID)
			return err
		}); err != nil {
			t.Fatalf("ResumeParkedInTx: %v", err)
		}
		return resumed
	}
	if !resume() {
		t.Fatalf("first ResumeParkedInTx did not resume the parked row")
	}
	if resume() {
		t.Fatalf("second ResumeParkedInTx resumed an already-stale row")
	}

	parked, err = q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID)
	if err != nil {
		t.Fatalf("GetParkedByNode after resume: %v", err)
	}
	if parked != nil {
		t.Fatalf("row still parked after resume: %+v", parked)
	}

	owner, err := q.GetClaimedBy(ctx, runID)
	if err != nil {
		t.Fatalf("GetClaimedBy after resume: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("resumed row ownership = %s/%s, want unclaimed", owner.Kind, owner.SupervisorID)
	}
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  32,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeRunID == runID {
				return nil
			}
		}
		t.Fatalf("resumed row %s not surfaced as a dispatch candidate", runID)
		return nil
	}); err != nil {
		t.Fatalf("SelectCandidates after resume: %v", err)
	}
}

func testRegisterAsyncAckRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)
	ackID := "ack-" + uuid.New().String()
	expectedPrincipal := "executor-" + uuid.New().String()
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RegisterAsyncAck(ctx, tx, runID, ackID, now, nil, nil, expectedPrincipal)
	}); err != nil {
		t.Fatalf("RegisterAsyncAck: %v", err)
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		got, err := q.LookupRunByAsyncAckID(ctx, tx, ackID)
		if err != nil {
			return err
		}
		if got == nil {
			t.Fatalf("LookupRunByAsyncAckID(%q) returned nil after registration", ackID)
		}
		if got.ID != runID {
			t.Fatalf("LookupRunByAsyncAckID = %s, want %s", got.ID, runID)
		}
		if got.AsyncAckID == nil || *got.AsyncAckID != ackID {
			t.Fatalf("dispatch row AsyncAckID = %v, want %s", got.AsyncAckID, ackID)
		}
		if got.LastProgressAt == nil {
			t.Fatalf("RegisterAsyncAck did not set last_progress_at")
		}
		if got.AsyncAckPrincipal == nil || *got.AsyncAckPrincipal != expectedPrincipal {
			t.Fatalf("dispatch row AsyncAckPrincipal = %v, want %s (dispatched-executor principal must round-trip)", got.AsyncAckPrincipal, expectedPrincipal)
		}
		return nil
	}); err != nil {
		t.Fatalf("LookupRunByAsyncAckID round-trip: %v", err)
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		got, err := q.LookupRunByAsyncAckID(ctx, tx, "no-such-ack-id")
		if err != nil {
			return err
		}
		if got != nil {
			t.Fatalf("LookupRunByAsyncAckID(unknown) = %+v, want nil", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("LookupRunByAsyncAckID(unknown): %v", err)
	}
}

func testBumpLastProgressAt(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)

	t1 := time.Now().UTC().Truncate(time.Second)
	var found bool
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var berr error
		found, berr = q.BumpLastProgressAt(ctx, tx, runID, t1)
		return berr
	}); err != nil {
		t.Fatalf("BumpLastProgressAt(t1): %v", err)
	}
	if !found {
		t.Fatalf("BumpLastProgressAt(t1): found=false for seeded run %s", runID)
	}

	got, err := q.GetByID(ctx, runID)
	if err != nil || got == nil {
		t.Fatalf("GetByID after first bump: row=%v err=%v", got, err)
	}
	if got.LastProgressAt == nil || got.LastProgressAt.Before(t1.Add(-time.Second)) {
		t.Fatalf("last_progress_at after first bump = %v, want >= %v", got.LastProgressAt, t1)
	}

	t2 := t1.Add(5 * time.Second)
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var berr error
		found, berr = q.BumpLastProgressAt(ctx, tx, runID, t2)
		return berr
	}); err != nil {
		t.Fatalf("BumpLastProgressAt(t2): %v", err)
	}
	if !found {
		t.Fatalf("BumpLastProgressAt(t2): found=false for seeded run %s", runID)
	}
	got, err = q.GetByID(ctx, runID)
	if err != nil || got == nil {
		t.Fatalf("GetByID after second bump: row=%v err=%v", got, err)
	}
	if got.LastProgressAt == nil || got.LastProgressAt.Before(t2.Add(-time.Second)) {
		t.Fatalf("last_progress_at after second bump = %v, want >= %v", got.LastProgressAt, t2)
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var berr error
		found, berr = q.BumpLastProgressAt(ctx, tx, shared.UUID(uuid.New()), t2)
		return berr
	}); err != nil {
		t.Fatalf("BumpLastProgressAt(bogus): %v", err)
	}
	if found {
		t.Fatalf("BumpLastProgressAt(bogus): found=true, want false")
	}
}
