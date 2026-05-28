// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Tests for InvalidateNode + RecalculateNode. Backed by the real
// persistence.Database via the pgtest harness; a lightweight in-memory
// fake satisfies persistence.Queue so the test can assert on dispatch
// behavior without depending on the Postgres queue implementation.
package runtime_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

// --- In-memory fake persistence.Queue ----------------------------------
// Named invTestQueue to avoid colliding with fakeQueue in pure_cascade_test.go
// (same package). This variant additionally records RemoveForNode calls.

type invTestQueue struct {
	mu           sync.Mutex
	enqueued     []persistence.DispatchRequest
	removedNodes []shared.UUID
	// real, when non-nil, is the underlying postgres queue; the fake
	// delegates GetInFlightRunForNode to it so the post-stage-5 cascade
	// walker can resolve receiver / sender run ids without re-
	// implementing the SQL here.
	real persistence.Queue
}

func newInvTestQueueWithReal(real persistence.Queue) *invTestQueue {
	return &invTestQueue{real: real}
}

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

func (f *invTestQueue) RemoveForNode(_ context.Context, nodeID shared.UUID, _ shared.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedNodes = append(f.removedNodes, nodeID)
	return nil
}

func (f *invTestQueue) RemoveForNodeInTx(_ context.Context, nodeID shared.UUID, _ shared.UUID, _ string, _ persistence.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedNodes = append(f.removedNodes, nodeID)
	return nil
}

func (f *invTestQueue) ListOrphanedClaims(_ context.Context, _ time.Time) ([]persistence.DispatchRow, error) {
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
func (f *invTestQueue) ListLive(_ context.Context, _ persistence.DispatchListFilter, _ persistence.ListPagination) (persistence.PaginatedListResult[persistence.DispatchRow], error) {
	return persistence.PaginatedListResult[persistence.DispatchRow]{}, nil
}
func (f *invTestQueue) CountLive(_ context.Context, _ persistence.DispatchListFilter) (int, error) {
	return 0, nil
}
func (f *invTestQueue) GetByID(_ context.Context, _ shared.UUID) (*persistence.DispatchRow, error) {
	return nil, nil
}
func (f *invTestQueue) GetInFlightRunForNode(ctx context.Context, tx persistence.Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error) {
	if f.real != nil {
		return f.real.GetInFlightRunForNode(ctx, tx, nodeID, runScopeID)
	}
	return shared.UUID{}, false, nil
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
func (f *invTestQueue) GetParkedByNode(_ context.Context, _ shared.UUID, _ shared.UUID) (*persistence.ParkedRow, error) {
	return nil, nil
}
func (f *invTestQueue) ResumeParkedInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string) (bool, error) {
	return false, nil
}
func (f *invTestQueue) RebindRunFrameInTx(_ context.Context, _ persistence.Tx, _, _ shared.UUID) error {
	return nil
}
func (f *invTestQueue) GetRetryNoProgress(_ context.Context, _ shared.UUID) (int, *int, error) {
	return 0, nil, nil
}
func (f *invTestQueue) SetRetryNoProgressForNodeInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ shared.UUID, _ int) error {
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
func (f *invTestQueue) ListParkedDiagnostic(_ context.Context, _ persistence.Tx, _ string) ([]persistence.ParkedDiagnosticRow, error) {
	return nil, nil
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
	driver   persistence.Database
	persist  persistence.Tables
	q        *invTestQueue
	clock    shared.Clock
	log      shared.Logger
	instance persistence.InstanceRow
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	tpl := insertDeployedTemplate(ctx, t, d.Tables(), nodepkg.TemplateSpec{
		Name: "sched-test-" + uuid.NewString(), Version: "v1",
		FrameResolutionMode: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:      nodepkg.FrameTimeoutDefaultMs,
		Nodes:               []nodepkg.TemplateNodeDef{},
	})

	ck := "ck-" + uuid.NewString()
	var inst persistence.InstanceRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := d.Tables().RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instID,
		}); err != nil {
			return err
		}
		row, err := d.Tables().Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tpl.ID, InstanceKey: &ck,
			Params:         map[string]any{},
			MainRunScopeID: mainScopeID,
		}, tx)
		if err != nil {
			return err
		}
		inst = row
		return nil
	}))

	return &fixture{
		driver:   d,
		persist:  d.Tables(),
		q:        newInvTestQueueWithReal(d.Queue()),
		clock:    shared.SystemClock{},
		log:      shared.SilentLogger{},
		instance: inst,
	}
}

// createNodeInState inserts a node, then forces its state via a direct SQL
// UPDATE so the test can exercise specific state paths without routing
// through the state machine's legal-transition constraints. Stale/running
// createNodeInState seeds a node in the requested state. Post-stage-3
// cutover: state lives on rimsky_node_runs, so 'stale' / 'running' are
// seeded by inserting an in-flight run row with the desired state. The
// 'fresh' case requires only the rimsky_nodes row (no run row).
//
// nodes get a frame_id so the dispatch enqueue path satisfies blessed-
// invariant 19.
func (f *fixture) createNodeInState(t *testing.T, executor string, state cascade.NodeState, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	ctx := context.Background()
	var n persistence.NodeRow
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_ = deps // legacy: dependency-edge resolution is now via subscription-edge map
		row, err := f.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: uuid.New(), InstanceID: f.instance.ID, NodeType: "t",
			Executor: executor,
		}, tx)
		if err != nil {
			return err
		}
		n = row
		return nil
	}))
	if state == cascade.NodeStateFresh {
		return n
	}
	// Reuse existing running frame for this instance if any; otherwise
	// insert a fresh one.
	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND state = 'running'`,
		[]any{f.instance.ID}, &count)
	var frameID shared.UUID
	if count == 0 {
		pgtest.QueryRowForTest(ctx, t, f.driver, `
            INSERT INTO rimsky_frames
                (instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
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
	// Insert the in-flight run row in the requested state.
	runPhase := "pending"
	if state == cascade.NodeStateRunning {
		runPhase = "active"
	}
	pgtest.ExecForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        VALUES (gen_random_uuid(), $1, $2, ARRAY[]::text[], NOW(), $3, $4, $5, $6)
    `, n.ID, executor, runPhase, string(state), frameID, f.instance.MainRunScopeID)
	n.State = state
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

	parent := f.createNodeInState(t, "worker", cascade.NodeStateFresh)

	err := runtime.InvalidateNode(ctx, runtime.InvalidateArgs{
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
	require.Equal(t, cascade.NodeStateFresh, p.State)

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
	err := runtime.InvalidateNode(ctx, runtime.InvalidateArgs{
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

	n := f.createNodeInState(t, "worker", cascade.NodeStateFresh)

	err := runtime.RecalculateNode(ctx, runtime.RecalculateArgs{
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
	require.Equal(t, cascade.NodeStateFresh, after.State)
}

// TestRecalculateNode_StaleWithPendingWaitSet_IsNoOp asserts that a
// stale node with at least one wait-set row gating it on a sender stays
// queued: RecalculateNode is a no-op when the wait-set is non-empty.
// Under the post-2026-05-14 subscription-cascade model, the wait-set
// row is the eligibility-gate; depsness retires.
func TestRecalculateNode_StaleWithPendingWaitSet_IsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	dep := f.createNodeInState(t, "worker", cascade.NodeStateStale)
	target := f.createNodeInState(t, "worker", cascade.NodeStateStale)

	// Seed a wait-set row gating target on dep in the running frame.
	// Post-stage-5 the wait-set keys on run id, so resolve each node's
	// in-flight run id via the queue.
	require.NotNil(t, target.FrameID)
	var depRunID, targetRunID shared.UUID
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1 AND frame_id = $2`,
		[]any{dep.ID, *target.FrameID}, &depRunID)
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1 AND frame_id = $2`,
		[]any{target.ID, *target.FrameID}, &targetRunID)
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           *target.FrameID,
			ReceiverRunID:     targetRunID,
			SenderRunID:       depRunID,
			TopicKind:         "state",
			SubscriptionScope: "direct",
		}, tx)
	}))

	err := runtime.RecalculateNode(ctx, runtime.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Empty(t, eq)
}

// TestRecalculateNode_StaleWithEmptyWaitSetAndExecutor_EnqueuesDispatch
// asserts the post-drain dispatch path: once the wait-set is empty (the
// settled-state drain ran when the sender resolved), RecalculateNode
// enqueues the target for dispatch.
func TestRecalculateNode_StaleWithEmptyWaitSetAndExecutor_EnqueuesDispatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	target := f.createNodeInState(t, "runner", cascade.NodeStateStale)
	require.NotNil(t, target.FrameID)
	// No wait-set rows seeded — empty by default.

	err := runtime.RecalculateNode(ctx, runtime.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Len(t, eq, 1)
	require.Equal(t, target.ID, eq[0].NodeID)
	require.Equal(t, "runner", eq[0].ExecutorName)
}

// TestRecalculateNode_StaleNoExecutor_NoEnqueue confirms a pure-cascade
// (empty executor) node is skipped by RecalculateNode regardless of
// wait-set state — the scheduler's pure-cascade sweep handles it.
func TestRecalculateNode_StaleNoExecutor_NoEnqueue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	target := f.createNodeInState(t, "", cascade.NodeStateStale)

	err := runtime.RecalculateNode(ctx, runtime.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Empty(t, eq)
}
