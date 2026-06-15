// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// F3 must-pass scenario — fanout_heartbeat_stale_recovery_e2e.
//
// End-to-end coverage of fan-out child heartbeat-stale recovery under
// the RunScope-first reshape per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Test coverage matrix / F3":
//
//   - A partition-child run is seeded as a zombie active row tied to a
//     fake supervisor whose last_heartbeat_at is older than the
//     scheduler's HeartbeatTimeout.
//   - The scheduler's SweepStaleHeartbeats fires; transitions the row
//     running → stale, re-enqueues the dispatch via Queue.Enqueue with
//     prior_dispatch_id + prior_dispatch_disposition = "heartbeat_stale".
//   - The new dispatch lives in the SAME partition RunScope as the
//     zombie (no scope reassignment on recovery).
//
// Pins two load-bearing properties of the reshape:
//
//  1. Heartbeat-stale recovery threads RunScope correctly — re-enqueue
//     uses the recovered run's RunScopeID, NOT the instance's main
//     RunScope.
//  2. The persistence-layer recovery-aware fields populate. The
//     wire-level surfacing of these fields is regression-pinned by F2's
//     retry-after-error path + the recovery_aware_dispatch.go
//     conformance test.
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

var _ = genv1.PriorDispatchDisposition_PRIOR_HEARTBEAT_STALE // @deliberate: package-import witness

func TestFanOutHeartbeatStaleRecoveryE2E(t *testing.T) {
	t.Parallel()
	// @deliberate: NoSupervisor + NoScheduler so the test owns the heartbeat-stale
	// recovery deterministically — no background sweep can race the
	// seeded zombie. The test seeds the zombie row directly, then drives
	// runtime.SweepStaleHeartbeats explicitly (line below) — that's the
	// canonical conductor path, exercising the full re-enqueue chain
	// (state transition + zombie row retirement + cascade walk + new
	// dispatch with recovery-aware fields populated). The implicit
	// supervisor's heartbeat-tick is not what is under test here; we
	// want the sweep firing on a known-stale fixture.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fanout-hb-stale", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-fanout-hb-stale", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n, "worker node missing")

	// @deliberate: Build the partition RunScope manually: F3 doesn't need to drive
	// SplitScope through a real producer. The partition RunScope's
	// shape (parent_run_id, partition_key, instance_id) is all the
	// SweepStaleHeartbeats path consumes.
	mainScopeID := h.GetMainRunScopeID(iid)
	parentRunID := shared.UUID(uuid.New())
	partitionScopeID := shared.UUID(uuid.New())

	// @deliberate: Seed a synthetic parent run in the main scope so the partition
	// scope's parent_run_id has a valid FK target. The parent row is
	// minimal — only the columns the partition's recovery path reads.
	var frameID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND state = 'running' LIMIT 1`,
		uuid.UUID(iid),
	).Scan(&frameID))

	// @deliberate: Synthetic main-scope parent run (so partition_scope.parent_run_id
	// FK is satisfied). Insert directly with run_scope_id populated.
	_, err := h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores,
		                              enqueued_at, phase, state, frame_id, run_scope_id)
		VALUES ($1, $2, 'stub', '{}'::text[], NOW(), 'completed', 'fresh', $3, $4)
	`, parentRunID, n.ID, frameID, mainScopeID)
	require.NoError(t, err)

	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               partitionScopeID,
			ParentRunScopeID: &mainScopeID,
			ParentRunID:      &parentRunID,
			GraphName:        "main",
			PartitionKey:     "partition-X",
			InstanceID:       iid,
		})
	}))

	// @deliberate: Clear any harness-seeded initial dispatch in the main scope so the
	// nodeSelect LATERAL surfaces our zombie row unambiguously when
	// SweepStaleHeartbeats picks it up. The LATERAL's "most-relevant
	// run row" predicate ranks in-flight rows by enqueued_at DESC; a
	// stale-but-recent initial pending row would otherwise win the
	// projection over the zombie's older active row.
	_, err = h.Pool.Exec(h.Ctx,
		`DELETE FROM rimsky_node_runs WHERE node_id = $1 AND id != $2 AND id != $3`,
		n.ID, parentRunID, uuid.Nil)
	require.NoError(t, err)

	// @deliberate: Seed the zombie partition-child run as a live in-flight 'active'
	// row with phase='active' state='running' and a stale heartbeat
	// older than HeartbeatTimeout. This is the pre-sweep state that
	// SweepStaleHeartbeats inspects: zombie supervisor's heartbeat
	// stopped firing, the run is still nominally running. The bound
	// node row's frame_id + executor must be set so the sweep's
	// re-enqueue path resolves them.
	_, err = h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes SET frame_id = $1, updated_at = NOW(), executor = 'stub' WHERE id = $2`,
		frameID, n.ID)
	require.NoError(t, err)
	zombieID := uuid.New()
	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores,
		                              enqueued_at, claimed_by, claimed_at,
		                              last_heartbeat_at, phase, state, frame_id, run_scope_id)
		VALUES ($1, $2, 'stub', '{}'::text[], NOW() - INTERVAL '60 seconds',
		        'zombie-sup', NOW() - INTERVAL '60 seconds',
		        NOW() - INTERVAL '30 seconds',
		        'active', 'running', $3, $4)
	`, zombieID, n.ID, frameID, partitionScopeID)
	require.NoError(t, err)

	// @constraint: Drive SweepStaleHeartbeats — this is the canonical conductor
	// path. Per spec §"Recovery-aware executor protocol" the sweep
	// transitions phase 'active' → 'completed' / state 'running' →
	// 'stale', then issues a Queue.Enqueue with prior_dispatch_id =
	// zombieID and prior_dispatch_disposition = "heartbeat_stale".
	require.NoError(t, runtime.SweepStaleHeartbeats(h.Ctx, runtime.ConductorArgs{
		Persist:          h.Persist,
		Queue:            h.Queue,
		Clock:            shared.SystemClock{},
		Logger:           shared.SilentLogger{},
		HeartbeatTimeout: 5 * time.Second,
	}))

	// @deliberate: Assert: a new dispatch row exists in the partition RunScope with
	// prior_dispatch_id = zombieID and disposition = heartbeat_stale.
	var newDispatchID uuid.UUID
	var priorID *uuid.UUID
	var priorDisposition *string
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		SELECT id, prior_dispatch_id, prior_dispatch_disposition
		  FROM rimsky_node_runs
		 WHERE node_id = $1
		   AND run_scope_id = $2
		   AND id <> $3
		   AND prior_dispatch_id = $3
	`, n.ID, partitionScopeID, zombieID).Scan(&newDispatchID, &priorID, &priorDisposition))

	require.NotNil(t, priorID, "recovery dispatch should carry prior_dispatch_id")
	require.Equal(t, zombieID, *priorID,
		"prior_dispatch_id must equal the zombie dispatch id")
	require.NotNil(t, priorDisposition, "recovery dispatch should carry prior_dispatch_disposition")
	require.Equal(t, "heartbeat_stale", *priorDisposition,
		"prior_dispatch_disposition must be heartbeat_stale per the SweepStaleHeartbeats path")

	// @deliberate: Sanity: the new dispatch lives in the SAME partition RunScope as
	// the zombie. The SELECT predicate `run_scope_id = $2` above gates
	// this — but assert again on the row directly so the failure
	// message is unambiguous if the row drifts.
	var newScopeID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT run_scope_id FROM rimsky_node_runs WHERE id = $1`, newDispatchID,
	).Scan(&newScopeID))
	require.Equal(t, uuid.UUID(partitionScopeID), newScopeID,
		"recovery dispatch must stay in the same partition RunScope as the zombie")
}
