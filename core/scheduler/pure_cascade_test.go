// Tests for ProcessPureCascade. Uses the real Postgres storage backend via
// pgtest (same pattern as invalidate_test.go + core/storage/postgres tests)
// and a lightweight fake DispatchQueue so assertions can inspect exactly
// what propagated.
package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// --- Fake DispatchQueue (pure-cascade-local; invalidate_test.go has its own)

type fakeQueue struct {
	mu       sync.Mutex
	enqueued []queue.DispatchRequest
}

func (f *fakeQueue) Enqueue(_ context.Context, req queue.DispatchRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, req)
	return nil
}
func (f *fakeQueue) SelectCandidates(_ context.Context, _ pgx.Tx, _ queue.SelectCandidatesRequest) ([]queue.Candidate, error) {
	return nil, nil
}
func (f *fakeQueue) ClaimDispatchRow(_ context.Context, _ pgx.Tx, _ shared.UUID, _ string) (bool, error) {
	return false, nil
}
func (f *fakeQueue) Complete(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *fakeQueue) Fail(_ context.Context, _ shared.UUID, _ string, _ string) error {
	return nil
}
func (f *fakeQueue) RemoveForNode(_ context.Context, _ shared.UUID, _ string) error {
	return nil
}
func (f *fakeQueue) ListOrphanedClaims(_ context.Context, _ time.Time) ([]shared.DispatchRow, error) {
	return nil, nil
}
func (f *fakeQueue) ReleaseClaim(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *fakeQueue) GetClaimedBy(_ context.Context, _ shared.UUID) (queue.ClaimOwnership, error) {
	return queue.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *fakeQueue) RefreshHeartbeat(_ context.Context, _ string) error { return nil }

func (f *fakeQueue) snapshot() []queue.DispatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]queue.DispatchRequest, len(f.enqueued))
	copy(out, f.enqueued)
	return out
}

var _ queue.DispatchQueue = (*fakeQueue)(nil)

// --- Local fixture helpers --------------------------------------------------

func newPureCascadeBackend(t *testing.T) (*pgstorage.PostgresStorageBackend, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	return pgstorage.New(pool), teardown
}

func pcDeployTemplate(ctx context.Context, t *testing.T, b *pgstorage.PostgresStorageBackend, name string) storage.TemplateSummary {
	t.Helper()
	sum, err := b.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: name, Version: "v1", Description: "test",
		FrameResolution: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:  nodepkg.FrameTimeoutDefaultMs,
		Nodes:           []nodepkg.TemplateNodeDef{},
	}, nil)
	require.NoError(t, err)
	return sum
}

func pcCreateInstance(ctx context.Context, t *testing.T, b *pgstorage.PostgresStorageBackend, tplID shared.UUID, ck string) storage.InstanceRow {
	t.Helper()
	inst, err := b.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: tplID, ConsumerKey: ck, Params: map[string]any{},
	}, nil)
	require.NoError(t, err)
	return inst
}

// pcCreateNode creates a node and forces it to 'stale'. Under the frame
// model Create() defaults to 'fresh'; pure-cascade tests need an
// in-flight stale source to exercise ProcessPureCascade.
func pcCreateNode(ctx context.Context, t *testing.T, b *pgstorage.PostgresStorageBackend, instanceID shared.UUID, executor string, deps ...shared.UUID) storage.NodeRow {
	t.Helper()
	n, err := b.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: instanceID, NodeType: "t",
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(t, err)
	if err := b.Transaction(ctx, func(ctx context.Context, stx storage.Tx) error {
		pgT, err := pgstorage.PgxTxFromStorage(stx)
		if err != nil {
			return err
		}
		_, err = pgT.Exec(ctx, `UPDATE rimsky_nodes SET state = 'stale' WHERE id = $1`, n.ID)
		return err
	}); err != nil {
		t.Fatalf("pcCreateNode: force stale: %v", err)
	}
	n.State = shared.NodeStateStale
	return n
}

// pcSeedFrame inserts a running rimsky_frames row for the given instance,
// assigns the frame_id to the given node, and returns the frame id. Used
// to satisfy blessed-invariant 19 (no NULL frame_id on in-flight dispatch
// enqueue) for tests that drive ProcessPureCascade against pre-existing
// stale nodes.
func pcSeedFrame(ctx context.Context, t *testing.T, b *pgstorage.PostgresStorageBackend, instanceID, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var frameID shared.UUID
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, stx storage.Tx) error {
		pgT, err := pgstorage.PgxTxFromStorage(stx)
		if err != nil {
			return err
		}
		if err := pgT.QueryRow(ctx, `
            INSERT INTO rimsky_frames
                (instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
            VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
            RETURNING frame_id
        `, instanceID, nodeID).Scan(&frameID); err != nil {
			return err
		}
		_, err = pgT.Exec(ctx, `UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, nodeID)
		return err
	}))
	return frameID
}

func pcArgs(b *pgstorage.PostgresStorageBackend, q *fakeQueue) PureCascadeArgs {
	return PureCascadeArgs{
		Storage: b, Queue: q, Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}
}

// --- Tests -----------------------------------------------------------------

func TestProcessPureCascade_NoReady_ReturnsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, teardown := newPureCascadeBackend(t)
	t.Cleanup(teardown)

	tpl := pcDeployTemplate(ctx, t, b, "empty")
	_ = pcCreateInstance(ctx, t, b, tpl.ID, "ck-0")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(b, q))
	require.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.Empty(t, q.snapshot())
}

func TestProcessPureCascade_SingleReady_TransitionsToFreshAndLogsCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, teardown := newPureCascadeBackend(t)
	t.Cleanup(teardown)

	tpl := pcDeployTemplate(ctx, t, b, "alpha")
	inst := pcCreateInstance(ctx, t, b, tpl.ID, "ck-1")
	// Pure-cascade node with no deps → starts stale, trivially ready.
	pure := pcCreateNode(ctx, t, b, inst.ID, "")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(b, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// State transitioned to fresh.
	got, err := b.Nodes().Get(ctx, pure.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, shared.NodeStateFresh, got.State)

	// pure_cascade_commit event logged with correct node + instance.
	evs, err := b.Events().List(ctx, storage.EventListFilter{
		NodeID: &pure.ID, Kind: "pure_cascade_commit",
	}, storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	require.Len(t, evs.Events, 1)
	require.NotNil(t, evs.Events[0].NodeID)
	assert.Equal(t, pure.ID, *evs.Events[0].NodeID)
	require.NotNil(t, evs.Events[0].InstanceID)
	assert.Equal(t, inst.ID, *evs.Events[0].InstanceID)

	// Pure-cascade nodes never enqueue — no dispatch rows.
	assert.Empty(t, q.snapshot())
}

func TestProcessPureCascade_WithExecutorNodeIsSkipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, teardown := newPureCascadeBackend(t)
	t.Cleanup(teardown)

	tpl := pcDeployTemplate(ctx, t, b, "alpha")
	inst := pcCreateInstance(ctx, t, b, tpl.ID, "ck-1")
	// Executor-having node: stale, deps trivially fresh, but has an executor
	// → ListPureCascadeReady must not pick it up.
	execNode := pcCreateNode(ctx, t, b, inst.ID, "worker")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(b, q))
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	got, err := b.Nodes().Get(ctx, execNode.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shared.NodeStateStale, got.State)

	evs, err := b.Events().List(ctx, storage.EventListFilter{
		NodeID: &execNode.ID, Kind: "pure_cascade_commit",
	}, storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	assert.Empty(t, evs.Events)

	// And no one else transitioned or enqueued.
	assert.Empty(t, q.snapshot())
}

// TestProcessPureCascade_NativeClaimOnly_Enqueues pins the §17.1 step 4b
// branch: an empty-executor node whose template node-def declares at
// least one store with claim=true is treated as native claim-only — the
// scheduler enqueues it onto the dispatch queue with the template's
// RequiredStores, leaves the node stale, and does NOT log
// pure_cascade_commit. The supervisor's omnibus runner takes it from
// there.
func TestProcessPureCascade_NativeClaimOnly_Enqueues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, teardown := newPureCascadeBackend(t)
	t.Cleanup(teardown)

	// Template has one node def whose Stores include a claim-true entry.
	sum, err := b.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: "claim-only", Version: "v1", Description: "test",
		FrameResolution: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:  nodepkg.FrameTimeoutDefaultMs,
		Nodes: []nodepkg.TemplateNodeDef{{
			Type:     "t",
			Executor: "",
			Stores: []nodepkg.NodeStoreRef{
				{Name: "alpha", Selector: "x", Intent: "rw"},
				{Name: "beta", Selector: "y", Intent: "r"},
			},
		}},
	}, nil)
	require.NoError(t, err)
	inst := pcCreateInstance(ctx, t, b, sum.ID, "ck-claim")
	claimNode := pcCreateNode(ctx, t, b, inst.ID, "")
	// Seed a running frame and assign claimNode.frame_id so the dispatch
	// enqueue path can satisfy blessed-invariant 19.
	pcSeedFrame(ctx, t, b, inst.ID, claimNode.ID)

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(b, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Node stays stale — supervisor's omnibus runner will drive it.
	got, err := b.Nodes().Get(ctx, claimNode.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shared.NodeStateStale, got.State)

	// One enqueue with empty ExecutorName and the template's RequiredStores.
	enq := q.snapshot()
	require.Len(t, enq, 1)
	assert.Equal(t, claimNode.ID, enq[0].NodeID)
	assert.Equal(t, "", enq[0].ExecutorName)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, enq[0].RequiredStores)

	// No pure_cascade_commit event for native claim-only nodes.
	evs, err := b.Events().List(ctx, storage.EventListFilter{
		NodeID: &claimNode.ID, Kind: "pure_cascade_commit",
	}, storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	assert.Empty(t, evs.Events)
}

func TestProcessPureCascade_CascadesToDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, teardown := newPureCascadeBackend(t)
	t.Cleanup(teardown)

	tpl := pcDeployTemplate(ctx, t, b, "alpha")
	inst := pcCreateInstance(ctx, t, b, tpl.ID, "ck-1")

	// A: pure cascade, no deps. B: executor "worker", depends on A.
	// Before sweep: A=stale, B=stale (dep A stale). Sweep flips A → fresh,
	// then emits recalculate to B; B's dep is now fresh so the recalculate
	// enqueues B onto the dispatch queue.
	pureA := pcCreateNode(ctx, t, b, inst.ID, "")
	execB := pcCreateNode(ctx, t, b, inst.ID, "worker", pureA.ID)
	// Seed a frame for both (B is the one that gets enqueued).
	pcSeedFrame(ctx, t, b, inst.ID, execB.ID)

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(b, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// A is fresh.
	gotA, err := b.Nodes().Get(ctx, pureA.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shared.NodeStateFresh, gotA.State)

	// B was enqueued by the recalculate path.
	enq := q.snapshot()
	require.Len(t, enq, 1)
	assert.Equal(t, execB.ID, enq[0].NodeID)
	assert.Equal(t, "worker", enq[0].ExecutorName)

	// pure_cascade_commit logged for A only.
	evs, err := b.Events().List(ctx, storage.EventListFilter{
		Kind: "pure_cascade_commit",
	}, storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	require.Len(t, evs.Events, 1)
	require.NotNil(t, evs.Events[0].NodeID)
	assert.Equal(t, pureA.ID, *evs.Events[0].NodeID)
}
