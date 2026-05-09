// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Tests for InvalidateNode + RecalculateNode. Backed by the real
// persistence.Driver via the pgtest harness; a lightweight in-memory
// fake satisfies persistence.Queue so the test can assert on dispatch
// behavior without depending on the Postgres queue implementation.
package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/internal/pgtest"
	"github.com/fallguy/rimsky/foundation/persistence"
	nodepkg "github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// --- In-memory fake persistence.Queue ----------------------------------
// Named invTestQueue to avoid colliding with fakeQueue in pure_cascade_test.go
// (same package). This variant additionally records RemoveForNode calls.

type invTestQueue struct {
	mu           sync.Mutex
	enqueued     []persistence.DispatchRequest
	removedNodes []shared.UUID
}

func newInvTestQueue() *invTestQueue { return &invTestQueue{} }

func (f *invTestQueue) Enqueue(_ context.Context, req persistence.DispatchRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, req)
	return nil
}

func (f *invTestQueue) EnqueueInTx(_ context.Context, req persistence.DispatchRequest, _ persistence.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, req)
	return nil
}

func (f *invTestQueue) SelectCandidates(_ context.Context, _ persistence.Tx, _ persistence.SelectCandidatesRequest) ([]persistence.Candidate, error) {
	return nil, nil
}

func (f *invTestQueue) ClaimDispatchRow(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string) (bool, error) {
	return false, nil
}

func (f *invTestQueue) Complete(_ context.Context, _ shared.UUID, _ string) error { return nil }

func (f *invTestQueue) RemoveForNode(_ context.Context, nodeID shared.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedNodes = append(f.removedNodes, nodeID)
	return nil
}

func (f *invTestQueue) RemoveForNodeInTx(_ context.Context, nodeID shared.UUID, _ string, _ persistence.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedNodes = append(f.removedNodes, nodeID)
	return nil
}

func (f *invTestQueue) ListOrphanedClaims(_ context.Context, _ time.Time) ([]shared.DispatchRow, error) {
	return nil, nil
}
func (f *invTestQueue) ReleaseClaim(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *invTestQueue) GetClaimedBy(_ context.Context, _ shared.UUID) (persistence.ClaimOwnership, error) {
	return persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *invTestQueue) GetDispatchNode(_ context.Context, _ shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *invTestQueue) RefreshHeartbeat(_ context.Context, _ string) error { return nil }
func (f *invTestQueue) ListLive(_ context.Context, _ persistence.DispatchListFilter, _ persistence.ListPagination) (persistence.PaginatedListResult[shared.DispatchRow], error) {
	return persistence.PaginatedListResult[shared.DispatchRow]{}, nil
}
func (f *invTestQueue) CountLive(_ context.Context, _ persistence.DispatchListFilter) (int, error) {
	return 0, nil
}
func (f *invTestQueue) GetByID(_ context.Context, _ shared.UUID) (*shared.DispatchRow, error) {
	return nil, nil
}

// Park-lifecycle helpers for the 2026-05-08 platform-extensions plan.
// invTestQueue is the cascade-invalidate test fixture; the parked
// helpers are no-ops since these tests don't park nodes.
func (f *invTestQueue) ParkActiveInTx(_ context.Context, _ persistence.Tx, _ persistence.ParkActiveInput) error {
	return nil
}
func (f *invTestQueue) ListParkedReadyForResume(_ context.Context, _ time.Time, _ int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *invTestQueue) ListParkedOverdue(_ context.Context, _ time.Time, _ int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *invTestQueue) GetParkedByNode(_ context.Context, _ shared.UUID) (*persistence.ParkedRow, error) {
	return nil, nil
}
func (f *invTestQueue) ResumeParkedInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _, _ string) (bool, error) {
	return false, nil
}
func (f *invTestQueue) GetRetryNoProgress(_ context.Context, _ shared.UUID) (int, *int, error) {
	return 0, nil, nil
}
func (f *invTestQueue) SetRetryNoProgressForNodeInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ int) error {
	return nil
}
func (f *invTestQueue) UpdateDispatchTuningInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ *int, _ *int) error {
	return nil
}
func (f *invTestQueue) LoadResumeMetadataInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) (*persistence.ResumeMetadataRow, error) {
	return nil, nil
}
func (f *invTestQueue) CountParkedByReason(_ context.Context) (map[string]int, error) {
	return nil, nil
}
func (f *invTestQueue) ClearResumeMetadataInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) error {
	return nil
}

func (f *invTestQueue) snapshot() ([]persistence.DispatchRequest, []shared.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	eq := make([]persistence.DispatchRequest, len(f.enqueued))
	copy(eq, f.enqueued)
	rm := make([]shared.UUID, len(f.removedNodes))
	copy(rm, f.removedNodes)
	return eq, rm
}

var _ persistence.Queue = (*invTestQueue)(nil)

// --- Fixtures ---------------------------------------------------------

type fixture struct {
	driver   persistence.Driver
	persist  persistence.Store
	q        *invTestQueue
	clock    shared.Clock
	log      shared.Logger
	instance persistence.InstanceRow
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	tpl := insertDeployedTemplate(ctx, t, d.Store(), nodepkg.TemplateSpec{
		Name: "sched-test-" + uuid.NewString(), Version: "v1",
		FrameResolution: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:  nodepkg.FrameTimeoutDefaultMs,
		Nodes:           []nodepkg.TemplateNodeDef{},
	})

	ck := "ck-" + uuid.NewString()
	var inst persistence.InstanceRow
	require.NoError(t, d.Store().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := d.Store().Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: uuid.New(), TemplateHash: tpl.ID, InstanceKey: &ck,
			Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = row
		return nil
	}))

	return &fixture{
		driver:   d,
		persist:  d.Store(),
		q:        newInvTestQueue(),
		clock:    shared.SystemClock{},
		log:      shared.SilentLogger{},
		instance: inst,
	}
}

// createNodeInState inserts a node, then forces its state via a direct SQL
// UPDATE so the test can exercise specific state paths without routing
// through the state machine's legal-transition constraints. Stale/running
// nodes get a frame_id so the dispatch enqueue path satisfies blessed-
// invariant 19.
func (f *fixture) createNodeInState(t *testing.T, executor string, state shared.NodeState, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	ctx := context.Background()
	var n persistence.NodeRow
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := f.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: uuid.New(), InstanceID: f.instance.ID, NodeType: "t",
			Executor: executor, Dependencies: deps,
		}, tx)
		if err != nil {
			return err
		}
		n = row
		return nil
	}))

	// Always UPDATE: Create() now defaults to 'fresh' (frame-resolution
	// model), so any test asking for a non-fresh state must override.
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_nodes SET state = $1 WHERE id = $2`, string(state), n.ID)
	n.State = state
	if state == shared.NodeStateStale || state == shared.NodeStateRunning {
		// Reuse the existing running frame for this instance if any (the
		// uq_rimsky_frames_running partial unique index limits one running
		// frame per instance); otherwise insert a fresh one.
		var count int
		pgtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND state = 'running'`,
			[]any{f.instance.ID}, &count)
		var frameID shared.UUID
		if count == 0 {
			pgtest.QueryRowForTest(ctx, t, f.driver, `
                INSERT INTO rimsky_frames
                    (instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
                VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
                RETURNING frame_id
            `, []any{f.instance.ID, n.ID}, &frameID)
		} else {
			pgtest.QueryRowForTest(ctx, t, f.driver, `
                SELECT frame_id FROM rimsky_frames
                WHERE instance_id = $1 AND state = 'running'
                LIMIT 1
            `, []any{f.instance.ID}, &frameID)
		}
		pgtest.ExecForTest(ctx, t, f.driver,
			`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, n.ID)
		n.FrameID = &frameID
	}
	return n
}

// --- InvalidateNode tests ---------------------------------------------
//
// Under the frame-resolution model
// (docs/history/2026-04-26-frame-resolution-design.md), InvalidateNode no
// longer mutates rimsky_nodes.state. It enqueues a rimsky_frames row
// (or coalesces into a pending one), and the scheduler tick's frame
// engine advances the frame to running, marking sources stale at that
// time.

func TestInvalidateNode_EnqueuesFrameAndEmitsEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	parent := f.createNodeInState(t, "worker", shared.NodeStateFresh)

	err := integration.InvalidateNode(ctx, integration.InvalidateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: parent.ID,
		Reason:       "test_kick",
	})
	require.NoError(t, err)

	// Source node remains fresh until the frame engine advances the frame.
	var p *persistence.NodeRow
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.persist.Nodes().Get(ctx, parent.ID, tx)
		p = r
		return err
	}))
	require.Equal(t, shared.NodeStateFresh, p.State)

	// A queued frame row exists with this node as source.
	var (
		count   int
		state   string
		hasNode bool
	)
	pgtest.QueryRowForTest(ctx, t, f.driver, `
        SELECT COUNT(*), MAX(state), bool_or($2 = ANY(source_node_ids))
        FROM rimsky_frames WHERE instance_id = $1
    `, []any{f.instance.ID, parent.ID}, &count, &state, &hasNode)
	require.Equal(t, 1, count)
	require.Equal(t, "queued", state)
	require.True(t, hasNode)

	// Audit events were appended.
	var events persistence.EventListResult
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{NodeID: &parent.ID},
			persistence.ListPagination{Limit: 100}, tx)
		events = r
		return err
	}))
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
	f := newFixture(t)

	missing := uuid.New()
	err := integration.InvalidateNode(ctx, integration.InvalidateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: missing, Reason: "ghost",
	})
	require.NoError(t, err)

	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{f.instance.ID}, &count)
	require.Equal(t, 0, count)
}

// --- RecalculateNode tests --------------------------------------------

func TestRecalculateNode_FreshTarget_IsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	n := f.createNodeInState(t, "worker", shared.NodeStateFresh)

	err := integration.RecalculateNode(ctx, integration.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: n.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Empty(t, eq)

	var after *persistence.NodeRow
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.persist.Nodes().Get(ctx, n.ID, tx)
		after = r
		return err
	}))
	require.Equal(t, shared.NodeStateFresh, after.State)
}

func TestRecalculateNode_StaleWithUnmetDep_IsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	// Dep is stale → target is not ready.
	dep := f.createNodeInState(t, "worker", shared.NodeStateStale)
	target := f.createNodeInState(t, "worker", shared.NodeStateStale, dep.ID)

	err := integration.RecalculateNode(ctx, integration.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Empty(t, eq)
}

func TestRecalculateNode_StaleWithAllDepsFreshAndExecutor_EnqueuesDispatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	dep := f.createNodeInState(t, "worker", shared.NodeStateFresh)
	target := f.createNodeInState(t, "runner", shared.NodeStateStale, dep.ID)

	err := integration.RecalculateNode(ctx, integration.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
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
	f := newFixture(t)

	dep := f.createNodeInState(t, "worker", shared.NodeStateFresh)
	// Empty executor → pure-cascade node; the scheduler sweep handles it.
	target := f.createNodeInState(t, "", shared.NodeStateStale, dep.ID)

	err := integration.RecalculateNode(ctx, integration.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Empty(t, eq)
}
