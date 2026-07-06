// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
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

func TestVerifyBeforeRun_PostCommitSteal(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "post-commit-steal", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-post-commit-steal", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_node_runs WHERE node_id = $1`, n.ID)
	require.NoError(t, err)
	frameID := h.GetRunningFrameID(iid)
	nodeRunID := uuid.New()
	mainScopeID := h.GetMainRunScopeID(iid)
	_, err = h.Pool.Exec(h.Ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, frame_id, run_scope_id, sequence)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '5 seconds', $3, $4, 1)`,
		nodeRunID, n.ID, frameID, mainScopeID,
	)
	require.NoError(t, err)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	var stolen bool
	args := runtime.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		ClaimHandles:      h.Persist.ClaimHandles(),
		AdvisoryLocker:    h.Driver.AdvisoryLocker(),
		StoreRegistry:     locks.NewRegistry(),
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "scenario-runner",
		AcceptedExecutors: []string{"stub"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		LivenessInterval: 100 * time.Millisecond,
		PostCommitHook: func(ctx context.Context) {
			tag, uerr := h.Pool.Exec(ctx,
				`UPDATE rimsky_node_runs SET claimed_by = 'thief-supervisor', claimed_at = NOW() WHERE id = $1`,
				nodeRunID,
			)
			require.NoError(t, uerr)
			require.Equal(t, int64(1), tag.RowsAffected(),
				"post-commit hook should flip ownership of exactly the committed dispatch row")
			stolen = true
		},
	}

	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, stolen,
		"post-commit hook must have fired — the acquisition tx committed and the verify window was reached")
	require.False(t, out.Ran,
		"verify-before-run must bail (Ran=false) when the claim was stolen between commit and the verify-read")

	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, gerr := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, n.ID)
		latest = r
		return gerr
	}))
	require.NotNil(t, latest)
	require.Equal(t, cascade.NodeStateStale, latest.State,
		"node must remain stale — the verify bail fired before the running transition / dispatch")

	var terminalCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'terminal/%'`,
		n.ID,
	).Scan(&terminalCount))
	require.Zero(t, terminalCount,
		"no terminal/* event must be emitted — the stolen dispatch was never executed")

	var orphanCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE kind = 'orphaned_claim_lost_race'
		    AND payload->>'dispatch_id' = $1`,
		nodeRunID.String(),
	).Scan(&orphanCount))
	require.Equal(t, 1, orphanCount,
		"verify-before-run bail must emit exactly one orphaned_claim_lost_race event for the stolen dispatch")
}
