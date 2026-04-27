// §19.1 — region-lock conflict: two supervisors race on overlapping
// regions; only one wins via §13.3 step-3d RegionsConflict.
//
// We declare a single node with a region lock against the stub
// filesystem store. The first supervisor's runner claims the dispatch
// row, inserts a region lock-holder row in the acquisition tx, and
// reaches running. A second supervisor running in parallel sees the
// existing region holder during step-3d re-evaluation (or in step-2
// hint eligibility) and bails. After the first supervisor releases
// the lock, the second supervisor would be free to claim — but the
// node has already advanced to fresh in this scenario.
//
// The "two supervisors race" mechanism is modeled here by:
//  1. Pre-seed an existing region lock-holder row for the node's
//     target store + region, owned by `winner-sup`. This represents
//     the in-tx state after `winner-sup`'s acquisition tx committed.
//  2. Run `RunNode` with `loser-sup` as SupervisorID. The runner's
//     step-3d region-conflict re-evaluation sees the existing holder,
//     RegionsConflict returns true, the runner bails. No claim, no
//     lock-holder row inserted by `loser-sup`.
//
// This is the same shape as named_lock_counting_test but for the
// region-lock branch (§13.3 step 3d) rather than the named-lock branch
// (§13.3 step 3b).
package locks

import (
	"context"
	"encoding/json"
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
	"github.com/fallguy/rimsky/core/store/filesystem"
	"github.com/fallguy/rimsky/core/supervisor"
)

func TestRegionLockConflict(t *testing.T) {
	t.Parallel()
	// Configure a real filesystem store under name "content" so the
	// node's RegionRef("content", ...) resolves to a registered
	// region-capable store and the template validator's
	// region-on-filesystem rule passes. Both `winner-sup` and
	// `loser-sup` share this store registry — mirrors a single-process
	// deployment with a supervisor pool of two.
	root := t.TempDir()
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor:        true,
		NoScheduler:         true,
		ExtraStoreFactories: []store.Factory{filesystem.Factory{}},
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"content": {
					"kind": "filesystem",
					"mode": "direct",
					"root": root,
				},
			},
		},
	})

	h.Stub.WhenType("a").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "region-conflict", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("content", "shared/x.md")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-region", map[string]any{})

	a := h.FindNode(iid, "a")
	require.NotNil(t, a)

	// Force the dispatch row eligible.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_dispatch
		    SET executor_name = 'stub',
		        required_stores = '{"content"}',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        enqueued_at = NOW() - INTERVAL '5 seconds'
		  WHERE node_id = $1`,
		a.ID,
	)
	require.NoError(t, err)

	// Pre-seed a foreign region holder for "content" with overlapping
	// tokens. The stub_filesystem RegionsConflict treats two []string
	// regions as overlapping iff they share any token; "shared/x.md"
	// shares tokens with itself, so the runner's step-3d will see the
	// conflict and bail.
	foreignHolderID := uuid.New()
	storeName := "content"
	regionData, err := json.Marshal([]string{"shared/x.md"})
	require.NoError(t, err)
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 foreignHolderID,
			LockKind:           storage.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         regionData,
			HolderSupervisorID: "winner-sup",
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
		SupervisorID:      "loser-sup",
		AcceptedExecutors: []string{"stub"},
		AcceptedStores:    []string{"content"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
	}

	// loser-sup attempts to claim. Region-conflict re-evaluation sees
	// the existing holder → bails. No new lock-holder row inserted, no
	// claim taken on the dispatch row.
	out, err := supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.False(t, out.Ran, "loser-sup must lose the region race")

	// Node still stale, dispatch row still unclaimed.
	got, err := h.Storage.Nodes().Get(h.Ctx, a.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)

	// Only the pre-seeded foreign holder row exists; loser-sup did
	// not insert one of its own.
	holders, err := h.Storage.LockHolders().ListByHolderNode(h.Ctx, a.ID, nil)
	require.NoError(t, err)
	require.Len(t, holders, 1, "loser-sup must not have inserted a holder row")
	require.Equal(t, "winner-sup", holders[0].HolderSupervisorID)

	// Release the foreign holder; loser-sup can now win.
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Delete(ctx, foreignHolderID, "winner-sup", tx)
	}))

	out, err = supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "loser-sup should claim once the conflicting region is released")
}
