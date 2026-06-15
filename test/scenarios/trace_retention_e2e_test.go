// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pass-7 acceptance gate for the 2026-06-03 durable-by-default lifecycle
// spec, scenario 4 ("Trace retention"). Proves end-to-end against the real
// runtime that a live durable instance's old trace is reaped under a
// configured retention window while in-flight and recent trace survive.
//
// The retention reaper is the real runtime path: scheduler.Tick →
// runtime.SweepRunTreeRetention → Frames().PruneTraceForRetention
// (frames + cascade node_runs) + Events().DeleteOlderThan (audit log) +
// NodeEvents().DeleteOlderThan (named events), all gated on
// scheduler.Config.Retention {RecentFramesKept, TraceTrailing}. This test
// drives scheduler.Tick once, synchronously, against a real testcontainers
// Postgres.
//
// Harness mode: NoScheduler (so the harness's own background tick loop does
// not race the seeded rows) + NoSupervisor (so the created instance never
// spawns real frames/run rows that would pollute the retention assertions).
// The trace rows are seeded via raw SQL with controlled timestamps — the
// same proven shape as retention_sweep_e2e_test.go — because real frames are
// stamped ended_at = now() and could not be aged into a short reaping
// window without sleeping. The behavior under test is rimsky's real
// retention sweep; the seeded rows are the trace it reaps.
//
// What it asserts:
//   - Old terminal frames AND their node_runs (cascade) are deleted.
//   - Old audit events (rimsky_events) and old named events
//     (rimsky_node_events) are deleted.
//   - The two most-recent terminal frames + their node_runs survive (the
//     count-cap survivors), and recent events survive (within the window).
//   - An in-flight parked-held frame (state='running' with a parked
//     node_run) and its node_run survive — nothing live is ever reaped.
//   - No surviving event references a node that was removed by a frame reap
//     (frame-reaped node_runs do not delete their rimsky_nodes row, and the
//     audit/named events key on instance_id/node_id, never a frame FK, so
//     no event dangles).
package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/scheduler"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTraceRetention_EndToEnd(t *testing.T) {
	t.Parallel()
	// @constraint: NoScheduler so the harness's own tick loop doesn't race our seeded
	// rows; NoSupervisor so the durable instance never spawns real frames /
	// run rows. We drive scheduler.Tick synchronously below.
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tplHash := h.DeployTemplate(node.TemplateSpec{
		Name:           "trace-retention-" + uuid.NewString(),
		Version:        "v1",
		FrameTimeoutMs: node.FrameTimeoutDefaultMs,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	// @deliberate: Durable instance — NO terminate_after_run flag. Trace retention is the
	// mechanism that keeps a long-lived durable instance's trace bounded.
	instanceID := h.CreateInstance(tplHash, "", map[string]any{})
	scopeID := h.GetMainRunScopeID(instanceID)

	// @deliberate: Clear the instance-factory's auto-created root frames so the test
	// drives its own seed exclusively. Under the typed-message schema every root
	// node gets a seeded synthetic message + frame at instance-create; the
	// harness's CreateInstance helper drives frame.RunTick during
	// waitForRootDispatch, so by the time we get here the auto-frame has already
	// advanced to a terminal state and would count toward
	// PruneTraceForRetention's per-instance ranking. node_runs CASCADE via the
	// frame FK; the auto-message row stays behind harmlessly (no FK from
	// messages to anything we touch).
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(instanceID))

	// @deliberate: A node to hang the seeded run/event rows off.
	nodeID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_nodes (id, instance_id, node_type, executor)
	           VALUES ($1, $2, 'retention-node', 'worker')`, nodeID, instanceID)

	// @deliberate: Retention window: reap anything older than 1h. Old rows are seeded at
	// now-24h (well past the window AND past the count cap), recent rows at
	// now-1min (inside the window).
	const window = time.Hour
	const recentKept = 2
	old := time.Now().Add(-24 * time.Hour)
	recent := time.Now().Add(-1 * time.Minute)

	// @deliberate: Seed terminal frames + their node_runs
	// Three old terminal frames (ended_at = now-24h, staggered by minutes so
	// they rank below the recent two) + two recent terminal frames
	// (ended_at ~ now-1min). With RecentFramesKept=2 and a 1h window, the two
	// recent frames survive (rank <= 2 AND inside window) and the three old
	// frames are reaped (rank > 2 AND older than cutoff). Their node_runs go
	// via the frame->node_run ON DELETE CASCADE.
	survivingRunIDs := map[string]bool{}
	survivingFrameIDs := map[string]bool{}
	reapedRunIDs := map[string]bool{}
	reapedFrameIDs := map[string]bool{}

	seedTerminalFrame := func(endedAt time.Time, survives bool) {
		// @constraint: rimsky_frames.triggering_message_id is NOT NULL under the
		// typed-message schema; seed a typed envelope first so the frame's FK
		// resolves.
		messageID := uuid.New()
		h.ExecSQL(`INSERT INTO rimsky_messages
		    (id, instance_id, type, sender, sender_kind)
		    VALUES ($1, $2, 'fixture/trace-retention', 'operator', 'operator')`,
			messageID, instanceID)
		frameID := uuid.New()
		h.ExecSQL(`INSERT INTO rimsky_frames
		    (frame_id, instance_id, triggering_message_id, state,
		     queued_at, started_at, ended_at, frame_timeout_ms)
		    VALUES ($1, $2, $3, 'completed',
		            $4, $4, $5, 600000)`,
			frameID, instanceID, messageID, endedAt, endedAt)
		runID := uuid.New()
		h.ExecSQL(`INSERT INTO rimsky_node_runs
		    (id, node_id, executor_name, enqueued_at, phase, state, frame_id, run_scope_id)
		    VALUES ($1, $2, 'worker', $3, 'completed', 'failed', $4, $5)`,
			runID, nodeID, endedAt, frameID, scopeID)
		if survives {
			survivingFrameIDs[frameID.String()] = true
			survivingRunIDs[runID.String()] = true
		} else {
			reapedFrameIDs[frameID.String()] = true
			reapedRunIDs[runID.String()] = true
		}
	}

	// @deliberate: Three old terminal frames (reaped). Stagger ended_at within the old
	// epoch so ranks are deterministic.
	for i := 0; i < 3; i++ {
		seedTerminalFrame(old.Add(time.Duration(i)*time.Minute), false)
	}
	// @deliberate: Two recent terminal frames (survive — the count-cap survivors, inside
	// the window).
	seedTerminalFrame(recent, true)
	seedTerminalFrame(recent.Add(time.Minute), true)

	require.Len(t, reapedFrameIDs, 3)
	require.Len(t, survivingFrameIDs, recentKept)

	// @deliberate: Seed an in-flight parked-held frame (must survive). Per the
	// frame-end rule a parked node_run holds its frame open, so the frame stays
	// running/held; retention exempts all non-terminal frames, so both the
	// frame and its run survive. We seed a fresh running frame (with its
	// triggering message) directly rather than reusing the instance-factory's
	// auto-frame, which under the typed-message schema has already terminated
	// by the time CreateInstance returns (the harness's waitForRootDispatch
	// drove frame.RunTick to terminal).
	inflightMessageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/trace-retention-inflight', 'operator', 'operator')`,
		inflightMessageID, instanceID)
	inflightFrameID := shared.UUID(uuid.New())
	h.ExecSQL(`INSERT INTO rimsky_frames
	    (frame_id, instance_id, triggering_message_id, state,
	     queued_at, started_at, last_progress_at, frame_timeout_ms)
	    VALUES ($1, $2, $3, 'running', now(), now(), now(), 600000)`,
		uuid.UUID(inflightFrameID), instanceID, inflightMessageID)
	inflightRunID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_node_runs
	    (id, node_id, executor_name, enqueued_at, phase, state, parked_at,
	     parked_reason, frame_id, run_scope_id)
	    VALUES ($1, $2, 'worker', now(), 'parked', 'parked', now(),
	            'await_callback', $3, $4)`,
		inflightRunID, nodeID, inflightFrameID, scopeID)

	// @deliberate: Seed audit events (rimsky_events) — old reaped, recent survive
	oldAuditID := insertAuditEvent(h, instanceID, nodeID, "terminal/success", old)
	recentAuditID := insertAuditEvent(h, instanceID, nodeID, "terminal/success", recent)

	// @deliberate: Seed named events (rimsky_node_events) — old reaped, recent survive
	oldNamedID := insertNamedEvent(h, instanceID, nodeID, "progress", old)
	recentNamedID := insertNamedEvent(h, instanceID, nodeID, "progress", recent)

	// @deliberate: Drive one tick with trace retention configured
	cfg := scheduler.Config{
		Persist:        h.Persist,
		Queue:          h.Queue,
		AdvisoryLocker: h.Driver.AdvisoryLocker(),
		Clock:          shared.SystemClock{},
		Logger:         shared.SilentLogger{},
		ClaimHandles:   h.Persist.ClaimHandles(),
		Retention: runtime.RetentionConfig{
			RecentFramesKept: recentKept,
			TraceTrailing:    window,
		},
	}
	require.NoError(t, scheduler.Tick(h.Ctx, cfg))

	for fid := range reapedFrameIDs {
		assert.Equal(t, 0, countRows(h, `SELECT COUNT(*) FROM rimsky_frames WHERE frame_id = $1`, fid),
			"old terminal frame %s should be reaped", fid)
	}
	for rid := range reapedRunIDs {
		assert.Equal(t, 0, countRows(h, `SELECT COUNT(*) FROM rimsky_node_runs WHERE id = $1`, rid),
			"old terminal frame's node_run %s should be reaped via cascade", rid)
	}
	for fid := range survivingFrameIDs {
		assert.Equal(t, 1, countRows(h, `SELECT COUNT(*) FROM rimsky_frames WHERE frame_id = $1`, fid),
			"recent terminal frame %s (rank<=%d, inside window) must survive", fid, recentKept)
	}
	for rid := range survivingRunIDs {
		assert.Equal(t, 1, countRows(h, `SELECT COUNT(*) FROM rimsky_node_runs WHERE id = $1`, rid),
			"recent terminal frame's node_run %s must survive", rid)
	}

	// @constraint: Assert in-flight parked-held frame survives
	assert.Equal(t, 1, countRows(h, `SELECT COUNT(*) FROM rimsky_frames WHERE frame_id = $1`, inflightFrameID.String()),
		"in-flight parked-held frame must survive (nothing live is reaped)")
	assert.Equal(t, 1, countRows(h, `SELECT COUNT(*) FROM rimsky_node_runs WHERE id = $1`, inflightRunID.String()),
		"in-flight parked-held node_run must survive")

	// @constraint: Assert event-log retention (audit + named)
	assert.Equal(t, 0, countRows(h, `SELECT COUNT(*) FROM rimsky_events WHERE id = $1`, oldAuditID),
		"old audit event should be reaped by the time window")
	assert.Equal(t, 1, countRows(h, `SELECT COUNT(*) FROM rimsky_events WHERE id = $1`, recentAuditID),
		"recent audit event (inside window) must survive")
	assert.Equal(t, 0, countRows(h, `SELECT COUNT(*) FROM rimsky_node_events WHERE id = $1`, oldNamedID),
		"old named event should be reaped by the time window")
	assert.Equal(t, 1, countRows(h, `SELECT COUNT(*) FROM rimsky_node_events WHERE id = $1`, recentNamedID),
		"recent named event (inside window) must survive")

	// @deliberate: Assert no dangling: surviving events reference a live node
	// The frame reap cascades to node_runs only; the rimsky_nodes row
	// survives, so the surviving events' node_id still resolves. Confirm the
	// node row is intact and every surviving event still points at it.
	assert.Equal(t, 1, countRows(h, `SELECT COUNT(*) FROM rimsky_nodes WHERE id = $1`, nodeID.String()),
		"the node row must survive a frame reap (frame->node_run cascade does not delete the node)")
	assert.Equal(t, 0, countRows(h,
		`SELECT COUNT(*) FROM rimsky_events e
		  WHERE e.node_id IS NOT NULL
		    AND NOT EXISTS (SELECT 1 FROM rimsky_nodes n WHERE n.id = e.node_id)`),
		"no surviving audit event may reference a removed node")
}

// insertAuditEvent inserts one rimsky_events row at occurred_at and returns
// its BIGSERIAL id.
func insertAuditEvent(h *scenario.Harness, instanceID, nodeID shared.UUID, kind string, at time.Time) int64 {
	var id int64
	h.QueryRowSQL(`INSERT INTO rimsky_events (instance_id, node_id, kind, payload, occurred_at)
	               VALUES ($1, $2, $3, '{}'::jsonb, $4) RETURNING id`,
		[]any{instanceID, nodeID, kind, at}, &id)
	return id
}

// insertNamedEvent inserts one rimsky_node_events row at emitted_at and
// returns its BIGSERIAL id. emitter_node_id is the node's UUID string (the
// column is TEXT).
func insertNamedEvent(h *scenario.Harness, instanceID, nodeID shared.UUID, name string, at time.Time) int64 {
	var id int64
	h.QueryRowSQL(`INSERT INTO rimsky_node_events
	               (instance_id, emitter_node_id, event_name, payload_inline, emitted_at)
	               VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		[]any{instanceID, nodeID.String(), name, []byte(`{}`), at}, &id)
	return id
}

// countRows runs a COUNT(*) query and returns the scalar.
func countRows(h *scenario.Harness, sql string, args ...any) int {
	var n int
	h.QueryRowSQL(sql, args, &n)
	return n
}
