// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

type invTestQueue struct {
	mu           sync.Mutex
	enqueued     []persistence.DispatchRequest
	removedNodes []shared.UUID
	real         persistence.Queue
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

func (f *invTestQueue) PromoteClaimedToRunning(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string) (bool, error) {
	return false, nil
}

func (f *invTestQueue) Complete(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *invTestQueue) ForceComplete(_ context.Context, _ shared.UUID) error      { return nil }

func (f *invTestQueue) RemoveForNodeInTx(_ context.Context, nodeID shared.UUID, _ shared.UUID, _ string, _ persistence.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedNodes = append(f.removedNodes, nodeID)
	return nil
}

func (f *invTestQueue) ForceRemoveForNode(_ context.Context, nodeID shared.UUID, _ shared.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedNodes = append(f.removedNodes, nodeID)
	return nil
}

func (f *invTestQueue) ForceRemoveForNodeInTx(_ context.Context, nodeID shared.UUID, _ shared.UUID, _ persistence.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedNodes = append(f.removedNodes, nodeID)
	return nil
}

func (f *invTestQueue) ListOrphanedClaims(_ context.Context) ([]persistence.DispatchRow, error) {
	return nil, nil
}
func (f *invTestQueue) ReleaseClaim(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *invTestQueue) ReleaseClaimWithDisposition(_ context.Context, _ shared.UUID, _ string, _ string) error {
	return nil
}
func (f *invTestQueue) StampPriorDispatchInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ shared.UUID, _ string) error {
	return nil
}

func (f *invTestQueue) ForceReleaseClaim(_ context.Context, _ shared.UUID) error { return nil }
func (f *invTestQueue) GetClaimedBy(_ context.Context, _ shared.UUID) (persistence.ClaimOwnership, error) {
	return persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *invTestQueue) GetDispatchNode(_ context.Context, _ shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *invTestQueue) GetDispatchNodeInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
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
func (f *invTestQueue) GetMostRecentRunForNodeInScope(ctx context.Context, tx persistence.Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error) {
	if f.real != nil {
		return f.real.GetMostRecentRunForNodeInScope(ctx, tx, nodeID, runScopeID)
	}
	return shared.UUID{}, false, nil
}

func (f *invTestQueue) ListInFlightRunStates(ctx context.Context, tx persistence.Tx, nodeIDs []shared.UUID, frameID, runScopeID shared.UUID) (map[shared.UUID][]string, error) {
	if f.real != nil {
		return f.real.ListInFlightRunStates(ctx, tx, nodeIDs, frameID, runScopeID)
	}
	return map[shared.UUID][]string{}, nil
}

func (f *invTestQueue) ParkActiveInTx(_ context.Context, _ persistence.Tx, _ persistence.ParkActiveInput) error {
	return nil
}
func (f *invTestQueue) ListParkedReadyForResume(_ context.Context, _ time.Time, _ int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *invTestQueue) GetParkedByNode(_ context.Context, _ persistence.Tx, _ shared.UUID, _ shared.UUID) (*persistence.ParkedRow, error) {
	return nil, nil
}
func (f *invTestQueue) ResumeParkedInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) (bool, error) {
	return false, nil
}
func (f *invTestQueue) UpdateDispatchTuningInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ *int) error {
	return nil
}
func (f *invTestQueue) CountParked(_ context.Context) (int, error) {
	return 0, nil
}
func (f *invTestQueue) BumpLastProgressAt(_ context.Context, _ persistence.Tx, _ shared.UUID, _ time.Time) (bool, error) {
	return true, nil
}
func (f *invTestQueue) RegisterAsyncAck(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string, _ time.Time, _ *int, _ *int, _ string) error {
	return nil
}
func (f *invTestQueue) LookupRunByAsyncAckID(_ context.Context, _ persistence.Tx, _ string) (*persistence.DispatchRow, error) {
	return nil, nil
}
func (f *invTestQueue) LoadScratchInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]byte, string, string, error) {
	return nil, "", "", nil
}
func (f *invTestQueue) WriteScratchInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ []byte, _, _ string) error {
	return nil
}
func (f *invTestQueue) ListParkedDiagnostic(_ context.Context, _ persistence.Tx) ([]persistence.ParkedDiagnosticRow, error) {
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

type fixture struct {
	driver      persistence.Database
	persist     persistence.Tables
	q           *invTestQueue
	clock       shared.Clock
	log         shared.Logger
	instance    persistence.InstanceRow
	mainScopeID shared.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithNodes(t, []nodepkg.TemplateNodeDef{})
}

func newFixtureWithNodes(t *testing.T, defs []nodepkg.TemplateNodeDef) *fixture {
	t.Helper()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	tpl := insertDeployedTemplate(ctx, t, d.Tables(), nodepkg.TemplateSpec{
		Name: "sched-test-" + uuid.NewString(), Version: "v1",
		Nodes: defs,
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
			Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = row
		return nil
	}))

	return &fixture{
		driver:      d,
		persist:     d.Tables(),
		q:           newInvTestQueueWithReal(d.Queue()),
		clock:       shared.SystemClock{},
		log:         shared.SilentLogger{},
		instance:    inst,
		mainScopeID: mainScopeID,
	}
}

func (f *fixture) createNodeInState(t *testing.T, executor string, state cascade.NodeState, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	_ = deps
	return f.createTypedNodeInState(t, "t", executor, state)
}

func (f *fixture) createTypedNodeInState(t *testing.T, nodeType, executor string, state cascade.NodeState) persistence.NodeRow {
	t.Helper()
	ctx := context.Background()
	var n persistence.NodeRow
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := f.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: uuid.New(), InstanceID: f.instance.ID, NodeType: nodeType,
			Executor: executor,
		}, tx)
		if err != nil {
			return err
		}
		n = row
		return nil
	}))
	var count int
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
		[]any{f.instance.ID}, &count)
	var frameID shared.UUID
	if count == 0 {
		msgID := uuid.New()
		pgdbtest.ExecForTest(ctx, t, f.driver, `
            INSERT INTO rimsky_messages
                (id, instance_id, type, sender, sender_kind, received_at)
            VALUES ($1, $2, 'test/seed', 'test', 'operator', now())
        `, msgID, f.instance.ID)
		pgdbtest.QueryRowForTest(ctx, t, f.driver, `
            INSERT INTO rimsky_frames
                (instance_id, triggering_message_id, root_run_scope_id, started_at)
            VALUES ($1, $2, $3, now())
            RETURNING frame_id
        `, []any{f.instance.ID, msgID, f.mainScopeID}, &frameID)
	} else {
		pgdbtest.QueryRowForTest(ctx, t, f.driver, `
            SELECT frame_id FROM rimsky_frames
            WHERE instance_id = $1 AND ended_at IS NULL
            LIMIT 1
        `, []any{f.instance.ID}, &frameID)
	}
	pgdbtest.ExecForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
        VALUES (gen_random_uuid(), $1, $2, ARRAY[]::text[], NOW(), $3, 1, 'cascade', $4, $5)
    `, n.ID, executor, string(state), frameID, f.mainScopeID)
	return n
}

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

	var afterLatest *persistence.NodeRunLatest
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, tx, n.ID)
		afterLatest = r
		return err
	}))
	require.NotNil(t, afterLatest)
	require.Equal(t, cascade.NodeStateFresh, afterLatest.State)
}

func TestRecalculateNode_StaleWithPendingWaitSet_IsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	dep := f.createNodeInState(t, "worker", cascade.NodeStateStale)
	target := f.createNodeInState(t, "worker", cascade.NodeStateStale)

	var frameID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL LIMIT 1`,
		[]any{f.instance.ID}, &frameID)
	var depRunID, targetRunID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1 AND frame_id = $2`,
		[]any{dep.ID, frameID}, &depRunID)
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1 AND frame_id = $2`,
		[]any{target.ID, frameID}, &targetRunID)
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           frameID,
			ReceiverNodeRunID: targetRunID,
			SenderNodeRunID:   depRunID,
			TopicKind:         "state",
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

func TestRecalculateNode_StaleWithEmptyWaitSetAndExecutor_EnqueuesDispatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	target := f.createNodeInState(t, "runner", cascade.NodeStateStale)

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

func TestRecalculateNode_StaleWithDrainedWaitSet_StillEnqueues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	dep := f.createNodeInState(t, "worker", cascade.NodeStateStale)
	target := f.createNodeInState(t, "runner", cascade.NodeStateStale)

	var frameID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL LIMIT 1`,
		[]any{f.instance.ID}, &frameID)
	var depRunID, targetRunID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1 AND frame_id = $2`,
		[]any{dep.ID, frameID}, &depRunID)
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1 AND frame_id = $2`,
		[]any{target.ID, frameID}, &targetRunID)
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.persist.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           frameID,
			ReceiverNodeRunID: targetRunID,
			SenderNodeRunID:   depRunID,
			TopicKind:         "state",
		}, tx)
	}))
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.persist.WaitSet().MarkDrainedBySender(ctx, frameID, depRunID, tx)
	}))

	err := runtime.RecalculateNode(ctx, runtime.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Len(t, eq, 1,
		"a cascade-created stale run with a fully-drained (non-empty) wait-set must not be a silent no-op")
	require.Equal(t, target.ID, eq[0].NodeID)
}

func TestRecalculateNode_PopulatesRequiredClaimProducersFromNodeDef(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixtureWithNodes(t, []nodepkg.TemplateNodeDef{
		{
			Type: "recalc-req-cp", Executor: "runner",
			ClaimProducers: []nodepkg.NodeClaimProducerRef{
				{Name: "workspace-recalc", Selector: "/x", Intent: "rw", Alias: "schema"},
			},
		},
	})

	target := f.createTypedNodeInState(t, "recalc-req-cp", "runner", cascade.NodeStateStale)

	err := runtime.RecalculateNode(ctx, runtime.RecalculateArgs{
		Persist: f.persist, Queue: f.q, Clock: f.clock, Logger: f.log,
		TargetNodeID: target.ID,
	})
	require.NoError(t, err)

	eq, _ := f.q.snapshot()
	require.Len(t, eq, 1)
	require.Equal(t, []string{"workspace-recalc"}, eq[0].RequiredClaimProducers,
		"recalculate dispatch must derive required_claim_producers from the node definition, not hardcode empty")
}
