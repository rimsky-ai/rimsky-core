// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: frame
// @concept: event-log

package sqlite_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

// traceFixture carries the row ids the retention sweep keys on:
// `nOld` terminal frames + their runs (reaped), one recent terminal
// frame + run (survives), one in-flight held frame + parked run
// (never reaped), and old + recent audit-event ids.
type traceFixture struct {
	instanceID  string
	scopeID     string
	oldFrames   []string
	oldRuns     []string
	recentFrame string
	recentRun   string
	heldFrame   string
	heldRun     string
	oldEventID  int64
	newEventID  int64
	oldTime     time.Time
	recentTime  time.Time
	cutoff      time.Time
}

func seedTraceFixture(t *testing.T, ctx context.Context, d persistence.Database, nOld int) traceFixture {
	t.Helper()
	rawDB := sqlitedrv.DBFromDatabase(d)

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	scopeID := uuid.New().String()

	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, main_run_scope_id) VALUES (?, ?, ?)`,
		instanceID, templateID, scopeID,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
		scopeID, instanceID,
	); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := stx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}

	msgID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID,
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	now := time.Now().UTC()
	oldTime := now.Add(-24 * time.Hour)
	cutoff := now.Add(-1 * time.Hour)
	recentTime := now.Add(-time.Minute)
	rfc := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

	seedTerminalFrame := func(endedAt time.Time) (string, string) {
		frameID := uuid.New().String()
		nodeID := uuid.New().String()
		runID := uuid.New().String()
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_frames
			   (frame_id, instance_id, triggering_message_id, state,
			    queued_at, started_at, ended_at, frame_timeout_ms)
			 VALUES (?, ?, ?, 'completed', ?, ?, ?, 600000)`,
			frameID, instanceID, msgID, rfc(endedAt), rfc(endedAt), rfc(endedAt),
		); err != nil {
			t.Fatalf("seed terminal frame: %v", err)
		}
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
			 VALUES (?, ?, 'fixture', ?)`,
			nodeID, instanceID, frameID,
		); err != nil {
			t.Fatalf("seed node: %v", err)
		}
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_node_runs
			   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
			 VALUES (?, ?, 'stub', '[]', ?, 'completed', 'failed', ?, ?)`,
			runID, nodeID, rfc(endedAt), frameID, scopeID,
		); err != nil {
			t.Fatalf("seed terminal node_run: %v", err)
		}
		return frameID, runID
	}

	oldFrames := make([]string, 0, nOld)
	oldRuns := make([]string, 0, nOld)
	for i := 0; i < nOld; i++ {
		f, r := seedTerminalFrame(oldTime.Add(time.Duration(i) * time.Minute))
		oldFrames = append(oldFrames, f)
		oldRuns = append(oldRuns, r)
	}
	recentFrame, recentRun := seedTerminalFrame(recentTime)

	// @constraint: in-flight running frame held open by a parked
	// node_run — retention must never reap a non-terminal frame
	// regardless of age.
	heldFrame := uuid.New().String()
	heldNode := uuid.New().String()
	heldRun := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, state,
		    queued_at, started_at, frame_timeout_ms)
		 VALUES (?, ?, ?, 'running', ?, ?, 600000)`,
		heldFrame, instanceID, msgID, rfc(oldTime), rfc(oldTime),
	); err != nil {
		t.Fatalf("seed held frame: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
		 VALUES (?, ?, 'fixture', ?)`,
		heldNode, instanceID, heldFrame,
	); err != nil {
		t.Fatalf("seed held node: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		 VALUES (?, ?, 'stub', '[]', ?, 'parked', 'parked', ?, ?)`,
		heldRun, heldNode, rfc(oldTime), heldFrame, scopeID,
	); err != nil {
		t.Fatalf("seed parked node_run: %v", err)
	}

	insertEvent := func(when time.Time, kind string) int64 {
		res, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_events (instance_id, kind, payload, occurred_at)
			 VALUES (?, ?, '{}', ?)`,
			instanceID, kind, rfc(when),
		)
		if err != nil {
			t.Fatalf("seed event: %v", err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	oldEventID := insertEvent(oldTime, "work_started")
	newEventID := insertEvent(recentTime, "work_completed")

	return traceFixture{
		instanceID:  instanceID,
		scopeID:     scopeID,
		oldFrames:   oldFrames,
		oldRuns:     oldRuns,
		recentFrame: recentFrame,
		recentRun:   recentRun,
		heldFrame:   heldFrame,
		heldRun:     heldRun,
		oldEventID:  oldEventID,
		newEventID:  newEventID,
		oldTime:     oldTime,
		recentTime:  recentTime,
		cutoff:      cutoff,
	}
}

// TestSQLite_FrameRetention_PrunesOldTerminalFramesAndCascadesNodeRuns
// pins the post-coherence frame retention path: terminal frames older
// than the cutoff (and not in the most-recent kept window) are reaped
// alongside their node_runs via the rimsky_node_runs.frame_id ON
// DELETE CASCADE. The held in-flight running frame and its parked
// node_run survive regardless of age (the reaper keys on phase).
//
// @concept: frame
func TestSQLite_FrameRetention_PrunesOldTerminalFramesAndCascadesNodeRuns(t *testing.T) {
	ctx := context.Background()
	d := openSQLite(t)
	rawDB := sqlitedrv.DBFromDatabase(d)
	f := seedTraceFixture(t, ctx, d, 3)

	store := d.Tables()
	if _, err := store.Frames().PruneTraceForRetention(ctx, 1, f.cutoff); err != nil {
		t.Fatalf("PruneTraceForRetention: %v", err)
	}

	frameExists := func(id string) bool {
		var n int
		if err := rawDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rimsky_frames WHERE frame_id = ?`, id,
		).Scan(&n); err != nil {
			t.Fatalf("count frame: %v", err)
		}
		return n > 0
	}
	runExists := func(id string) bool {
		var n int
		if err := rawDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rimsky_node_runs WHERE id = ?`, id,
		).Scan(&n); err != nil {
			t.Fatalf("count run: %v", err)
		}
		return n > 0
	}

	for _, fid := range f.oldFrames {
		if frameExists(fid) {
			t.Errorf("old terminal frame %s should be reaped, but survives", fid)
		}
	}
	for _, rid := range f.oldRuns {
		if runExists(rid) {
			t.Errorf("old terminal node_run %s should be cascade-deleted with its frame, but survives", rid)
		}
	}
	if !frameExists(f.recentFrame) {
		t.Errorf("recent terminal frame must survive the reap")
	}
	if !runExists(f.recentRun) {
		t.Errorf("recent terminal frame's node_run must survive")
	}
	if !frameExists(f.heldFrame) {
		t.Errorf("in-flight parked-held running frame must NEVER be reaped")
	}
	if !runExists(f.heldRun) {
		t.Errorf("parked node_run of the held frame must NEVER be reaped")
	}
}

// TestSQLite_EventRetention_DeleteOlderThan pins the audit-event
// retention path: rows older than the cutoff are reaped, recent rows
// inside the trailing window survive. The audit ledger is time-keyed
// (no frame FK), so the trailing window alone bounds it.
//
// @concept: event-log
func TestSQLite_EventRetention_DeleteOlderThan(t *testing.T) {
	ctx := context.Background()
	d := openSQLite(t)
	rawDB := sqlitedrv.DBFromDatabase(d)
	f := seedTraceFixture(t, ctx, d, 2)

	store := d.Tables()
	deleted, err := store.Events().DeleteOlderThan(ctx, f.cutoff)
	if err != nil {
		t.Fatalf("Events.DeleteOlderThan: %v", err)
	}
	if deleted < 1 {
		t.Errorf("DeleteOlderThan reported %d, want at least 1 (the old event)", deleted)
	}

	eventExists := func(id int64) bool {
		var n int
		if err := rawDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rimsky_events WHERE id = ?`, id,
		).Scan(&n); err != nil {
			t.Fatalf("count event: %v", err)
		}
		return n > 0
	}
	if eventExists(f.oldEventID) {
		t.Errorf("old audit event should be reaped by the trailing window")
	}
	if !eventExists(f.newEventID) {
		t.Errorf("recent audit event inside the window must survive")
	}
}

// TestSQLite_EventTimeRoundTripThroughProductionPaths pins the unified
// RFC3339Nano emitted_at convention end-to-end through the production
// accessors: Insert via the Events table stamps occurred_at, and
// DeleteOlderThan compares it against a formatTime cutoff. If the
// Insert write format ever drifts from the reaper's compare format,
// the lexicographic `occurred_at < cutoff` comparison breaks and the
// reap boundary flips.
//
// @concept: event-log
func TestSQLite_EventTimeRoundTripThroughProductionPaths(t *testing.T) {
	ctx := context.Background()
	d := openSQLite(t)
	rawDB := sqlitedrv.DBFromDatabase(d)
	f := seedTraceFixture(t, ctx, d, 0)
	_ = f
	store := d.Tables()

	rawCount := func() int {
		var n int
		if err := rawDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rimsky_events WHERE instance_id = ?`, f.instanceID,
		).Scan(&n); err != nil {
			t.Fatalf("count events: %v", err)
		}
		return n
	}

	before := rawCount()
	if before < 2 {
		t.Fatalf("seedTraceFixture must seed at least 2 events; got %d", before)
	}

	// @constraint: a past cutoff (well before old event's occurred_at)
	// must not reap rows — both events were seeded after this point.
	n, err := store.Events().DeleteOlderThan(ctx, f.oldTime.Add(-time.Hour))
	if err != nil {
		t.Fatalf("DeleteOlderThan(past): %v", err)
	}
	if n != 0 {
		t.Fatalf("past cutoff reaped %d row(s), want 0 — Insert and reaper time formats disagree", n)
	}
	if rawCount() != before {
		t.Fatalf("rows reaped by a too-early cutoff")
	}

	// @constraint: a future cutoff reaps everything — the lexicographic
	// comparison succeeds only when both Insert and reaper write
	// RFC3339Nano.
	n, err = store.Events().DeleteOlderThan(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("DeleteOlderThan(future): %v", err)
	}
	if n != before {
		t.Fatalf("future cutoff reaped %d row(s), want %d — Insert and reaper time formats disagree", n, before)
	}
	if rawCount() != 0 {
		t.Fatalf("rows survived a future cutoff: events table not empty")
	}
}

// TestSQLite_RetentionSweepRespectsWriterSerializationUnderContention
// pins that the retention sweep does not deadlock when run alongside
// other writer-slot traffic (the executor's keepalive bumps). Both
// paths hit the BEGIN IMMEDIATE writer slot at the FILE level
// (busy_timeout=5000ms); the sweep must yield cleanly without livelock.
//
// @concept: frame
// @concept: orphan-reaper
func TestSQLite_RetentionSweepRespectsWriterSerializationUnderContention(t *testing.T) {
	ctx := context.Background()
	d := openSQLite(t)
	f := seedTraceFixture(t, ctx, d, 5)
	store := d.Tables()

	runID, _ := seedDispatchInstance(t, ctx, d)
	stop := make(chan struct{})
	bumperReady := make(chan struct{})
	var wg sync.WaitGroup
	var bumpsCompleted atomic.Int64
	// @deliberate: keepalive-like background writer producing constant
	// BEGIN IMMEDIATE traffic against the file. Signals bumperReady
	// after the first successful bump so the sweep does not race the
	// goroutine startup.
	wg.Add(1)
	go func() {
		defer wg.Done()
		first := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				_, berr := d.Queue().BumpLastProgressAt(ctx, tx, runID, time.Now())
				return berr
			})
			if err != nil {
				t.Errorf("background bumper: %v", err)
				return
			}
			bumpsCompleted.Add(1)
			if first {
				first = false
				close(bumperReady)
			}
		}
	}()
	<-bumperReady

	// @constraint: run the sweeps multiple times while the writer is
	// busy — under wide-pool every iteration must complete.
	sweepDeadline := time.Now().Add(30 * time.Second)
	iters := 0
	for time.Now().Before(sweepDeadline) && iters < 10 {
		if _, err := store.Frames().PruneTraceForRetention(ctx, 1, f.cutoff); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("PruneTraceForRetention under contention: %v", err)
		}
		if _, err := store.Events().DeleteOlderThan(ctx, f.cutoff); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("Events.DeleteOlderThan under contention: %v", err)
		}
		iters++
	}
	close(stop)
	wg.Wait()
	if iters == 0 {
		t.Fatalf("retention sweep never completed under contention (deadlock or livelock)")
	}
	if bumpsCompleted.Load() == 0 {
		t.Fatalf("background bumper never made progress alongside the sweep")
	}
}
