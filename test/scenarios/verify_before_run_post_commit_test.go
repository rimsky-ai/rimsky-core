// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Gate 3 — the post-commit limb of @blessed-invariant 5 (verify-before-run).
//
// The sibling scenario `TestVerifyBeforeRunRace` exercises the candidate-
// SELECT guard: a row pre-claimed by another supervisor is never admitted
// as a candidate, so the runner returns Ran=false without ever committing.
// That covers the common case but NOT the rare cross-transaction handoff
// the invariant actually exists to catch: a row that IS unclaimed at
// candidate-SELECT time (so our runner wins the acquisition tx and commits
// ownership to itself) but is then stolen by another supervisor in the
// narrow window between the acquisition commit and the verify-before-run
// separate-read. The verify-read must catch that flip, emit
// `orphaned_claim_lost_race`, and bail WITHOUT dispatching to the executor.
//
// Forcing that window deterministically requires an injection point between
// the commit and the verify-read — `RunArgs.PostCommitHook`, a nil-default
// test-only seam. The hook flips `claimed_by` to a thief supervisor, exactly
// as a racing supervisor's claim would, so the verify-read observes foreign
// ownership and the bail path fires.
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

	// @constraint: Seed a single UNCLAIMED dispatch row (claimed_by left NULL, phase
	// defaults to 'pending'). Unlike the candidate-guard sibling test we do
	// NOT pre-seed an owner: this row must be admitted by the candidate
	// SELECT so our runner wins the acquisition tx and commits ownership to
	// itself — only then is the post-commit steal window reachable.
	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_node_runs WHERE node_id = $1`, n.ID)
	require.NoError(t, err)
	require.NotNil(t, n.FrameID, "expected node to carry a frame_id from the initial frame advance")
	dispatchID := uuid.New()
	mainScopeID := h.GetMainRunScopeID(iid)
	_, err = h.Pool.Exec(h.Ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, frame_id, run_scope_id)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '5 seconds', $3, $4)`,
		dispatchID, n.ID, *n.FrameID, mainScopeID,
	)
	require.NoError(t, err)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// stolen records whether the post-commit hook actually fired and flipped
	// ownership — guards against a silent regression where the seam stops
	// being invoked (which would make the test pass for the wrong reason).
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
		// @deliberate: Force the cross-transaction ownership flip in the window between
		// the acquisition commit and the verify-before-run separate-read.
		PostCommitHook: func(ctx context.Context) {
			tag, uerr := h.Pool.Exec(ctx,
				`UPDATE rimsky_node_runs SET claimed_by = 'thief-supervisor', claimed_at = NOW() WHERE id = $1`,
				dispatchID,
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

	// @deliberate: The executor must NOT have been invoked: the node stays stale (the
	// runner never transitioned it to running) and no terminal event was
	// emitted for it.
	var got *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, gerr := h.Persist.Nodes().Get(h.Ctx, n.ID, tx)
		got = r
		return gerr
	}))
	require.Equal(t, cascade.NodeStateStale, got.State,
		"node must remain stale — the verify bail fired before the running transition / dispatch")

	var terminalCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'terminal/%'`,
		n.ID,
	).Scan(&terminalCount))
	require.Zero(t, terminalCount,
		"no terminal/* event must be emitted — the stolen dispatch was never executed")

	// @deliberate: The bail path must emit orphaned_claim_lost_race for the stolen
	// dispatch, carrying the dispatch_id in its payload.
	var orphanCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE kind = 'orphaned_claim_lost_race'
		    AND payload->>'dispatch_id' = $1`,
		dispatchID.String(),
	).Scan(&orphanCount))
	require.Equal(t, 1, orphanCount,
		"verify-before-run bail must emit exactly one orphaned_claim_lost_race event for the stolen dispatch")
}
