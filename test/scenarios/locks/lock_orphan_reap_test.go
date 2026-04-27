// §19.1 — orphan-reap: a lock-holder row left behind by a dead
// supervisor is reaped by the scheduler's §13.5 step-2 sweep once
// expires_at is past, and the downstream node becomes re-dispatchable.
//
// Models a "supervisor went silent" failure by:
//  1. registering a dead supervisor and a node assigned to it,
//  2. inserting a lock-holder row owned by the dead supervisor with
//     expires_at backdated past the §18 invariant 6 cutoff (5 ×
//     heartbeat_timeout). The harness's heartbeat_timeout default is
//     5s, so the cutoff is 25s; we backdate expires_at to "2 minutes
//     ago" to be well clear of any timing slop.
//
// The harness boots the scheduler in tick-interval=250ms mode, so the
// sweep fires within seconds. We poll for the row's deletion and the
// `lock_orphan_reaped` event, then verify a fresh node downstream of
// the same supervisor (no lock involvement) is dispatch-eligible —
// i.e. the orphan reap left the queue in a usable state.
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

func TestLockOrphanReap(t *testing.T) {
	t.Parallel()
	// Scheduler runs (default tick 250ms) so the §13.5 sweep fires.
	// NoSupervisor so our manufactured assignment to "dead-sup" is
	// not stomped by the live supervisor.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	// One worker node. We block its dispatch via a named lock the dead
	// supervisor holds; once the lock is reaped, the node becomes
	// dispatch-eligible.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "orphan-reap", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithLocks(scenario.MutexLock("reaped-mutex")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-orphan-reap", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Force the dispatch row eligible.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_dispatch
		    SET executor_name = 'stub',
		        required_stores = '{}',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        enqueued_at = NOW() - INTERVAL '5 seconds'
		  WHERE node_id = $1`,
		worker.ID,
	)
	require.NoError(t, err)

	// Insert an expired lock-holder row owned by a dead supervisor.
	// expires_at is well past the §18 invariant 6 cutoff (5 ×
	// heartbeat_timeout) so the sweep is guaranteed to pick it up.
	holderID := uuid.New()
	lockName := "reaped-mutex"
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 holderID,
			LockKind:           storage.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "dead-sup",
			HolderNodeID:       worker.ID,
			ExpiresAt:          time.Now().Add(-2 * time.Minute),
		}, tx)
	}))

	// Wait for the scheduler's §13.5 step-2 sweep. The harness scheduler
	// tick is 250ms; 20s gives healthy margin even on heavily-loaded CI.
	deadline := time.Now().Add(20 * time.Second)
	var reaped bool
	for time.Now().Before(deadline) {
		got, _ := h.Storage.LockHolders().Get(h.Ctx, holderID, nil)
		if got == nil {
			reaped = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, reaped, "expired lock-holder row was not reaped by §13.5 step-2 sweep")

	// `lock_orphan_reaped` event was emitted with the reaped row's
	// metadata.
	wid := worker.ID
	evs, err := h.Storage.Events().List(h.Ctx,
		storage.EventListFilter{NodeID: &wid, Kind: "lock_orphan_reaped"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events, "expected lock_orphan_reaped event")
	require.Equal(t, "named", evs.Events[0].Payload["lock_kind"])
	require.Equal(t, "dead-sup", evs.Events[0].Payload["supervisor_id"])

	// Downstream re-dispatchable: with the lock-holder row gone, a
	// running-supervisor invocation of RunNode can now claim the worker
	// dispatch row. We drive RunNode directly to assert the lock is no
	// longer held.
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")

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
		SupervisorID:      "fresh-sup",
		AcceptedExecutors: []string{"stub"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
	}

	out, err := supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "downstream worker must be re-dispatchable after orphan reap")

	got, err := h.Storage.Nodes().Get(h.Ctx, worker.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State,
		"worker should reach fresh after the synchronous stub Complete")

}
