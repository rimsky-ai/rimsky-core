// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
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
	const blobOrphanBackend = "memory"
	survivingRunIDs := make(map[string]bool)
	prunedRunIDs := make(map[string]bool)
	base := time.Now().Add(-24 * time.Hour)
	blobOrphanHandle := "blob://retention-sweep-fixture/" + uuid.NewString()
	blobOrphanRunSeeded := false
	for i := 0; i < totalFrames; i++ {
		frameID := uuid.New()
		endedAt := base.Add(time.Duration(i) * time.Hour)
		messageID := uuid.New()
		h.ExecSQL(`INSERT INTO rimsky_messages
		    (id, instance_id, type, sender, sender_kind)
		    VALUES ($1, $2, 'fixture/retention-sweep', 'operator', 'operator')`,
			messageID, instanceID)
		h.ExecSQL(`INSERT INTO rimsky_frames
		    (frame_id, instance_id, triggering_message_id, started_at, ended_at, frame_timeout_ms, root_run_scope_id)
		    VALUES ($1, $2, $3,
		            $4, $4, 600000, $5)`,
			frameID, instanceID, messageID, endedAt, uuid.UUID(scopeID))

		runID := uuid.New()
		pruned := i < totalFrames-keepFrames
		if pruned && !blobOrphanRunSeeded {
			blobOrphanRunSeeded = true
			h.ExecSQL(`INSERT INTO rimsky_node_runs
			    (id, node_id, executor_name, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id,
			     scratch_handle, scratch_handle_backend)
			    VALUES ($1, $2, 'worker', $3, 'failed', 'cascade', 1, $4, $5, $6, $7)`,
				runID, nodeID, endedAt, frameID, scopeID, blobOrphanHandle, blobOrphanBackend)
		} else {
			h.ExecSQL(`INSERT INTO rimsky_node_runs
			    (id, node_id, executor_name, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
			    VALUES ($1, $2, 'worker', $3, 'failed', 'cascade', 1, $4, $5)`,
				runID, nodeID, endedAt, frameID, scopeID)
		}

		if !pruned {
			survivingRunIDs[runID.String()] = true
		} else {
			prunedRunIDs[runID.String()] = true
		}
	}
	require.True(t, blobOrphanRunSeeded, "test fixture bug: no pruned node_run seeded with a blob-backed scratch handle")
	require.Len(t, survivingRunIDs, keepFrames)
	require.Len(t, prunedRunIDs, totalFrames-keepFrames)

	staleFrameID := uuid.New()
	staleMessageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/retention-sweep-stale', 'operator', 'operator')`,
		staleMessageID, instanceID)
	h.ExecSQL(`INSERT INTO rimsky_frames
	    (frame_id, instance_id, triggering_message_id, started_at, ended_at, frame_timeout_ms, root_run_scope_id)
	    VALUES ($1, $2, $3,
	            $4, $4, 600000, $5)`,
		staleFrameID, instanceID, staleMessageID, base, uuid.UUID(scopeID))

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

	var orphanCount int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_blob_orphans WHERE handle = $1 AND backend = $2`,
		[]any{blobOrphanHandle, blobOrphanBackend}, &orphanCount)
	assert.Equal(t, 1, orphanCount,
		"pruning a node_run carrying a blob-backed scratch handle must queue it into the blob-orphan ledger")
}

func TestSchedulerTickSweepsClaimHandleRetention(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tplHash := h.DeployTemplate(node.TemplateSpec{
		Name:           "claim-handle-retention-" + uuid.NewString(),
		Version:        "v1",
		FrameTimeoutMs: node.FrameTimeoutDefaultMs,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	instanceID := h.CreateInstance(tplHash, "", map[string]any{})
	workerNode := h.FindNode(instanceID, "worker")
	require.NotNil(t, workerNode)

	const retentionSupervisor = "retention-tick-supervisor"
	producer := "retention-tick-producer"

	seedHandle := func(promote bool) shared.UUID {
		id := shared.UUID(uuid.New())
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			if err := h.Persist.ClaimHandles().Insert(h.Ctx, persistence.ClaimHandleInsertInput{
				ID:                 id,
				LockKind:           persistence.LockKindScope,
				ProducerName:       &producer,
				ClaimScopeData:     json.RawMessage(`{"path":"/retention-tick/` + id.String() + `"}`),
				HolderSupervisorID: retentionSupervisor,
				HolderNodeID:       workerNode.ID,
				ExpiresAt:          time.Now().Add(1 * time.Hour),
				Lifetime:           tmplspec.ClaimLifetimeSubgraph,
			}, tx); err != nil {
				return err
			}
			if !promote {
				return nil
			}
			return h.Persist.ClaimHandles().Promote(h.Ctx, id, retentionSupervisor, tmplspec.ClaimHandleStateCommitted, tx)
		}))
		return id
	}

	resolvedHandle := seedHandle(true)
	activeHandle := seedHandle(false)

	clock := shared.NewControllableClock(time.Now())
	cfg := scheduler.Config{
		Persist:        h.Persist,
		Queue:          h.Queue,
		AdvisoryLocker: h.Driver.AdvisoryLocker(),
		Clock:          clock,
		Logger:         shared.SilentLogger{},
		ClaimHandles:   h.Persist.ClaimHandles(),
		Retention: runtime.RetentionConfig{
			ClaimHandlesTrailing: time.Hour,
		},
	}
	clock.Advance(2 * time.Hour)
	require.NoError(t, scheduler.Tick(h.Ctx, cfg))

	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		row, err := h.Persist.ClaimHandles().Get(h.Ctx, resolvedHandle, tx)
		require.NoError(t, err)
		assert.Nil(t, row, "resolved claim handle older than the trailing window must be reaped by the scheduler tick")
		row, err = h.Persist.ClaimHandles().Get(h.Ctx, activeHandle, tx)
		require.NoError(t, err)
		assert.NotNil(t, row, "an unresolved (active) claim handle must survive the tick's retention sweep")
		return nil
	}))
}
