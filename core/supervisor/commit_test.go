// Shared test fixture for the supervisor package.
//
// This file used to host tests for the legacy `supervisor.Commit` entry
// point (deleted in Task 28; the commit flow now lives in
// `runner_terminal.go::applyTerminalComplete` and is exercised end-to-end
// through `RunNode`). The remaining purpose of this file is to provide
// the `fixture` scaffolding shared by `runner_test.go`,
// `callback_test.go`, and `supervisor_test.go`.
//
// New commit-path coverage lives in `runner_test.go` because the
// applyTerminalComplete function is unexported — driving it via a real
// RunNode call gives us the same coverage without exposing internal
// surface area.
package supervisor_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	nodepkg "github.com/fallguy/rimsky/core/node"
	queuepg "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	storagepg "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
)

// fixture is the common scaffolding for supervisor tests. Every supervisor
// test boots its own fresh Postgres container via pgtest, deploys a
// throwaway template + instance, and pre-builds a `*store.Registry` with a
// single stub-filesystem store and a single stub-claim store. Tests that
// need different store wiring can build their own registry.
type fixture struct {
	t           *testing.T
	pool        *pgxpool.Pool
	sb          *storagepg.PostgresStorageBackend
	q           *queuepg.Queue
	clock       *shared.ControllableClock
	log         *shared.CapturingLogger
	instance    shared.UUID
	template    shared.UUID
	registry    *store.Registry
	lockHolders *store.LockHoldersClient
	// fsStore + claimStore are convenience accessors for tests that want
	// to seed claim-store items or assert on stub state.
	fsStore    *stub.Store
	claimStore *stub.Store
}

// newFixture spins up a fresh Postgres container, deploys a template with
// the supplied per-node-type defs, creates a single instance, and returns
// the wired fixture. Call sites add nodes via addStaleNode / addRunningNode
// and enqueue dispatches via enqueueAndClaim or queue.Enqueue directly.
func newFixture(t *testing.T, nodeTypes []nodepkg.TemplateNodeDef) *fixture {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	sb := storagepg.New(pool)
	qq := queuepg.New(pool)

	tplSum, err := sb.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: "sup-tpl-" + uuid.NewString()[:8], Version: "v1",
		FrameResolution: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:  nodepkg.FrameTimeoutDefaultMs,
		Nodes:           nodeTypes,
	}, nil)
	require.NoError(t, err)

	inst, err := sb.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: tplSum.ID, ConsumerKey: "ck-" + uuid.NewString()[:8],
		Params: map[string]any{},
	}, nil)
	require.NoError(t, err)

	registry, fsStore, claimStore := buildStubRegistry(t)
	lockHolders := store.NewLockHoldersClient(pool)

	return &fixture{
		t:           t,
		pool:        pool,
		sb:          sb,
		q:           qq,
		clock:       shared.NewControllableClock(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)),
		log:         shared.NewCapturingLogger(),
		instance:    inst.ID,
		template:    tplSum.ID,
		registry:    registry,
		lockHolders: lockHolders,
		fsStore:     fsStore,
		claimStore:  claimStore,
	}
}

// buildStubRegistry builds a store registry with two pre-built stub stores
// suitable for the supervisor tests. Stores: "fs" (region-lock-capable
// stub_filesystem) and "claims" (claim-capable stub_claim_store). Tests
// that don't reference Stores in their template never observe either —
// the registry is only consulted when a node-def names a store.
func buildStubRegistry(t *testing.T) (*store.Registry, *stub.Store, *stub.Store) {
	t.Helper()
	reg := store.NewRegistry()
	reg.Register(stub.FilesystemFactory())
	reg.Register(stub.ClaimStoreFactory())
	built, err := reg.BuildAll(store.StoresConfig{
		Stores: map[string]map[string]any{
			"fs":     {"kind": stub.KindFilesystem},
			"claims": {"kind": stub.KindClaimStore},
		},
	})
	require.NoError(t, err)
	fs, _ := built["fs"].(*stub.Store)
	claims, _ := built["claims"].(*stub.Store)
	require.NotNil(t, fs, "fs stub store missing")
	require.NotNil(t, claims, "claims stub store missing")
	return reg, fs, claims
}

// addRunningNode creates a node with the given type/executor/dependencies,
// transitions it to running, and returns the row. Bypasses the state
// machine (Create defaults to 'fresh' under the frame model and the
// state machine forbids fresh→running) by direct UPDATE. Also seeds
// (or reuses) the running rimsky_frames row for this fixture's
// instance and assigns frame_id on the new node so dispatch enqueues
// satisfy blessed-invariant 19.
func (f *fixture) addRunningNode(nodeType, executor string, deps ...shared.UUID) storage.NodeRow {
	f.t.Helper()
	ctx := context.Background()
	n, err := f.sb.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance, NodeType: nodeType,
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(f.t, err)
	frameID := f.ensureRunningFrame()
	_, err = f.pool.Exec(ctx,
		`UPDATE rimsky_nodes SET state = 'running', frame_id = $1 WHERE id = $2`,
		frameID, n.ID)
	require.NoError(f.t, err)
	out, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(f.t, err)
	return *out
}

// addStaleNode creates a node and forces it to 'stale'. Under the
// frame-resolution model, Create() defaults to 'fresh' so the test
// fixtures must explicitly UPDATE to stale to match the pre-frame
// flow these tests exercise. Also seeds a running rimsky_frames row
// and assigns frame_id.
func (f *fixture) addStaleNode(nodeType, executor string, deps ...shared.UUID) storage.NodeRow {
	f.t.Helper()
	ctx := context.Background()
	n, err := f.sb.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance, NodeType: nodeType,
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(f.t, err)
	frameID := f.ensureRunningFrame()
	_, err = f.pool.Exec(ctx,
		`UPDATE rimsky_nodes SET state = 'stale', frame_id = $1 WHERE id = $2`,
		frameID, n.ID)
	require.NoError(f.t, err)
	out, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(f.t, err)
	return *out
}

// ensureRunningFrame returns the running rimsky_frames row for this fixture's
// instance, inserting one if none exists. Used by addStaleNode/addRunningNode
// so every test node has a frame_id set.
func (f *fixture) ensureRunningFrame() shared.UUID {
	f.t.Helper()
	ctx := context.Background()
	var id shared.UUID
	err := f.pool.QueryRow(ctx,
		`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND state = 'running' LIMIT 1`,
		f.instance,
	).Scan(&id)
	if err == nil {
		return id
	}
	require.NoError(f.t, f.pool.QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
        VALUES ($1, 'serial_queue', 'running', ARRAY[gen_random_uuid()]::UUID[], now(), now(), 600000)
        RETURNING frame_id
    `, f.instance).Scan(&id))
	return id
}

// eventKinds returns the Kind values for every event row on the node, oldest first.
func (f *fixture) eventKinds(nodeID shared.UUID) []string {
	f.t.Helper()
	ctx := context.Background()
	nid := nodeID
	res, err := f.sb.Events().List(ctx, storage.EventListFilter{NodeID: &nid},
		storage.ListPagination{Limit: 1000}, nil)
	require.NoError(f.t, err)
	kinds := make([]string, 0, len(res.Events))
	for i := len(res.Events) - 1; i >= 0; i-- {
		kinds = append(kinds, res.Events[i].Kind)
	}
	return kinds
}

func containsString(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// pendingDispatchForNode returns the pending dispatch row for the node, if any.
func (f *fixture) pendingDispatchForNode(nodeID shared.UUID) *shared.DispatchRow {
	f.t.Helper()
	ctx := context.Background()
	var (
		id         shared.UUID
		executor   *string
		stores     []string
		enqueuedAt time.Time
		claimedBy  *string
		claimedAt  *time.Time
	)
	err := f.pool.QueryRow(ctx,
		`SELECT id, executor_name, required_stores, enqueued_at, claimed_by, claimed_at
		   FROM rimsky_dispatch WHERE node_id = $1`, nodeID,
	).Scan(&id, &executor, &stores, &enqueuedAt, &claimedBy, &claimedAt)
	if err != nil {
		return nil
	}
	return &shared.DispatchRow{
		ID: id, NodeID: nodeID, ExecutorName: executor,
		RequiredStores: stores,
		EnqueuedAt:     enqueuedAt,
		ClaimedBy:      claimedBy, ClaimedAt: claimedAt,
	}
}

// hasLockHolderForNode returns true if at least one rimsky_lock_holders
// row exists with holder_node_id = nodeID. Used by tests asserting the
// release path actually deletes the rows.
func (f *fixture) hasLockHolderForNode(nodeID shared.UUID) bool {
	f.t.Helper()
	ctx := context.Background()
	rows, err := f.sb.LockHolders().ListByHolderNode(ctx, nodeID, nil)
	if err != nil {
		return false
	}
	return len(rows) > 0
}

// guard: avoid unused-import warnings if a future shrink of this file
// removes the explicit fmt usage from the helper bodies.
var _ = fmt.Sprintf
