// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: ParkResume conformance area.
// Pins the full park → resume lifecycle the supervisor's E1/E3/E4
// paths drive (claimant-guard coverage for ParkActiveInTx lives in
// claimant_guard.go; this area covers the lifecycle semantics):
//
//   - ListParkedReadyForResume cutoff selection + resume_at-ascending
//     ordering (the E3 sweep's wake-selection query).
//   - ListParkedOverdue: only parked rows with an elapsed
//     parked_at + max_park_duration_seconds AND a not-yet-due resume_at
//     are watchdog candidates (the park-timeout failure path).
//   - ResumeParkedInTx: parked → pending exactly once (gated on
//     phase='parked'), the row re-enters the dispatch-candidate pool,
//     and the park metadata (reason, reason_note) survives the
//     transition. Post-rewrite there is no separate ResumeContext
//     channel and no inline park payload — resume state rides
//     attribute carry-forward (concept:parked-state).
//
// SQLite stores the park timestamps as RFC3339 text while postgres
// uses timestamptz + INTERVAL arithmetic in the overdue predicate —
// driver-specific idioms with drift risk, hence identical assertions
// on both drivers.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// seedClaimedRunForNode enqueues + claims a node-run for an arbitrary
// node so park transitions (which require phase='active' under a
// claimant) can be exercised.
//
// @source: lib/foundation/persistence/conformance/claimant_guard.go:seedClaimedGuardRun
// @diverged: true
// @reason: takes the node id as a parameter so one test can hold
// several concurrently-parked runs (the in-flight unique index allows
// only one in-flight run per (node, run scope)).
func seedClaimedRunForNode(
	ctx context.Context, t *testing.T, d persistence.Database,
	fix fixtureSet, nodeID shared.UUID, supID string,
) shared.UUID {
	t.Helper()
	q := d.Queue()
	var dispatchID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         nodeID,
			ExecutorName:   "test-executor",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-1 * time.Second),
			FrameID:        fix.FrameID,
			RunScopeID:     fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             32,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID != nodeID {
				continue
			}
			ok, err := q.ClaimDispatchRow(ctx, tx, c.DispatchID, supID)
			if err != nil {
				return err
			}
			if !ok {
				t.Fatalf("seedClaimedRunForNode: claim was not successful")
			}
			dispatchID = c.DispatchID
			return nil
		}
		t.Fatalf("seedClaimedRunForNode: candidate not surfaced for node %s", nodeID)
		return nil
	}); err != nil {
		t.Fatalf("seedClaimedRunForNode: %v", err)
	}
	return dispatchID
}

// seedExtraNode creates an additional node row in the fixture instance
// so a test can hold several in-flight runs at once.
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

// parkRun parks an active claimed run with the supplied metadata in its
// own tx, failing the test on error.
func parkRun(ctx context.Context, t *testing.T, d persistence.Database, in persistence.ParkActiveInput) {
	t.Helper()
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Queue().ParkActiveInTx(ctx, tx, in)
	}); err != nil {
		t.Fatalf("ParkActiveInTx(%s): %v", in.DispatchID, err)
	}
}

const parkResumeSup = "park-resume-supervisor"

// testParkResumeSweepSelection covers ListParkedReadyForResume (cutoff
// + ordering) and ListParkedOverdue (the watchdog predicate).
func testParkResumeSweepSelection(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	now := time.Now()

	// @constraint: Three parked runs cover ListParkedReadyForResume
	// ordering and ListParkedOverdue selection:
	//   A: resume_at 2h overdue   → ready (first in ascending order)
	//   B: resume_at 1h overdue   → ready (second)
	//   C: resume_at 24h in the future, parked 1h ago with a 60s
	//      max_park_duration → NOT ready, but overdue for the watchdog.
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
		DispatchID: runA, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-3 * time.Hour), ResumeAt: now.Add(-2 * time.Hour),
		Reason: "snooze",
	})
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-3 * time.Hour), ResumeAt: now.Add(-1 * time.Hour),
		Reason: "snooze",
	})
	// @constraint: "await_callback" is the second value of the closed
	// ParkReason set (postgres enforces it with a storage CHECK; sqlite
	// constrains at the application layer only).
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runC, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-1 * time.Hour), ResumeAt: now.Add(24 * time.Hour),
		Reason: "await_callback",
	})

	// @constraint: cutoff between A and B's resume_at — only A is ready.
	ready, err := q.ListParkedReadyForResume(ctx, now.Add(-90*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(mid cutoff): %v", err)
	}
	if len(ready) != 1 || ready[0].DispatchID != runA {
		t.Fatalf("mid-cutoff ready set = %+v, want exactly [runA=%s]", ready, runA)
	}

	// @constraint: cutoff=now surfaces A then B in resume_at-ascending
	// order; C (future resume_at) is excluded.
	ready, err = q.ListParkedReadyForResume(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(now): %v", err)
	}
	if len(ready) != 2 || ready[0].DispatchID != runA || ready[1].DispatchID != runB {
		t.Fatalf("ready set = %+v, want [runA=%s, runB=%s] ascending", ready, runA, runB)
	}

	// @constraint: limit caps the batch from the front of the ordering.
	ready, err = q.ListParkedReadyForResume(ctx, now, 1)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(limit 1): %v", err)
	}
	if len(ready) != 1 || ready[0].DispatchID != runA {
		t.Fatalf("limit-1 ready set = %+v, want [runA=%s]", ready, runA)
	}

	// @constraint: watchdog returns only C — A/B have no
	// max_park_duration and their resume_at is already due (the deadline
	// path owns them, not the watchdog).
	overdue, err := q.ListParkedOverdue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListParkedOverdue: %v", err)
	}
	if len(overdue) != 1 || overdue[0].DispatchID != runC {
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

// testParkResumeParkedDiagnostic covers ListParkedDiagnostic — the
// admin diagnostics read (runtime/sweep_parked.go's held-frame /
// parked-node endpoints): parked-only selection, parked_at-ascending
// ordering, the rimsky_nodes JOIN populating instance_id, and the
// typed-reason filter.
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
	// @constraint: runC stays active (claimed, not parked) — it must
	// never surface in ListParkedDiagnostic.
	_ = seedClaimedRunForNode(ctx, t, d, fix, nodeC, parkResumeSup)

	resumeA := now.Add(2 * time.Hour)
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runA, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-3 * time.Hour), ResumeAt: resumeA,
		Reason: "snooze", ReasonNote: "diag note A",
	})
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runB, ExpectedClaimedBy: parkResumeSup,
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
			// @constraint: scope to the test's own instance — the suite
			// shares one database per subtest.
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

	// @constraint: unfiltered call returns both parked rows in
	// parked_at-ascending order (A first), with the joined instance id
	// and the park metadata populated.
	got := listDiag("")
	if len(got) != 2 || got[0].DispatchID != runA || got[1].DispatchID != runB {
		t.Fatalf("diagnostic set = %+v, want [runA=%s, runB=%s] parked_at-ascending", got, runA, runB)
	}
	a := got[0]
	if a.NodeID != nodeA.String() || a.FrameID != fix.FrameID.String() {
		t.Fatalf("diagnostic row anchors = node %q frame %q, want %s / %s", a.NodeID, a.FrameID, nodeA, fix.FrameID)
	}
	if a.Reason != "snooze" || a.ReasonNote != "diag note A" {
		t.Fatalf("diagnostic row metadata = %+v, want reason=snooze note=\"diag note A\"", a)
	}
	// @constraint: resume_at survives the projection (drivers store
	// timestamps at different precisions; compare with a 1s tolerance).
	if diff := a.ResumeAt.Sub(resumeA); diff < -time.Second || diff > time.Second {
		t.Fatalf("diagnostic resume_at = %v drifted from %v", a.ResumeAt, resumeA)
	}

	if got := listDiag("snooze"); len(got) != 1 || got[0].DispatchID != runA {
		t.Fatalf("snooze-filtered set = %+v, want exactly [runA=%s]", got, runA)
	}
	if got := listDiag("await_callback"); len(got) != 1 || got[0].DispatchID != runB {
		t.Fatalf("await_callback-filtered set = %+v, want exactly [runB=%s]", got, runB)
	}
	if got := listDiag("no_such_reason"); len(got) != 0 {
		t.Fatalf("unknown-reason filter returned rows: %+v", got)
	}
}

// testParkResumeHeldFrameCount covers Frames.CountHeldFrames — the
// `rimsky_held_frames` metrics gauge: running frames holding at least
// one PARKED run, counted per frame (not per parked run).
func testParkResumeHeldFrameCount(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()

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
		DispatchID: runA, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "snooze",
	})
	if got := countHeld(); got != 1 {
		t.Fatalf("CountHeldFrames = %d with one parked run, want 1", got)
	}

	// @constraint: a second parked run in the SAME frame still counts
	// as one held frame — the gauge counts frames, not parked rows.
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "await_callback",
	})
	if got := countHeld(); got != 1 {
		t.Fatalf("CountHeldFrames = %d with two parked runs in one frame, want 1", got)
	}

	for _, runID := range []shared.UUID{runA, runB} {
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := q.ResumeParkedInTx(ctx, tx, runID)
			return err
		}); err != nil {
			t.Fatalf("ResumeParkedInTx(%s): %v", runID, err)
		}
	}
	if got := countHeld(); got != 0 {
		t.Fatalf("CountHeldFrames = %d after both resumes, want 0", got)
	}
}

// testParkResumeMetadataRoundTrip covers the parked → pending
// transition: exactly-once resume, re-candidacy of the resumed row,
// and survival of the park reason metadata on the row across the
// transition. Resume state (session_token, executor-scratch) rides
// attribute carry-forward and is exercised separately under
// run_state_writes_isolated_by_scope.go.
func testParkResumeMetadataRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	now := time.Now()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)
	parkedAt := now.Add(-30 * time.Minute)
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID:        runID,
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
	if parked == nil || parked.DispatchID != runID {
		t.Fatalf("GetParkedByNode = %+v, want run %s", parked, runID)
	}
	if parked.Reason != "snooze" || parked.ReasonNote != "free-form note" {
		t.Fatalf("parked metadata mismatch: %+v", parked)
	}

	// @constraint: resume exactly once — first wake succeeds, second is
	// a no-op (gated on phase='parked' — the exactly-once property the
	// E3 sweep and the G3 invalidate handler both rely on when racing).
	resume := func() bool {
		var resumed bool
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			resumed, err = q.ResumeParkedInTx(ctx, tx, runID)
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
		t.Fatalf("second ResumeParkedInTx resumed an already-pending row")
	}

	parked, err = q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID)
	if err != nil {
		t.Fatalf("GetParkedByNode after resume: %v", err)
	}
	if parked != nil {
		t.Fatalf("row still parked after resume: %+v", parked)
	}

	// @constraint: resumed row re-enters the dispatch-candidate pool
	// unclaimed under the same dispatch id — resume does not allocate a
	// new run row.
	owner, err := q.GetClaimedBy(ctx, runID)
	if err != nil {
		t.Fatalf("GetClaimedBy after resume: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("resumed row ownership = %s/%s, want unclaimed", owner.Kind, owner.SupervisorID)
	}
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             32,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.DispatchID == runID {
				return nil
			}
		}
		t.Fatalf("resumed row %s not surfaced as a dispatch candidate", runID)
		return nil
	}); err != nil {
		t.Fatalf("SelectCandidates after resume: %v", err)
	}
}

// testRegisterAsyncAckRoundTrip covers RegisterAsyncAck +
// LookupRunByAsyncAckID — the persistent callback registry the
// supervisor uses to route POST /v1/callback/{ack_id} after an
// AwaitAsyncCallback handoff. Asserts registration uniqueness, durable
// lookup, and that ack registration also bumps last_progress_at so the
// quiet-period sweep treats fresh registration as recent activity.
func testRegisterAsyncAckRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)
	ackID := "ack-" + uuid.New().String()
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RegisterAsyncAck(ctx, tx, runID, ackID, now, nil, nil)
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
		return nil
	}); err != nil {
		t.Fatalf("LookupRunByAsyncAckID round-trip: %v", err)
	}

	// @constraint: unknown ackID resolves to (nil, nil), not an error.
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

// testBumpLastProgressAt covers the per-dispatch liveness timestamp.
// The §12.5 attribute writeback handler and the explicit keepalive
// endpoint both call BumpLastProgressAt to push the quiet-period sweep
// out — assert the timestamp advances monotonically across calls.
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

	// @constraint: bogus runID returns found=false with no error so the
	// POST /v1/runs/{id}/keepalive handler can distinguish 404 from 500.
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
