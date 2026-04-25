// Scenario 14 — verify-before-run race: a dispatch row is claimed by
// another supervisor; when our runner attempts to execute it, the
// claim-ownership check bails and emits orphaned_claim_lost_race.
package scenarios

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/supervisor"
)

func TestVerifyBeforeRunRace(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "race", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})
	iid := h.CreateInstance(tid, "ck-race", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	// Insert a dispatch row already claimed by "fake-other". Delete the
	// auto-enqueued row first so our insert doesn't conflict on node_id.
	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_dispatch WHERE node_id = $1`, n.ID)
	require.NoError(t, err)
	dispatchID := uuid.New()
	_, err = h.Pool.Exec(h.Ctx,
		`INSERT INTO rimsky_dispatch (id, node_id, executor_name, concurrency_tags, enqueued_at, claimed_by, claimed_at)
		 VALUES ($1, $2, 'stub', '{}', NOW(), 'fake-other', NOW())`,
		dispatchID, n.ID,
	)
	require.NoError(t, err)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// RunNode with SupervisorID="test-runner" should find the claim held by
	// someone else and return Ran=false without touching node state.
	out, err := supervisor.RunNode(h.Ctx, supervisor.RunArgs{
		Storage: h.Storage, Queue: h.Queue, Clock: shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		NodeID:       n.ID,
		DispatchID:   dispatchID,
		SupervisorID: "test-runner",
		Pool:         pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		GetResource: func(_ context.Context, _ shared.UUID) (resource.Resource, error) {
			return nil, nil
		},
	}, nil)
	require.NoError(t, err)
	require.False(t, out.Ran, "runner should not execute when another supervisor holds the claim")

	// Node still stale.
	got, err := h.Storage.Nodes().Get(h.Ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	// Expect orphaned_claim_lost_race event.
	nid := n.ID
	evs, err := h.Storage.Events().List(h.Ctx,
		storage.EventListFilter{NodeID: &nid, Kind: "orphaned_claim_lost_race"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events, "expected orphaned_claim_lost_race event")
}
