// Scenario 14 — verify-before-run race (blessed invariant 5): a dispatch
// row already claimed by another supervisor must NOT be executed by ours.
// In the redesigned omnibus runner this manifests as the §7.3 step 1
// candidate SELECT skipping the row (claimed_by IS NULL filter) AND, on
// the rare path where another supervisor steals the row between commit
// and the verify-before-run separate-read guard, the runner emits
// `orphaned_claim_lost_race` and bails without running.
//
// This scenario exercises the candidate-selection guard: with a row
// pre-claimed by a different supervisor, RunNode finds no eligible
// candidates and returns Ran=false; the node remains stale. The
// verify-before-run separate-read complement is unit-tested in
// `core/supervisor` (verifyBeforeRun is unexported); preserving that
// invariant here as a higher-level integration check that ownership
// gates execution end to end.
//
// Migrated to the stores-redesign template grammar (spec §11): no
// resource wiring; the runner is driven through `core/integration.RunNode`
// directly with a stub-store registry from the harness.
package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/modeling/executor"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
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

	// Replace the auto-enqueued dispatch row with one already claimed by
	// a different integration. ClaimDispatchRow is claimant-guarded and
	// SelectCandidates filters claimed_by IS NULL — neither path will
	// admit our runner.
	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_dispatch WHERE node_id = $1`, n.ID)
	require.NoError(t, err)
	// Reuse the frame_id from the node row (seeded by frame.RunTick).
	require.NotNil(t, n.FrameID, "expected node to carry a frame_id from the initial frame advance")
	dispatchID := uuid.New()
	_, err = h.Pool.Exec(h.Ctx,
		`INSERT INTO rimsky_dispatch (id, node_id, executor_name, required_stores, enqueued_at, claimed_by, claimed_at, last_heartbeat_at, frame_id)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '5 seconds', 'fake-other', NOW(), NOW(), $3)`,
		dispatchID, n.ID, *n.FrameID,
	)
	require.NoError(t, err)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// RunNode with our SupervisorID should find no eligible candidate
	// (the row's claimed_by is set), return Ran=false, and leave the
	// node unchanged.
	args := integration.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		LockHolders:       h.Persist.LockHolders(),
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
		HeartbeatInterval: 100 * time.Millisecond,
	}
	out, err := integration.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.False(t, out.Ran,
		"runner should not execute when another supervisor holds the claim")

	// Node remains in stale; the dispatch row is still owned by fake-other.
	got, err := h.Persist.Nodes().Get(h.Ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	own, err := h.Queue.GetClaimedBy(h.Ctx, dispatchID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "fake-other", own.SupervisorID)
}
