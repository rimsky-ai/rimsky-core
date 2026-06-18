// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestRetentionSweepsReapOnTick(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tplHash := h.DeployTemplate(node.TemplateSpec{
		Name:           "retention-sweep-" + uuid.NewString(),
		Version:        "v1",
		FrameTimeoutMs: node.FrameTimeoutDefaultMs,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	instanceID := h.CreateInstance(tplHash, "", map[string]any{})
	scopeID := h.GetMainRunScopeID(instanceID)

	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(instanceID))

	nodeID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_nodes (id, instance_id, node_type, executor)
	           VALUES ($1, $2, 'retention-node', 'worker')`, nodeID, instanceID)

	const totalFrames = 5
	const keepFrames = 2
	survivingRunIDs := make(map[string]bool)
	prunedRunIDs := make(map[string]bool)
	base := time.Now().Add(-24 * time.Hour)
	for i := 0; i < totalFrames; i++ {
		frameID := uuid.New()
		endedAt := base.Add(time.Duration(i) * time.Hour)
		messageID := uuid.New()
		h.ExecSQL(`INSERT INTO rimsky_messages
		    (id, instance_id, type, sender, sender_kind)
		    VALUES ($1, $2, 'fixture/retention-sweep', 'operator', 'operator')`,
			messageID, instanceID)
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

		if i >= totalFrames-keepFrames {
			survivingRunIDs[runID.String()] = true
		} else {
			prunedRunIDs[runID.String()] = true
		}
	}
	require.Len(t, survivingRunIDs, keepFrames)
	require.Len(t, prunedRunIDs, totalFrames-keepFrames)

	staleFrameID := uuid.New()
	staleMessageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/retention-sweep-stale', 'operator', 'operator')`,
		staleMessageID, instanceID)
	h.ExecSQL(`INSERT INTO rimsky_frames
	    (frame_id, instance_id, triggering_message_id, state,
	     queued_at, started_at, ended_at, frame_timeout_ms)
	    VALUES ($1, $2, $3, 'completed',
	            $4, $4, $4, 600000)`,
		staleFrameID, instanceID, staleMessageID, base)

	staleLineageIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for _, lid := range staleLineageIDs {
		h.ExecSQL(`INSERT INTO rimsky_lineage
		    (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
		    VALUES ($1, 'leaf_run', $2, $3, $4,
		            jsonb_build_object('run_id', $5::text, 'claim_handle_id', $6::text), '')`,
			lid, instanceID, staleFrameID, base, uuid.NewString(), uuid.NewString())
	}

	freshLineageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_lineage
	    (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
	    VALUES ($1, 'leaf_run', $2, $3, now(),
	            jsonb_build_object('run_id', $4::text, 'claim_handle_id', $5::text), '')`,
		freshLineageID, instanceID, staleFrameID, uuid.NewString(), uuid.NewString())

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
