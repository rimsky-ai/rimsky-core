// Tests for InvalidateNode + RecalculateNode. Backed by the real
// PostgresStorageBackend via the pgtest harness; the DispatchQueue is a
// lightweight in-memory fake that records Enqueue / RemoveForNode calls so
// the test can assert on dispatch behavior without depending on the Postgres
// queue implementation.
package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// --- In-memory fake DispatchQueue --------------------------------------
// Named invTestQueue to avoid colliding with fakeQueue in pure_cascade_test.go
// (same package). This variant additionally records RemoveForNode calls.

type invTestQueue struct {
	mu           sync.Mutex
	enqueued     []queue.DispatchRequest
	removedNodes []shared.UUID
}

func newInvTestQueue() *invTestQueue { return &invTestQueue{} }

func (f *invTestQueue) Enqueue(_ context.Context, req queue.DispatchRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, req)
	return nil
}

func (f *invTestQueue) SelectCandidates(_ context.Context, _ pgx.Tx, _ queue.SelectCandidatesRequest) ([]queue.Candidate, error) {
	return nil, nil
}

func (f *invTestQueue) ClaimDispatchRow(_ context.Context, _ pgx.Tx, _ shared.UUID, _ string) (bool, error) {
	return false, nil
}

func (f *invTestQueue) Complete(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *invTestQueue) Fail(_ context.Context, _ shared.UUID, _ string, _ string) error {
	return nil
}

func (f *invTestQueue) RemoveForNode(_ context.Context, nodeID shared.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedNodes = append(f.removedNodes, nodeID)
	return nil
}

func (f *invTestQueue) ListOrphanedClaims(_ context.Context, _ time.Time) ([]shared.DispatchRow, error) {
	return nil, nil
}
func (f *invTestQueue) ReleaseClaim(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *invTestQueue) GetClaimedBy(_ context.Context, _ shared.UUID) (queue.ClaimOwnership, error) {
	return queue.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *invTestQueue) RefreshHeartbeat(_ context.Context, _ string) error { return nil }

func (f *invTestQueue) snapshot() ([]queue.DispatchRequest, []shared.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	eq := make([]queue.DispatchRequest, len(f.enqueued))
	copy(eq, f.enqueued)
	rm := make([]shared.UUID, len(f.removedNodes))
	copy(rm, f.removedNodes)
	return eq, rm
}

var _ queue.DispatchQueue = (*invTestQueue)(nil)

// --- Fixtures ---------------------------------------------------------

type fixture struct {
	b        *pgstorage.PostgresStorageBackend
	pool     *pgxpool.Pool
	q        *invTestQueue
	clock    shared.Clock
	log      shared.Logger
	instance storage.InstanceRow
}

func newFixture(t *testing.T) (*fixture, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	b := pgstorage.New(pool)

	tpl, err := b.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: "sched-test-" + uuid.NewString(), Version: "v1",
		FrameResolution: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:  nodepkg.FrameTimeoutDefaultMs,
		Nodes:           []nodepkg.TemplateNodeDef{},
	}, nil)
	require.NoError(t, err)

	inst, err := b.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: tpl.ID, ConsumerKey: "ck-" + uuid.NewString(),
		Params: map[string]any{},
	}, nil)
	require.NoError(t, err)

	return &fixture{
		b:        b,
		pool:     pool,
		q:        newInvTestQueue(),
		clock:    shared.SystemClock{},
		log:      shared.SilentLogger{},
		instance: inst,
	}, teardown
}

// createNodeInState inserts a node, then forces its state via a direct SQL
// UPDATE so the test can exercise specific state paths without routing
// through the state machine's legal-transition constraints. Stale/running
// nodes get a frame_id so the dispatch enqueue path satisfies blessed-
// invariant 19.
func (f *fixture) createNodeInState(t *testing.T, executor string, state shared.NodeState, deps ...shared.UUID) storage.NodeRow {
	t.Helper()
	ctx := context.Background()
	n, err := f.b.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance.ID, NodeType: "t",
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(t, err)

	// Always UPDATE: Create() now defaults to 'fresh' (frame-resolution
	// model), so any test asking for a non-fresh state must override.
	_, err = f.pool.Exec(ctx,
		`UPDATE rimsky_nodes SET state = $1 WHERE id = $2`, string(state), n.ID)
	require.NoError(t, err)
	n.State = state
	if state == shared.NodeStateStale || state == shared.NodeStateRunning {
		// Reuse the existing running frame for this instance if any (the
		// uq_rimsky_frames_running partial unique index limits one running
		// frame per instance); otherwise insert a fresh one.
		var frameID shared.UUID
		err := f.pool.QueryRow(ctx, `
            SELECT frame_id FROM rimsky_frames
            WHERE instance_id = $1 AND state = 'running'
            LIMIT 1
        `, f.instance.ID).Scan(&frameID)
		if err != nil {
			require.NoError(t, f.pool.QueryRow(ctx, `
                INSERT INTO rimsky_frames
                    (instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
                VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
                RETURNING frame_id
            `, f.instance.ID, n.ID).Scan(&frameID))
		}
		_, err = f.pool.Exec(ctx, `UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, n.ID)
		require.NoError(t, err)
		n.FrameID = &frameID
	}
	return n
}

// --- InvalidateNode tests ---------------------------------------------
//
// Under the frame-resolution model
// (docs/specs/2026-04-26-frame-resolution-design.md), InvalidateNode no
// longer mutates rimsky_nodes.state. It enqueues a rimsky_frames row
// (or coalesces into a pending one), and the scheduler tick's frame
// engine advances the frame to running, marking sources stale at that
// time.

func TestInvalidateNode_EnqueuesFrameAndEmitsEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	parent := f.createNodeInState(t, "worker", shared.NodeStateFresh)

	err := InvalidateNode(ctx, InvalidateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: parent.ID,
		Reason:       "test_kick",
	})
	require.NoError(t, err)

	// Source node remains fresh until the frame engine advances the frame.
	p, err := f.b.Nodes().Get(ctx, parent.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, p.State)

	// A queued frame row exists with this node as source.
	var (
		count   int
		state   string
		hasNode bool
	)
	require.NoError(t, f.pool.QueryRow(ctx, `
        SELECT COUNT(*), MAX(state), bool_or($2 = ANY(source_node_ids))
        FROM rimsky_frames WHERE instance_id = $1
    `, f.instance.ID, parent.ID).Scan(&count, &state, &hasNode))
	require.Equal(t, 1, count)
	require.Equal(t, "queued", state)
	require.True(t, hasNode)

	// Audit events were appended.
	events, err := f.b.Events().List(ctx, storage.EventListFilter{NodeID: &parent.ID},
		storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	kinds := map[string]int{}
	for _, e := range events.Events {
		kinds[e.Kind]++
	}
	require.GreaterOrEqual(t, kinds["message_emitted"], 1)
	require.GreaterOrEqual(t, kinds["message_received"], 1)
}

func TestInvalidateNode_TargetMissing_NoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	missing := uuid.New()
	err := InvalidateNode(ctx, InvalidateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: missing, Reason: "ghost",
	})
	require.NoError(t, err)

	var count int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1`,
		f.instance.ID).Scan(&count))
	require.Equal(t, 0, count)
}

// --- RecalculateNode tests --------------------------------------------

func TestRecalculateNode_FreshTarget_IsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	n := f.createNodeInState(t, "worker", shared.NodeStateFresh)

	err := RecalculateNode(ctx, RecalculateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: n.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Empty(t, eq)

	after, err := f.b.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, after.State)
}

func TestRecalculateNode_StaleWithUnmetDep_IsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	// Dep is stale → target is not ready.
	dep := f.createNodeInState(t, "worker", shared.NodeStateStale)
	target := f.createNodeInState(t, "worker", shared.NodeStateStale, dep.ID)

	err := RecalculateNode(ctx, RecalculateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Empty(t, eq)
}

func TestRecalculateNode_StaleWithAllDepsFreshAndExecutor_EnqueuesDispatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	dep := f.createNodeInState(t, "worker", shared.NodeStateFresh)
	target := f.createNodeInState(t, "runner", shared.NodeStateStale, dep.ID)

	err := RecalculateNode(ctx, RecalculateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Len(t, eq, 1)
	require.Equal(t, target.ID, eq[0].NodeID)
	require.Equal(t, "runner", eq[0].ExecutorName)
}

func TestRecalculateNode_StaleWithAllDepsFreshButNoExecutor_NoEnqueue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	dep := f.createNodeInState(t, "worker", shared.NodeStateFresh)
	// Empty executor → pure-cascade node; the scheduler sweep handles it.
	target := f.createNodeInState(t, "", shared.NodeStateStale, dep.ID)

	err := RecalculateNode(ctx, RecalculateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Empty(t, eq)
}
