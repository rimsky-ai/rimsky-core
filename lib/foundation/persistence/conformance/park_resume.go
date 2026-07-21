// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"fmt"
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
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  32,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID != nodeID {
				continue
			}
			ok, err := q.ClaimDispatchRow(ctx, c.NodeRunID, supID, tx)
			if err != nil {
				return err
			}
			if !ok {
				t.Fatalf("seedClaimedRunForNode: claim was not successful")
			}
			if _, err := q.PromoteClaimedToRunning(ctx, c.NodeRunID, supID, tx); err != nil {
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
		if err := d.Queue().ParkActive(ctx, in, tx); err != nil {
			return err
		}
		row, err := d.Tables().Nodes().GetRunForGate(ctx, in.NodeRunID, tx)
		if err != nil {
			return err
		}
		if row == nil {
			return fmt.Errorf("parkRun: node run row %s vanished between ParkActive and GetRunForGate", in.NodeRunID)
		}
		return d.Tables().Nodes().UpdateState(ctx, in.NodeRunID,
			cascade.NodeStateParked, cascade.ReasonHandlerPark, nil, tx)
	}); err != nil {
		t.Fatalf("ParkActive(%s): %v", in.NodeRunID, err)
	}
}

func resumeRunInTx(ctx context.Context, d persistence.Database, nodeRunID shared.UUID, tx persistence.Tx) (bool, error) {
	resumed, err := d.Queue().ResumeParked(ctx, nodeRunID, tx)
	if err != nil || !resumed {
		return resumed, err
	}
	row, err := d.Tables().Nodes().GetRunForGate(ctx, nodeRunID, tx)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, fmt.Errorf("resumeRunInTx: node run row %s vanished between ResumeParked and GetRunForGate", nodeRunID)
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

	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runA, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-3 * time.Hour), ResumeAt: now.Add(-2 * time.Hour),
	})
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-3 * time.Hour), ResumeAt: now.Add(-1 * time.Hour),
	})
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runC, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-1 * time.Hour), ResumeAt: now.Add(24 * time.Hour),
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

	count, err := q.CountParked(ctx)
	if err != nil {
		t.Fatalf("CountParked: %v", err)
	}
	if count < 3 {
		t.Fatalf("CountParked = %d, want >= 3 (runA, runB, runC parked)", count)
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
	})
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-1 * time.Hour), ResumeAt: now.Add(4 * time.Hour),
	})

	listDiag := func() []persistence.ParkedDiagnosticRow {
		t.Helper()
		var out []persistence.ParkedDiagnosticRow
		if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
			rows, err := q.ListParkedDiagnostic(ctx, tx)
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
			t.Fatalf("ListParkedDiagnostic: %v", err)
		}
		return out
	}

	got := listDiag()
	if len(got) != 2 || got[0].NodeRunID != runA || got[1].NodeRunID != runB {
		t.Fatalf("diagnostic set = %+v, want [runA=%s, runB=%s] parked_at-ascending", got, runA, runB)
	}
	a := got[0]
	if a.NodeID != nodeA.String() || a.FrameID != fix.FrameID.String() {
		t.Fatalf("diagnostic row anchors = node %q frame %q, want %s / %s", a.NodeID, a.FrameID, nodeA, fix.FrameID)
	}
	if diff := a.ResumeAt.Sub(resumeA); diff < -time.Second || diff > time.Second {
		t.Fatalf("diagnostic resume_at = %v drifted from %v", a.ResumeAt, resumeA)
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
	})
	if got := countHeld(); got != 1 {
		t.Fatalf("CountHeldFrames = %d with one parked run, want 1", got)
	}

	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
	})
	if got := countHeld(); got != 1 {
		t.Fatalf("CountHeldFrames = %d with two parked runs in one frame, want 1", got)
	}

	for _, runID := range []shared.UUID{runA, runB} {
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := resumeRunInTx(ctx, d, runID, tx)
			return err
		}); err != nil {
			t.Fatalf("ResumeParked(%s): %v", runID, err)
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
	preParkRow, err := q.GetByID(ctx, runID)
	if err != nil || preParkRow == nil {
		t.Fatalf("GetByID before park: row=%v err=%v", preParkRow, err)
	}
	parkedAt := now.Add(-30 * time.Minute)
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID:         runID,
		ExpectedClaimedBy: parkResumeSup,
		ParkedAt:          parkedAt,
		ResumeAt:          now.Add(-1 * time.Minute),
	})

	parked, err := q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID, nil)
	if err != nil {
		t.Fatalf("GetParkedByNode: %v", err)
	}
	if parked == nil || parked.NodeRunID != runID {
		t.Fatalf("GetParkedByNode = %+v, want run %s", parked, runID)
	}
	if parked.ResumeAt == nil {
		t.Fatalf("parked row must carry resume_at: %+v", parked)
	}

	resume := func() bool {
		var resumed bool
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			resumed, err = resumeRunInTx(ctx, d, runID, tx)
			return err
		}); err != nil {
			t.Fatalf("ResumeParked: %v", err)
		}
		return resumed
	}
	if !resume() {
		t.Fatalf("first ResumeParked did not resume the parked row")
	}
	if resume() {
		t.Fatalf("second ResumeParked resumed an already-stale row")
	}

	parked, err = q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID, nil)
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
	if owner.Kind != persistence.ClaimOwnershipKindUnclaimed {
		t.Fatalf("resumed row ownership = %s/%s, want unclaimed", owner.Kind, owner.SupervisorID)
	}
	postResumeRow, err := q.GetByID(ctx, runID)
	if err != nil || postResumeRow == nil {
		t.Fatalf("GetByID after resume: row=%v err=%v", postResumeRow, err)
	}
	if postResumeRow.FrameID != preParkRow.FrameID {
		t.Fatalf("resume must not rebind the run's frame: before=%s after=%s "+
			"(park-resume does not open a new frame)", preParkRow.FrameID, postResumeRow.FrameID)
	}
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  32,
		}, tx)
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

func testParkResumeClearsParkedAt(
	t *testing.T, d persistence.Database,
	rawQuery func(t *testing.T, d persistence.Database, sql string, args ...any) []RawQueryRow,
) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	now := time.Now()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		NodeRunID:         runID,
		ExpectedClaimedBy: parkResumeSup,
		ParkedAt:          now.Add(-30 * time.Minute),
		ResumeAt:          now.Add(-1 * time.Minute),
	})

	parkedAtDuringPark := rawQuery(t, d,
		`SELECT parked_at FROM rimsky_node_runs WHERE id = ?`, runID.String())
	if len(parkedAtDuringPark) != 1 || parkedAtDuringPark[0]["parked_at"] == nil {
		t.Fatalf("parked_at must be set while parked, got %v", parkedAtDuringPark)
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		resumed, err := resumeRunInTx(ctx, d, runID, tx)
		if err != nil {
			return err
		}
		if !resumed {
			t.Fatalf("ResumeParked did not resume the parked row")
		}
		return nil
	}); err != nil {
		t.Fatalf("ResumeParked: %v", err)
	}

	parkedAtAfterResume := rawQuery(t, d,
		`SELECT parked_at FROM rimsky_node_runs WHERE id = ?`, runID.String())
	if len(parkedAtAfterResume) != 1 {
		t.Fatalf("row %s vanished after resume", runID)
	}
	if parkedAtAfterResume[0]["parked_at"] != nil {
		t.Fatalf("parked_at must be cleared on resume, still carries %v "+
			"(stale park diagnostics must not survive a resume)", parkedAtAfterResume[0]["parked_at"])
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
	wantMaxQuietSec := 45
	wantMaxRuntimeSec := 1800
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RegisterAsyncAck(ctx, runID, ackID, now, &wantMaxQuietSec, &wantMaxRuntimeSec, expectedPrincipal, tx)
	}); err != nil {
		t.Fatalf("RegisterAsyncAck: %v", err)
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		got, err := q.LookupRunByAsyncAckID(ctx, ackID, tx)
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
		if got.EffectiveMaxQuietPeriodSeconds == nil || *got.EffectiveMaxQuietPeriodSeconds != wantMaxQuietSec {
			t.Fatalf("dispatch row EffectiveMaxQuietPeriodSeconds = %v, want %d (the denormalized effective "+
				"value computed at dispatch time must survive the RegisterAsyncAck round-trip, not just a "+
				"nil no-op)", got.EffectiveMaxQuietPeriodSeconds, wantMaxQuietSec)
		}
		if got.EffectiveMaxRuntimeSeconds == nil || *got.EffectiveMaxRuntimeSeconds != wantMaxRuntimeSec {
			t.Fatalf("dispatch row EffectiveMaxRuntimeSeconds = %v, want %d (the denormalized effective "+
				"value computed at dispatch time must survive the RegisterAsyncAck round-trip, not just a "+
				"nil no-op)", got.EffectiveMaxRuntimeSeconds, wantMaxRuntimeSec)
		}
		return nil
	}); err != nil {
		t.Fatalf("LookupRunByAsyncAckID round-trip: %v", err)
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		got, err := q.LookupRunByAsyncAckID(ctx, "no-such-ack-id", tx)
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
		found, berr = q.BumpLastProgressAt(ctx, runID, t1, tx)
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
		found, berr = q.BumpLastProgressAt(ctx, runID, t2, tx)
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
		found, berr = q.BumpLastProgressAt(ctx, shared.UUID(uuid.New()), t2, tx)
		return berr
	}); err != nil {
		t.Fatalf("BumpLastProgressAt(bogus): %v", err)
	}
	if found {
		t.Fatalf("BumpLastProgressAt(bogus): found=true, want false")
	}
}
