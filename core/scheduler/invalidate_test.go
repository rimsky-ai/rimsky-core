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

func (f *invTestQueue) Claim(_ context.Context, _ string, _ []string, _ map[string]int) (*shared.DispatchRow, error) {
	return nil, nil
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
		Nodes: []nodepkg.TemplateNodeDef{},
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
// through the state machine's legal-transition constraints.
func (f *fixture) createNodeInState(t *testing.T, executor string, state shared.NodeState, deps ...shared.UUID) storage.NodeRow {
	t.Helper()
	ctx := context.Background()
	n, err := f.b.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance.ID, NodeType: "t",
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(t, err)

	if state != shared.NodeStateStale {
		_, err := f.pool.Exec(ctx,
			`UPDATE rimsky_nodes SET state = $1 WHERE id = $2`, string(state), n.ID)
		require.NoError(t, err)
		n.State = state
	}
	return n
}

// --- InvalidateNode tests ---------------------------------------------

func TestInvalidateNode_FreshToStale_EnqueuesEventsAndCascades(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	// Parent is fresh, child depends on parent and is fresh. Invalidating
	// parent should transition it to stale and cascade to child.
	parent := f.createNodeInState(t, "worker", shared.NodeStateFresh)
	child := f.createNodeInState(t, "worker", shared.NodeStateFresh, parent.ID)

	err := InvalidateNode(ctx, InvalidateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: parent.ID,
		Reason:       "test_kick",
	})
	require.NoError(t, err)

	// Parent + child are now stale.
	p, err := f.b.Nodes().Get(ctx, parent.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, p.State)

	c, err := f.b.Nodes().Get(ctx, child.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, c.State)

	// Events: both message_emitted and message_received were appended for
	// parent (and again for child via the cascade).
	events, err := f.b.Events().List(ctx, storage.EventListFilter{NodeID: &parent.ID},
		storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	kinds := map[string]int{}
	for _, e := range events.Events {
		kinds[e.Kind]++
	}
	require.GreaterOrEqual(t, kinds["message_emitted"], 1)
	require.GreaterOrEqual(t, kinds["message_received"], 1)

	// RemoveForNode was called at least for parent and child.
	_, removed := f.q.snapshot()
	removedSet := map[shared.UUID]bool{}
	for _, id := range removed {
		removedSet[id] = true
	}
	require.True(t, removedSet[parent.ID])
	require.True(t, removedSet[child.ID])
}

func TestInvalidateNode_AlreadyStale_IsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	n := f.createNodeInState(t, "worker", shared.NodeStateStale)

	err := InvalidateNode(ctx, InvalidateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: n.ID, Reason: "noop",
	})
	require.NoError(t, err)

	after, err := f.b.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, after.State)

	// No dispatch enqueues and no RemoveForNode call (the idempotent early
	// return skips the queue side-effect).
	eq, removed := f.q.snapshot()
	require.Empty(t, eq)
	require.Empty(t, removed)
}

func TestInvalidateNode_AlreadyRunning_IsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	n := f.createNodeInState(t, "worker", shared.NodeStateRunning)

	err := InvalidateNode(ctx, InvalidateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: n.ID, Reason: "noop",
	})
	require.NoError(t, err)

	after, err := f.b.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateRunning, after.State)

	eq, removed := f.q.snapshot()
	require.Empty(t, eq)
	require.Empty(t, removed)
}

func TestInvalidateNode_RestoreVersionPrevious_SwapsAndEmitsRecalculate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newFixture(t)
	t.Cleanup(teardown)

	// Owner + dependent. Owner owns a resource with 2 versions.
	owner := f.createNodeInState(t, "worker", shared.NodeStateFresh)
	dep := f.createNodeInState(t, "worker", shared.NodeStateStale, owner.ID)

	res, err := f.b.Resources().Create(ctx, storage.ResourceCreateInput{
		ResourcePath: []string{"a"}, OwnerNodeID: owner.ID, KeepVersions: 3,
	}, nil)
	require.NoError(t, err)
	v1, err := f.b.Resources().CommitVersion(ctx, res.ID, storage.ResourceCommitInput{
		ProducedBy: owner.ID, Data: []byte(`{"n":1}`),
	}, nil)
	require.NoError(t, err)
	v2, err := f.b.Resources().CommitVersion(ctx, res.ID, storage.ResourceCommitInput{
		ProducedBy: owner.ID, Data: []byte(`{"n":2}`),
	}, nil)
	require.NoError(t, err)
	_, _ = v1, v2

	// Invalidate with restore_version=previous.
	err = InvalidateNode(ctx, InvalidateArgs{
		Storage: f.b, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID:   owner.ID,
		Reason:         "restore",
		RestoreVersion: "previous",
	})
	require.NoError(t, err)

	// Owner was fresh; restore_version transitions it back to fresh (same
	// state via the restore_version reason in the state machine).
	afterOwner, err := f.b.Nodes().Get(ctx, owner.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, afterOwner.State)

	// Resource current version now points at v1 (previous).
	gotRes, err := f.b.Resources().Get(ctx, res.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, gotRes.CurrentVersionID)
	require.Equal(t, v1.ID, *gotRes.CurrentVersionID)

	// Dep received a recalculate event.
	depEvents, err := f.b.Events().List(ctx, storage.EventListFilter{NodeID: &dep.ID},
		storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	sawRecalc := false
	for _, e := range depEvents.Events {
		if e.Kind == "message_received" {
			if t, ok := e.Payload["type"].(string); ok && t == "recalculate" {
				sawRecalc = true
				break
			}
		}
	}
	require.True(t, sawRecalc, "dependent should have seen a recalculate message_received")
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
