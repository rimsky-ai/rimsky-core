// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestVerifyBeforeRunRace(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "race", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-race", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	frameID := h.GetRunningFrameID(iid)
	nodeRunID := uuid.New()
	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
	tx, err := h.Pool.Begin(h.Ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(h.Ctx) }()
	_, err = tx.Exec(h.Ctx, `DELETE FROM rimsky_node_runs WHERE node_id = $1`, n.ID)
	require.NoError(t, err)
	_, err = tx.Exec(h.Ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_claim_producers, enqueued_at, claimed_by, claimed_at, frame_id, run_scope_id, sequence)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '5 seconds', 'fake-other', NOW(), $3, $4, 1)`,
		nodeRunID, n.ID, frameID, mainScopeID,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(h.Ctx))

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	args := runtime.RunArgs{
		Persist:               h.Persist,
		Queue:                 h.Queue,
		ClaimHandles:          h.Persist.ClaimHandles(),
		AdvisoryLocker:        h.Driver.AdvisoryLocker(),
		ClaimProducerRegistry: locks.NewRegistry(),
		Clock:                 shared.SystemClock{},
		Logger:                shared.SilentLogger{},
		SupervisorID:          "scenario-runner",
		AcceptedExecutors:     []string{"stub"},
		Pool:                  pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		LivenessInterval: 100 * time.Millisecond,
	}
	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.False(t, out.Ran,
		"runner should not execute when another supervisor holds the claim")

	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, n.ID, tx)
		latest = r
		return err
	}))
	require.NotNil(t, latest)
	require.Equal(t, cascade.NodeStateStale, latest.State)

	own, err := h.Queue.GetClaimedBy(h.Ctx, nodeRunID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "fake-other", own.SupervisorID)
}
