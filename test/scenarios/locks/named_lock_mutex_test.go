// §19.1 — named-lock mutex (limit=1) blocks a second acquirer.
//
// A node declares a named lock with mode=mutex. We pre-seed a foreign
// holder row owned by a different supervisor; the §13.3 step-3b
// advisory-locked recount sees count=1 and the runner bails. After
// deleting the foreign row the runner can claim and the synchronous
// stub completes the run.
//
// Sister of named_lock_counting_test.go (limit=N>1); the runner code path
// is identical apart from the LockMode discriminator. Both tests live in
// `locks/` per spec §19.1.
package locks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/supervisor"
)

func TestNamedLockMutex(t *testing.T) {
	t.Parallel()
	// NoSupervisor / NoScheduler so the per-call eligibility decision is
	// observable without racing the live supervisor's tick loop.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	h.Stub.WhenType("a").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "named-lock-mutex", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithLocks(scenario.MutexLock("slot:mutex")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-mutex", map[string]any{})

	a := h.FindNode(iid, "a")
	require.NotNil(t, a)

	// Defensive: force the dispatch row to a known eligible shape with
	// enqueued_at safely in the past.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_dispatch
		    SET executor_name = 'stub',
		        required_stores = '{}',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        enqueued_at = NOW() - INTERVAL '5 seconds'
		  WHERE node_id = $1`,
		a.ID,
	)
	require.NoError(t, err)

	// Pre-seed a foreign holder row for the same named lock with
	// expires_at well in the future. CountByNamedLock will see count=1,
	// limit=1 (mutex implicit) → runner bails before claiming.
	foreignHolderID := uuid.New()
	lockName := "slot:mutex"
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 foreignHolderID,
			LockKind:           storage.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "other-sup",
			HolderNodeID:       a.ID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx)
	}))

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	args := supervisor.RunArgs{
		Storage:           h.Storage,
		Queue:             h.Queue,
		QueuePool:         h.Pool,
		LockHolders:       store.NewLockHoldersClient(h.Pool),
		StoreRegistry:     h.Stores,
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

	// First run: mutex held by foreign supervisor → runner bails.
	out, err := supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.False(t, out.Ran, "first run should bail on mutex limit")

	got, err := h.Storage.Nodes().Get(h.Ctx, a.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	// Release the foreign holder → count drops to 0 → runner claims.
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Delete(ctx, foreignHolderID, "other-sup", tx)
	}))

	out, err = supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "second run should claim once the mutex is released")

	got, err = h.Storage.Nodes().Get(h.Ctx, a.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State,
		"node should reach fresh after the synchronous stub Complete")
}
