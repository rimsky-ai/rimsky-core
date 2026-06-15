// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: frame
// @concept: event-log

package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

// TestTraceRetentionReapsWholeTrace seeds one instance with several old
// terminal frames (each with a node_run), one recent terminal frame, one
// in-flight running frame held open by a single parked node_run, plus old
// and recent rows in both event ledgers. It then runs the three retention
// deletes with a count cap of 1 and a cutoff between old and recent, and
// asserts: every old terminal frame row AND its cascade-deleted node_run
// are gone; old audit + named events are gone; the recent terminal frame,
// the parked-held running frame and its node_run, and the recent events
// all survive.
func TestTraceRetentionReapsWholeTrace(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()
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
	// @constraint: rimsky_instances.main_run_scope_id and rimsky_run_scopes.instance_id
	// are mutual FKs — both rows must land in one tx under deferred constraints
	// (mirrors queue_park_test.go's seed).
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
		t.Fatalf("commit: %v", err)
	}

	now := time.Now().UTC()
	// @deliberate: cutoff sits between old rows (well in the past) and recent
	// rows (just now); count cap (1) independently reaps all but the single
	// most-recent terminal frame. Both bounds agree on the old frames — the
	// lesser-of policy at work.
	cutoff := now.Add(-1 * time.Hour)
	oldTime := now.Add(-24 * time.Hour)
	rfc := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

	// @constraint: each seeded frame gets a distinct node so the in-flight
	// partial unique index never bites (it only constrains in-flight phases,
	// but distinct nodes keep the seed unambiguous). Returns (frameID, runID,
	// nodeID) for a completed frame with one terminal (phase=completed,
	// state=failed) node_run hung off it.
	seedTerminalFrame := func(endedAt time.Time) (string, string, string) {
		frameID := uuid.New().String()
		nodeID := uuid.New().String()
		runID := uuid.New().String()
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_frames
			   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
			    queued_at, started_at, ended_at, frame_timeout_ms)
			 VALUES (?, ?, 'serial_queue', 'completed', '[]', ?, ?, ?, 600000)`,
			frameID, instanceID, rfc(endedAt), rfc(endedAt), rfc(endedAt),
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
		return frameID, runID, nodeID
	}

	oldFrame1, oldRun1, _ := seedTerminalFrame(oldTime)
	oldFrame2, oldRun2, _ := seedTerminalFrame(oldTime.Add(time.Minute))
	oldFrame3, oldRun3, _ := seedTerminalFrame(oldTime.Add(2 * time.Minute))
	recentFrame, recentRun, _ := seedTerminalFrame(now.Add(-time.Minute))

	// @deliberate: an in-flight running frame held open by a parked node_run is
	// NOT terminal, so the reaper must never touch it regardless of age — this
	// seed exercises the parked-held exemption case.
	heldFrame := uuid.New().String()
	heldNode := uuid.New().String()
	heldRun := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
		    queued_at, started_at, frame_timeout_ms)
		 VALUES (?, ?, 'serial_queue', 'running', '[]', ?, ?, 600000)`,
		heldFrame, instanceID, rfc(oldTime), rfc(oldTime),
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

	// @constraint: occurred_at / emitted_at are both TEXT RFC3339Nano — the
	// production write paths both go through formatTime — so seed both with
	// the same rfc() formatter the cutoff is compared in.
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
	recentEventID := insertEvent(now.Add(-time.Minute), "work_completed")

	insertNodeEvent := func(when time.Time, name string) int64 {
		res, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_node_events
			   (instance_id, emitter_node_id, event_name, emitted_at)
			 VALUES (?, ?, ?, ?)`,
			instanceID, heldNode, name, rfc(when),
		)
		if err != nil {
			t.Fatalf("seed node_event: %v", err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	oldNodeEventID := insertNodeEvent(oldTime, "progress")
	recentNodeEventID := insertNodeEvent(now.Add(-time.Minute), "progress")

	// @deliberate: drive the three retention deletes the sweep orchestrates —
	// recentFramesKept=1 keeps only the single most-recent terminal frame,
	// cutoff reaps everything older, and the frames path cascades only to
	// node_runs (not to events or named events, which the next two calls reap
	// independently).
	store := d.Tables()
	if _, err := store.Frames().PruneTraceForRetention(ctx, 1, cutoff); err != nil {
		t.Fatalf("PruneTraceForRetention: %v", err)
	}
	if _, err := store.Events().DeleteOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("Events.DeleteOlderThan: %v", err)
	}
	if _, _, err := store.NodeEvents().DeleteOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("NodeEvents.DeleteOlderThan: %v", err)
	}

	frameExists := func(frameID string) bool {
		var n int
		if err := rawDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rimsky_frames WHERE frame_id = ?`, frameID,
		).Scan(&n); err != nil {
			t.Fatalf("count frame: %v", err)
		}
		return n > 0
	}
	runExists := func(runID string) bool {
		var n int
		if err := rawDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rimsky_node_runs WHERE id = ?`, runID,
		).Scan(&n); err != nil {
			t.Fatalf("count run: %v", err)
		}
		return n > 0
	}

	for _, fid := range []string{oldFrame1, oldFrame2, oldFrame3} {
		if frameExists(fid) {
			t.Errorf("old terminal frame %s should be reaped, but survives", fid)
		}
	}
	for _, rid := range []string{oldRun1, oldRun2, oldRun3} {
		if runExists(rid) {
			t.Errorf("old terminal frame's node_run %s should be cascade-deleted, but survives", rid)
		}
	}
	if !frameExists(recentFrame) {
		t.Errorf("recent terminal frame should survive the reap")
	}
	if !runExists(recentRun) {
		t.Errorf("recent terminal frame's node_run should survive the reap")
	}
	if !frameExists(heldFrame) {
		t.Errorf("in-flight parked-held running frame must never be reaped")
	}
	if !runExists(heldRun) {
		t.Errorf("parked node_run of the held frame must never be reaped")
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
	nodeEventExists := func(id int64) bool {
		var n int
		if err := rawDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rimsky_node_events WHERE id = ?`, id,
		).Scan(&n); err != nil {
			t.Fatalf("count node_event: %v", err)
		}
		return n > 0
	}

	if eventExists(oldEventID) {
		t.Errorf("old audit event should be reaped by the trailing window")
	}
	if !eventExists(recentEventID) {
		t.Errorf("recent audit event inside the window must survive")
	}
	if nodeEventExists(oldNodeEventID) {
		t.Errorf("old named event should be reaped by the trailing window")
	}
	if !nodeEventExists(recentNodeEventID) {
		t.Errorf("recent named event inside the window must survive")
	}
}

// TestNodeEventDeleteOlderThanQueuesSpilledBlobOrphans pins that a
// time-reaped named-event row whose payload spilled to a backend yields its
// (handle, backend) for blob-orphan reaping. Without this, a durable
// instance's spilled named-event payloads leak forever: the instance never
// terminates, so the instance-delete cascade (the only other reclamation
// path) never runs, and SweepOrphanedBlobs reaps only what is queued in
// rimsky_blob_orphans. The sweep orchestrator queues the returned orphans;
// here we assert the persistence method surfaces them.
func TestNodeEventDeleteOlderThanQueuesSpilledBlobOrphans(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()
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
		t.Fatalf("commit: %v", err)
	}

	now := time.Now().UTC()
	cutoff := now.Add(-1 * time.Hour)
	oldTime := now.Add(-24 * time.Hour)
	nodeID := uuid.New().String()

	// @deliberate: seed an old spilled named event (handle set) — the reaper
	// must both delete the row AND surface (handle, backend) as an orphan, or
	// the spilled bytes leak forever.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_events
		   (instance_id, emitter_node_id, event_name, payload_handle, payload_handle_backend, emitted_at)
		 VALUES (?, ?, 'progress', 'blob-handle-xyz', 'filesystem', ?)`,
		instanceID, nodeID, oldTime.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("seed spilled node_event: %v", err)
	}

	deleted, orphans, err := d.Tables().NodeEvents().DeleteOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("NodeEvents.DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteOlderThan deleted %d rows, want 1", deleted)
	}
	if len(orphans) != 1 {
		t.Fatalf("DeleteOlderThan returned %d orphans, want 1 (spilled payload handle must be surfaced "+
			"for blob-orphan reaping or the bytes leak)", len(orphans))
	}
	if orphans[0].Handle != "blob-handle-xyz" || orphans[0].Backend != "filesystem" {
		t.Fatalf("orphan = %+v, want {Handle:blob-handle-xyz Backend:filesystem}", orphans[0])
	}
	// @constraint: the handle must be durably queued in rimsky_blob_orphans by
	// the same transaction that deleted the row — surfacing it in the return
	// slice is not enough; SweepOrphanedBlobs reaps only what is persisted
	// there.
	var queued int
	if err := rawDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_blob_orphans WHERE handle = 'blob-handle-xyz'`,
	).Scan(&queued); err != nil {
		t.Fatalf("count blob_orphans: %v", err)
	}
	if queued != 1 {
		t.Fatalf("spilled handle must be persisted in rimsky_blob_orphans by DeleteOlderThan "+
			"(atomic with the row delete), found %d rows", queued)
	}
}

// TestNodeEventTimeRoundTripThroughProductionPaths pins the unified
// RFC3339Nano emitted_at convention end to end through the PRODUCTION
// accessors (not direct-SQL seeds): Insert stamps emitted_at, LatestByName
// reads it back (TEXT→time.Time via parseTime), and DeleteOlderThan compares
// it against a formatTime cutoff. If Insert's write format ever drifts from
// the reaper's compare format, the lexicographic `emitted_at < cutoff`
// comparison breaks and the reap boundary below flips — a regression the
// direct-SQL-seeded gate tests above cannot surface.
func TestNodeEventTimeRoundTripThroughProductionPaths(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)
	tables := d.Tables()

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	scopeID := uuid.New().String()
	emitterNodeID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed: %v", err)
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

	// @deliberate: write through the production Insert so emitted_at is
	// stamped via formatTime — the whole point of this test is to pin that
	// format against the reaper's compare format.
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, ierr := tables.NodeEvents().Insert(ctx, persistence.NodeEvent{
			InstanceID:    instanceID,
			EmitterNodeID: emitterNodeID,
			EventName:     "progress",
		}, tx)
		return ierr
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	latest := func() *persistence.NodeEvent {
		var got *persistence.NodeEvent
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			ev, e := tables.NodeEvents().LatestByName(ctx, instanceID, emitterNodeID, "progress", tx)
			got = ev
			return e
		}); err != nil {
			t.Fatalf("LatestByName: %v", err)
		}
		return got
	}

	ev := latest()
	if ev == nil {
		t.Fatalf("inserted node event must be readable via LatestByName")
	}
	if ev.EmittedAt.IsZero() || time.Since(ev.EmittedAt) > time.Minute {
		t.Fatalf("emitted_at did not round-trip through formatTime/parseTime: got %v", ev.EmittedAt)
	}

	// @constraint: a past cutoff must not reap a just-written row — a non-zero
	// count here means Insert's write format and the reaper's compare format
	// have drifted.
	if n, _, derr := tables.NodeEvents().DeleteOlderThan(ctx, time.Now().Add(-time.Hour)); derr != nil {
		t.Fatalf("DeleteOlderThan(past): %v", derr)
	} else if n != 0 {
		t.Fatalf("past cutoff reaped %d row(s), want 0 — Insert and reaper time formats disagree", n)
	}
	if latest() == nil {
		t.Fatalf("event must survive a past cutoff")
	}

	// @constraint: a future cutoff must reap the row — Insert's stored format
	// and the reaper's formatTime cutoff compare correctly only when both are
	// RFC3339Nano.
	if n, _, derr := tables.NodeEvents().DeleteOlderThan(ctx, time.Now().Add(time.Hour)); derr != nil {
		t.Fatalf("DeleteOlderThan(future): %v", derr)
	} else if n != 1 {
		t.Fatalf("future cutoff reaped %d row(s), want 1 — Insert and reaper time formats disagree", n)
	}
	if latest() != nil {
		t.Fatalf("event must be reaped by a future cutoff")
	}
}
