// Scenario 16 (renamed) — named-lock counting bounds simultaneous claims.
// Replaces the legacy concurrency_tag_limit scenario: tag-limit semantics
// were retired in the stores redesign and replaced by node-template
// `locks: [{name, mode: counting, limit: N}]` declarations (spec §11.3 +
// §11.5). The runner enforces the limit via the §13.3 step 3b advisory-
// locked recount over `rimsky_lock_holders` rows of kind=named.
//
// Two nodes both declare a named lock with limit=1. We pre-seed one
// holder row owned by a different supervisor; the supervisor's runner
// finds the count at limit and bails the candidate. After the seeded
// row's expires_at lapses (or we delete it), the runner can claim the
// lock. Asserts the count, the bail, and the eventual successful
// acquisition.
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

func TestNamedLockCounting(t *testing.T) {
	t.Parallel()
	// NoSupervisor so we can drive RunNode deterministically and assert
	// the per-call eligibility decision rather than racing the live
	// supervisor's tick loop.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	// Stub responds with a synchronous Complete so the runner walks the
	// terminal path inline and releases the lock-holder row before
	// RunNode returns.
	h.Stub.WhenType("a").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "named-lock-count", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithLocks(scenario.CountingLock("slot:foo", 1)),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-tag", map[string]any{})

	a := h.FindNode(iid, "a")
	require.NotNil(t, a)

	// Force the dispatch row to a known eligible shape with enqueued_at
	// safely in the past (defensive against any clock skew between the
	// test host and the Postgres container).
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
	// expires_at well in the future. CountByNamedLock will see count=1
	// and the runner bails before claiming the dispatch row.
	foreignHolderID := uuid.New()
	lockName := "slot:foo"
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 foreignHolderID,
			LockKind:           storage.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "other-sup",
			HolderNodeID:       a.ID, // FK target; the holder can be any real node
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

	// First run: the limit is reached → no candidate is eligible.
	out, err := supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.False(t, out.Ran, "first run should bail on named-lock limit")

	// Node still stale; dispatch row still unclaimed.
	got, err := h.Storage.Nodes().Get(h.Ctx, a.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	// Release the foreign holder row → count drops to 0 → the runner
	// can now claim and the synchronous stub completes the run.
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Delete(ctx, foreignHolderID, "other-sup", tx)
	}))

	out, err = supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "second run should claim once the lock is released")

	got, err = h.Storage.Nodes().Get(h.Ctx, a.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State,
		"node should reach fresh after the synchronous stub Complete")
}
