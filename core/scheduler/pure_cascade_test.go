// Tests for ProcessPureCascade. Uses the real persistence.Driver via
// pgtest (same pattern as invalidate_test.go) and a lightweight fake
// persistence.Queue so assertions can inspect exactly what propagated.
package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

// --- Fake persistence.Queue (pure-cascade-local; invalidate_test.go has its own)

type fakeQueue struct {
	mu       sync.Mutex
	enqueued []persistence.DispatchRequest
}

func (f *fakeQueue) Enqueue(_ context.Context, req persistence.DispatchRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, req)
	return nil
}
func (f *fakeQueue) EnqueueInTx(_ context.Context, req persistence.DispatchRequest, _ persistence.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, req)
	return nil
}
func (f *fakeQueue) SelectCandidates(_ context.Context, _ persistence.Tx, _ persistence.SelectCandidatesRequest) ([]persistence.Candidate, error) {
	return nil, nil
}
func (f *fakeQueue) ClaimDispatchRow(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string) (bool, error) {
	return false, nil
}
func (f *fakeQueue) Complete(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *fakeQueue) RemoveForNode(_ context.Context, _ shared.UUID, _ string) error {
	return nil
}
func (f *fakeQueue) RemoveForNodeInTx(_ context.Context, _ shared.UUID, _ string, _ persistence.Tx) error {
	return nil
}
func (f *fakeQueue) ListOrphanedClaims(_ context.Context, _ time.Time) ([]shared.DispatchRow, error) {
	return nil, nil
}
func (f *fakeQueue) ReleaseClaim(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *fakeQueue) GetClaimedBy(_ context.Context, _ shared.UUID) (persistence.ClaimOwnership, error) {
	return persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *fakeQueue) GetDispatchNode(_ context.Context, _ shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *fakeQueue) RefreshHeartbeat(_ context.Context, _ string) error { return nil }
func (f *fakeQueue) ListLive(_ context.Context, _ persistence.DispatchListFilter, _ persistence.ListPagination) (persistence.PaginatedListResult[shared.DispatchRow], error) {
	return persistence.PaginatedListResult[shared.DispatchRow]{}, nil
}
func (f *fakeQueue) CountLive(_ context.Context, _ persistence.DispatchListFilter) (int, error) {
	return 0, nil
}

func (f *fakeQueue) snapshot() []persistence.DispatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]persistence.DispatchRequest, len(f.enqueued))
	copy(out, f.enqueued)
	return out
}

var _ persistence.Queue = (*fakeQueue)(nil)

// --- Local fixture helpers --------------------------------------------------

type pcFixture struct {
	persist persistence.Store
	driver  persistence.Driver
}

func newPureCascadeFixture(t *testing.T) *pcFixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	return &pcFixture{persist: d.Store(), driver: d}
}

func pcDeployTemplate(ctx context.Context, t *testing.T, b persistence.Store, name string) persistence.TemplateRow {
	t.Helper()
	return insertDeployedTemplate(ctx, t, b, nodepkg.TemplateSpec{
		Name: name, Version: "v1", Description: "test",
		FrameResolution: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:  nodepkg.FrameTimeoutDefaultMs,
		Nodes:           []nodepkg.TemplateNodeDef{},
	})
}

func pcCreateInstance(ctx context.Context, t *testing.T, b persistence.Store, templateHash string, ck string) persistence.InstanceRow {
	t.Helper()
	ckCopy := ck
	inst, err := b.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID: uuid.New(), TemplateHash: templateHash, InstanceKey: &ckCopy, Params: map[string]any{},
	}, nil)
	require.NoError(t, err)
	return inst
}

// pcCreateNode creates a node and forces it to 'stale'. Under the frame
// model Create() defaults to 'fresh'; pure-cascade tests need an
// in-flight stale source to exercise ProcessPureCascade.
func pcCreateNode(ctx context.Context, t *testing.T, f *pcFixture, instanceID shared.UUID, executor string, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	n, err := f.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID: uuid.New(), InstanceID: instanceID, NodeType: "t",
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(t, err)
	forceState(ctx, t, f, n.ID, "stale")
	n.State = shared.NodeStateStale
	return n
}

// forceState bypasses the state machine and writes a literal state via
// pgtest.ExecForTest. The pure-cascade tests need a stale node at
// create time even though Create() defaults to fresh.
func forceState(ctx context.Context, t *testing.T, f *pcFixture, id shared.UUID, state string) {
	t.Helper()
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_nodes SET state = $1 WHERE id = $2`, state, id)
}

// pcSeedFrame inserts a running rimsky_frames row for the given instance,
// assigns the frame_id to the given node, and returns the frame id. Used
// to satisfy blessed-invariant 19 (no NULL frame_id on in-flight dispatch
// enqueue) for tests that drive ProcessPureCascade against pre-existing
// stale nodes.
func pcSeedFrame(ctx context.Context, t *testing.T, f *pcFixture, instanceID, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var frameID shared.UUID
	pgtest.QueryRowForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
        VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
        RETURNING frame_id
    `, []any{instanceID, nodeID}, &frameID)
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, nodeID)
	return frameID
}

func pcArgs(b persistence.Store, q *fakeQueue) PureCascadeArgs {
	return PureCascadeArgs{
		Persist: b, Queue: q, Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}
}

// --- Tests -----------------------------------------------------------------

func TestProcessPureCascade_NoReady_ReturnsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "empty")
	_ = pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-0")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.Empty(t, q.snapshot())
}

func TestProcessPureCascade_SingleReady_TransitionsToFreshAndLogsCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "alpha")
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")
	// Pure-cascade node with no deps → starts stale, trivially ready.
	pure := pcCreateNode(ctx, t, f, inst.ID, "")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// State transitioned to fresh.
	got, err := f.persist.Nodes().Get(ctx, pure.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, shared.NodeStateFresh, got.State)

	// pure_cascade_commit event logged with correct node + instance.
	evs, err := f.persist.Events().List(ctx, persistence.EventListFilter{
		NodeID: &pure.ID, Kind: "pure_cascade_commit",
	}, persistence.ListPagination{Limit: 100}, nil)
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
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "alpha")
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")
	// Executor-having node: stale, deps trivially fresh, but has an executor
	// → ListPureCascadeReady must not pick it up.
	execNode := pcCreateNode(ctx, t, f, inst.ID, "worker")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	got, err := f.persist.Nodes().Get(ctx, execNode.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shared.NodeStateStale, got.State)

	evs, err := f.persist.Events().List(ctx, persistence.EventListFilter{
		NodeID: &execNode.ID, Kind: "pure_cascade_commit",
	}, persistence.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	assert.Empty(t, evs.Events)

	// And no one else transitioned or enqueued.
	assert.Empty(t, q.snapshot())
}

// TestProcessPureCascade_NativeClaimOnly_Enqueues pins the §7.3 step 4b
// branch: an empty-executor node whose template node-def declares at
// least one store with claim=true is treated as native claim-only — the
// scheduler enqueues it onto the dispatch queue with the template's
// RequiredStores, leaves the node stale, and does NOT log
// pure_cascade_commit. The supervisor's omnibus runner takes it from
// there.
func TestProcessPureCascade_NativeClaimOnly_Enqueues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	// Template has one node def whose Stores include a claim-true entry.
	sum := insertDeployedTemplate(ctx, t, f.persist, nodepkg.TemplateSpec{
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
	})
	inst := pcCreateInstance(ctx, t, f.persist, sum.ID, "ck-claim")
	claimNode := pcCreateNode(ctx, t, f, inst.ID, "")
	// Seed a running frame and assign claimNode.frame_id so the dispatch
	// enqueue path can satisfy blessed-invariant 19.
	pcSeedFrame(ctx, t, f, inst.ID, claimNode.ID)

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Node stays stale — supervisor's omnibus runner will drive it.
	got, err := f.persist.Nodes().Get(ctx, claimNode.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shared.NodeStateStale, got.State)

	// One enqueue with empty ExecutorName and the template's RequiredStores.
	enq := q.snapshot()
	require.Len(t, enq, 1)
	assert.Equal(t, claimNode.ID, enq[0].NodeID)
	assert.Equal(t, "", enq[0].ExecutorName)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, enq[0].RequiredStores)

	// No pure_cascade_commit event for native claim-only nodes.
	evs, err := f.persist.Events().List(ctx, persistence.EventListFilter{
		NodeID: &claimNode.ID, Kind: "pure_cascade_commit",
	}, persistence.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	assert.Empty(t, evs.Events)
}

func TestProcessPureCascade_CascadesToDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "alpha")
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")

	// A: pure cascade, no deps. B: executor "worker", depends on A.
	// Before sweep: A=stale, B=stale (dep A stale). Sweep flips A → fresh,
	// then emits recalculate to B; B's dep is now fresh so the recalculate
	// enqueues B onto the dispatch queue.
	pureA := pcCreateNode(ctx, t, f, inst.ID, "")
	execB := pcCreateNode(ctx, t, f, inst.ID, "worker", pureA.ID)
	// Seed a frame for both (B is the one that gets enqueued).
	pcSeedFrame(ctx, t, f, inst.ID, execB.ID)

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// A is fresh.
	gotA, err := f.persist.Nodes().Get(ctx, pureA.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shared.NodeStateFresh, gotA.State)

	// B was enqueued by the recalculate path.
	enq := q.snapshot()
	require.Len(t, enq, 1)
	assert.Equal(t, execB.ID, enq[0].NodeID)
	assert.Equal(t, "worker", enq[0].ExecutorName)

	// pure_cascade_commit logged for A only.
	evs, err := f.persist.Events().List(ctx, persistence.EventListFilter{
		Kind: "pure_cascade_commit",
	}, persistence.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	require.Len(t, evs.Events, 1)
	require.NotNil(t, evs.Events[0].NodeID)
	assert.Equal(t, pureA.ID, *evs.Events[0].NodeID)
}
