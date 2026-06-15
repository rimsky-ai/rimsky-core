// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// E10 — retention sweeps wired into the scheduler tick.
//
// SweepLineageRetention / SweepRunTreeRetention were dead code: the tick
// wired every other sweep but not these two, and scheduler.Config.Retention
// was never populated. This e2e drives the real tick against a real
// Postgres-backed driver, seeds stale rimsky_lineage rows and a backlog of
// terminal frames, and asserts the tick reaps them per a
// Retention{LineageTrailing, RecentFramesKept} config — the proof that the
// sweeps are now reachable.
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

// TestRetentionSweepsReapOnTick pins E10: one scheduler.Tick with a
// Retention config set must (a) delete stale rimsky_lineage rows past the
// LineageTrailing cutoff whose run/claim_handle is gone, and (b) prune
// rimsky_node_runs rows belonging to all but the RecentFramesKept
// most-recent terminal frames per instance. Both gate on
// scheduler.Config.Retention, which the production wiring now populates.
func TestRetentionSweepsReapOnTick(t *testing.T) {
	t.Parallel()
	// @constraint: NoScheduler so the harness's own tick loop doesn't race our seeded
	// rows; NoSupervisor so the created instance never spawns real frames /
	// run rows that would pollute the retention assertions. We drive
	// scheduler.Tick synchronously below.
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tplHash := h.DeployTemplate(node.TemplateSpec{
		Name:                "retention-sweep-" + uuid.NewString(),
		Version:             "v1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		FrameTimeoutMs:      node.FrameTimeoutDefaultMs,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	instanceID := h.CreateInstance(tplHash, "", map[string]any{})
	scopeID := h.GetMainRunScopeID(instanceID)

	// @constraint: A node to hang the seeded run rows off (node.frame_id stays NULL;
	// the run rows carry frame_id directly).
	nodeID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_nodes (id, instance_id, node_type, executor)
	           VALUES ($1, $2, 'retention-node', 'worker')`, nodeID, instanceID)

	// @deliberate: Seed terminal frames + run rows
	//
	// Five completed frames per instance with distinct ended_at. With
	// RecentFramesKept=2 (no TraceTrailing here), PruneTraceForRetention
	// keeps the 2 most-recent terminal frame rows and deletes the other 3;
	// the deleted frames' run rows go via the frame→node_run ON DELETE
	// CASCADE, so the surviving frames' runs survive and the pruned
	// frames' runs are removed — the assertions below ride on the run
	// rows, which hold under the frame-row-deleting reaper.
	const totalFrames = 5
	const keepFrames = 2
	survivingRunIDs := make(map[string]bool)
	prunedRunIDs := make(map[string]bool)
	base := time.Now().Add(-24 * time.Hour)
	for i := 0; i < totalFrames; i++ {
		frameID := uuid.New()
		// @deliberate: ended_at increases with i so the highest-i frames are the most
		// recent terminal frames (the survivors).
		endedAt := base.Add(time.Duration(i) * time.Hour)
		h.ExecSQL(`INSERT INTO rimsky_frames
		    (frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
		     queued_at, started_at, ended_at, frame_timeout_ms)
		    VALUES ($1, $2, 'serial_queue', 'completed', ARRAY[$3]::UUID[],
		            $4, $4, $5, 600000)`,
			frameID, instanceID, nodeID, endedAt, endedAt)

		runID := uuid.New()
		h.ExecSQL(`INSERT INTO rimsky_node_runs
		    (id, node_id, executor_name, enqueued_at, phase, state, frame_id, run_scope_id)
		    VALUES ($1, $2, 'worker', $3, 'completed', 'failed', $4, $5)`,
			runID, nodeID, endedAt, frameID, scopeID)

		if i >= totalFrames-keepFrames {
			survivingRunIDs[runID.String()] = true
		} else {
			prunedRunIDs[runID.String()] = true
		}
	}
	require.Len(t, survivingRunIDs, keepFrames)
	require.Len(t, prunedRunIDs, totalFrames-keepFrames)

	// @deliberate: Seed lineage rows
	//
	// Stale rows: observed_at well past the 1h LineageTrailing cutoff, with
	// record run_id/claim_handle_id pointing at rows that don't exist, so
	// the prune predicate (observed_at < cutoff AND run gone AND
	// claim_handle gone) matches them.
	staleFrameID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_frames
	    (frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
	     queued_at, started_at, ended_at, frame_timeout_ms)
	    VALUES ($1, $2, 'serial_queue', 'completed', ARRAY[$3]::UUID[],
	            $4, $4, $4, 600000)`,
		staleFrameID, instanceID, nodeID, base)

	staleLineageIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for _, lid := range staleLineageIDs {
		h.ExecSQL(`INSERT INTO rimsky_lineage
		    (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
		    VALUES ($1, 'leaf_run', $2, $3, $4,
		            jsonb_build_object('run_id', $5::text, 'claim_handle_id', $6::text), '')`,
			lid, instanceID, staleFrameID, base, uuid.NewString(), uuid.NewString())
	}

	// @constraint: Fresh lineage row: observed_at is now, so it's inside the trailing
	// window and must survive.
	freshLineageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_lineage
	    (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
	    VALUES ($1, 'leaf_run', $2, $3, now(),
	            jsonb_build_object('run_id', $4::text, 'claim_handle_id', $5::text), '')`,
		freshLineageID, instanceID, staleFrameID, uuid.NewString(), uuid.NewString())

	// @deliberate: Drive one tick with retention configured
	cfg := scheduler.Config{
		Persist:        h.Persist,
		Queue:          h.Queue,
		AdvisoryLocker: h.Driver.AdvisoryLocker(),
		Clock:          shared.SystemClock{},
		Logger:         shared.SilentLogger{},
		ClaimHandles:   h.Persist.ClaimHandles(),
		Retention: runtime.RetentionConfig{
			LineageTrailing:  time.Hour,
			RecentFramesKept: keepFrames,
		},
	}
	require.NoError(t, scheduler.Tick(h.Ctx, cfg))

	for _, lid := range staleLineageIDs {
		var n int
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_lineage WHERE id = $1`,
			[]any{lid}, &n)
		assert.Equal(t, 0, n, "stale lineage row %s should be reaped", lid)
	}
	var freshN int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_lineage WHERE id = $1`,
		[]any{freshLineageID}, &freshN)
	assert.Equal(t, 1, freshN, "fresh lineage row inside the trailing window must survive")

	for runID := range survivingRunIDs {
		var n int
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_node_runs WHERE id = $1`,
			[]any{runID}, &n)
		assert.Equal(t, 1, n, "run row %s (recent terminal frame) must survive", runID)
	}
	for runID := range prunedRunIDs {
		var n int
		h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_node_runs WHERE id = $1`,
			[]any{runID}, &n)
		assert.Equal(t, 0, n, "run row %s (stale terminal frame) should be pruned", runID)
	}
}
