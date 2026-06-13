// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// park_resume.go — ParkResume conformance area.
//
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
//     phase='parked'), wake_reason persisted, the row re-enters the
//     dispatch-candidate pool, and the park metadata survives the
//     transition so E4 can build ResumeContext.
//   - LoadResumeMetadataInTx / ClearResumeMetadataInTx: the metadata
//     round-trip and the post-dispatch clear (a re-park cycle starts
//     clean).
//
// SQLite stores the park timestamps as RFC3339 text while postgres
// uses timestamptz + INTERVAL arithmetic in the overdue predicate —
// driver-specific idioms with drift risk, hence identical assertions
// on both drivers.
package conformance

import (
	"bytes"
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

	// Three parked runs:
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
	// "await_callback" is the second value of the closed ParkReason set
	// (postgres enforces it with a storage CHECK; sqlite constrains at
	// the application layer only).
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runC, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: now.Add(-1 * time.Hour), ResumeAt: now.Add(24 * time.Hour),
		Reason: "await_callback",
	})

	// Cutoff between A and B: only A is ready.
	ready, err := q.ListParkedReadyForResume(ctx, now.Add(-90*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(mid cutoff): %v", err)
	}
	if len(ready) != 1 || ready[0].DispatchID != runA {
		t.Fatalf("mid-cutoff ready set = %+v, want exactly [runA=%s]", ready, runA)
	}

	// Cutoff now: A then B, resume_at ascending; C (future resume_at)
	// is excluded.
	ready, err = q.ListParkedReadyForResume(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(now): %v", err)
	}
	if len(ready) != 2 || ready[0].DispatchID != runA || ready[1].DispatchID != runB {
		t.Fatalf("ready set = %+v, want [runA=%s, runB=%s] ascending", ready, runA, runB)
	}

	// Limit caps the batch from the front of the ordering.
	ready, err = q.ListParkedReadyForResume(ctx, now, 1)
	if err != nil {
		t.Fatalf("ListParkedReadyForResume(limit 1): %v", err)
	}
	if len(ready) != 1 || ready[0].DispatchID != runA {
		t.Fatalf("limit-1 ready set = %+v, want [runA=%s]", ready, runA)
	}

	// Watchdog: only C is overdue — A/B have no max_park_duration and
	// their resume_at is already due (the deadline path owns them, not
	// the watchdog).
	overdue, err := q.ListParkedOverdue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListParkedOverdue: %v", err)
	}
	if len(overdue) != 1 || overdue[0].DispatchID != runC {
		t.Fatalf("overdue set = %+v, want exactly [runC=%s]", overdue, runC)
	}

	// CountParkedByReason groups the live parked rows by typed reason.
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
	// runC stays active (claimed, not parked): it must never surface.
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
			// Scope to the test's own instance — the suite shares one
			// database per subtest.
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

	// Unfiltered: both parked rows, parked_at ascending (A first), with
	// the joined instance id and the park metadata populated.
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
	// resume_at survives the projection (drivers store timestamps at
	// different precisions; compare with a 1s tolerance).
	if diff := a.ResumeAt.Sub(resumeA); diff < -time.Second || diff > time.Second {
		t.Fatalf("diagnostic resume_at = %v drifted from %v", a.ResumeAt, resumeA)
	}

	// Typed-reason filter narrows to the matching rows only.
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

	// No parked runs: nothing held.
	runA := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)
	nodeB := seedExtraNode(ctx, t, d, fix, "held-node-b")
	runB := seedClaimedRunForNode(ctx, t, d, fix, nodeB, parkResumeSup)
	if got := countHeld(); got != 0 {
		t.Fatalf("CountHeldFrames = %d with no parked runs, want 0", got)
	}

	// One parked run holds the frame.
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runA, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "snooze",
	})
	if got := countHeld(); got != 1 {
		t.Fatalf("CountHeldFrames = %d with one parked run, want 1", got)
	}

	// A second parked run in the SAME frame: still one held frame —
	// the gauge counts frames, not parked rows.
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID: runB, ExpectedClaimedBy: parkResumeSup,
		ParkedAt: time.Now(), ResumeAt: time.Now().Add(1 * time.Hour),
		Reason: "await_callback",
	})
	if got := countHeld(); got != 1 {
		t.Fatalf("CountHeldFrames = %d with two parked runs in one frame, want 1", got)
	}

	// Both resumed: the hold releases.
	for _, runID := range []shared.UUID{runA, runB} {
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := q.ResumeParkedInTx(ctx, tx, runID, "deadline_elapsed")
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
// transition: exactly-once resume, wake_reason persistence, metadata
// survival for ResumeContext, re-candidacy of the resumed row, and the
// post-dispatch metadata clear.
func testParkResumeMetadataRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	now := time.Now()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, parkResumeSup)
	payload := []byte(`{"resume":"state"}`)
	parkedAt := now.Add(-30 * time.Minute)
	parkRun(ctx, t, d, persistence.ParkActiveInput{
		DispatchID:        runID,
		ExpectedClaimedBy: parkResumeSup,
		ParkedAt:          parkedAt,
		ResumeAt:          now.Add(-1 * time.Minute),
		Reason:            "snooze",
		ReasonNote:        "free-form note",
		SessionToken:      "session-token-1",
		PayloadInline:     payload,
	})

	// The parked row is visible with its metadata.
	parked, err := q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID)
	if err != nil {
		t.Fatalf("GetParkedByNode: %v", err)
	}
	if parked == nil || parked.DispatchID != runID {
		t.Fatalf("GetParkedByNode = %+v, want run %s", parked, runID)
	}
	if parked.Reason != "snooze" || parked.SessionToken != "session-token-1" ||
		!bytes.Equal(parked.PayloadInline, payload) {
		t.Fatalf("parked metadata mismatch: %+v", parked)
	}

	// Resume exactly once: first wake succeeds, second is a no-op
	// (gated on phase='parked' — the exactly-once property the E3
	// sweep and the G3 invalidate handler both rely on when racing).
	resume := func(wakeReason string) bool {
		var resumed bool
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			resumed, err = q.ResumeParkedInTx(ctx, tx, runID, wakeReason)
			return err
		}); err != nil {
			t.Fatalf("ResumeParkedInTx: %v", err)
		}
		return resumed
	}
	if !resume("deadline_elapsed") {
		t.Fatalf("first ResumeParkedInTx did not resume the parked row")
	}
	if resume("deadline_elapsed") {
		t.Fatalf("second ResumeParkedInTx resumed an already-pending row")
	}

	// The row left the parked set…
	parked, err = q.GetParkedByNode(ctx, fix.NodeID, fix.MainRunScopeID)
	if err != nil {
		t.Fatalf("GetParkedByNode after resume: %v", err)
	}
	if parked != nil {
		t.Fatalf("row still parked after resume: %+v", parked)
	}

	// …and re-entered the dispatch-candidate pool unclaimed (the same
	// dispatch id — resume does not allocate a new run row).
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

	// The park metadata survived the parked → pending transition so E4
	// can build ResumeContext, and wake_reason rode along.
	var meta *persistence.ResumeMetadataRow
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		meta, err = q.LoadResumeMetadataInTx(ctx, tx, runID)
		return err
	}); err != nil {
		t.Fatalf("LoadResumeMetadataInTx: %v", err)
	}
	if meta == nil {
		t.Fatalf("LoadResumeMetadataInTx returned nil after resume")
	}
	if meta.Reason != "snooze" || meta.ReasonNote != "free-form note" ||
		meta.SessionToken != "session-token-1" || !bytes.Equal(meta.PayloadInline, payload) {
		t.Fatalf("resume metadata mismatch: %+v", meta)
	}
	if meta.WakeReason != "deadline_elapsed" {
		t.Fatalf("wake_reason = %q, want deadline_elapsed", meta.WakeReason)
	}
	// parked_at is preserved across the transition (drivers store it at
	// different precisions; compare with a 1s tolerance).
	if diff := meta.ParkedAt.Sub(parkedAt); diff < -time.Second || diff > time.Second {
		t.Fatalf("parked_at = %v drifted from %v", meta.ParkedAt, parkedAt)
	}

	// After a successful resume dispatch the runner clears the metadata
	// — a re-park cycle starts clean (LoadResumeMetadataInTx's nil
	// contract distinguishes fresh dispatch from resume).
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.ClearResumeMetadataInTx(ctx, tx, runID)
	}); err != nil {
		t.Fatalf("ClearResumeMetadataInTx: %v", err)
	}
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		meta, err = q.LoadResumeMetadataInTx(ctx, tx, runID)
		return err
	}); err != nil {
		t.Fatalf("LoadResumeMetadataInTx after clear: %v", err)
	}
	if meta != nil {
		t.Fatalf("metadata survived ClearResumeMetadataInTx: %+v", meta)
	}
}
