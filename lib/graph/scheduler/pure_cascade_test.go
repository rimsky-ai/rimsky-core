// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scheduler

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

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
func (f *fakeQueue) PromoteClaimedToRunning(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string) (bool, error) {
	return false, nil
}
func (f *fakeQueue) Complete(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *fakeQueue) ForceComplete(_ context.Context, _ shared.UUID) error      { return nil }
func (f *fakeQueue) RemoveForNode(_ context.Context, _ shared.UUID, _ shared.UUID, _ string) error {
	return nil
}
func (f *fakeQueue) RemoveForNodeInTx(_ context.Context, _ shared.UUID, _ shared.UUID, _ string, _ persistence.Tx) error {
	return nil
}
func (f *fakeQueue) ForceRemoveForNode(_ context.Context, _ shared.UUID, _ shared.UUID) error {
	return nil
}
func (f *fakeQueue) ForceRemoveForNodeInTx(_ context.Context, _ shared.UUID, _ shared.UUID, _ persistence.Tx) error {
	return nil
}
func (f *fakeQueue) ListOrphanedClaims(_ context.Context) ([]persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeQueue) ReleaseClaim(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *fakeQueue) ForceReleaseClaim(_ context.Context, _ shared.UUID) error      { return nil }
func (f *fakeQueue) GetClaimedBy(_ context.Context, _ shared.UUID) (persistence.ClaimOwnership, error) {
	return persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *fakeQueue) GetDispatchNode(_ context.Context, _ shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *fakeQueue) RefreshHeartbeat(_ context.Context, _ string) error { return nil }
func (f *fakeQueue) ListLive(_ context.Context, _ persistence.DispatchListFilter, _ persistence.ListPagination) (persistence.PaginatedListResult[persistence.DispatchRow], error) {
	return persistence.PaginatedListResult[persistence.DispatchRow]{}, nil
}
func (f *fakeQueue) CountLive(_ context.Context, _ persistence.DispatchListFilter) (int, error) {
	return 0, nil
}
func (f *fakeQueue) GetByID(_ context.Context, _ shared.UUID) (*persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeQueue) GetInFlightRunForNode(_ context.Context, _ persistence.Tx, _ shared.UUID, _ shared.UUID) (shared.UUID, bool, error) {
	return shared.UUID{}, false, nil
}
func (f *fakeQueue) GetMostRecentRunForNodeInScope(_ context.Context, _ persistence.Tx, _ shared.UUID, _ shared.UUID) (shared.UUID, bool, error) {
	return shared.UUID{}, false, nil
}
func (f *fakeQueue) ListInFlightRunStates(context.Context, persistence.Tx, []shared.UUID, shared.UUID, shared.UUID) (map[shared.UUID][]string, error) {
	return map[shared.UUID][]string{}, nil
}

func (f *fakeQueue) ParkActiveInTx(_ context.Context, _ persistence.Tx, _ persistence.ParkActiveInput) error {
	return nil
}
func (f *fakeQueue) ListParkedReadyForResume(_ context.Context, _ time.Time, _ int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeQueue) GetParkedByNode(_ context.Context, _ shared.UUID, _ shared.UUID) (*persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeQueue) ResumeParkedInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) (bool, error) {
	return false, nil
}
func (f *fakeQueue) RebindRunFrameInTx(_ context.Context, _ persistence.Tx, _, _ shared.UUID) error {
	return nil
}
func (f *fakeQueue) GetRetryNoProgress(_ context.Context, _ shared.UUID) (int, *int, error) {
	return 0, nil, nil
}
func (f *fakeQueue) SetRetryNoProgressForRunInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ int) error {
	return nil
}
func (f *fakeQueue) UpdateDispatchTuningInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ *int) error {
	return nil
}
func (f *fakeQueue) BumpLastProgressAt(_ context.Context, _ persistence.Tx, _ shared.UUID, _ time.Time) (bool, error) {
	return true, nil
}
func (f *fakeQueue) RegisterAsyncAck(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string, _ time.Time, _ *int, _ *int, _ string) error {
	return nil
}
func (f *fakeQueue) LookupRunByAsyncAckID(_ context.Context, _ persistence.Tx, _ string) (*persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeQueue) LoadScratchInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]byte, string, string, error) {
	return nil, "", "", nil
}
func (f *fakeQueue) WriteScratchInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ []byte, _, _ string) error {
	return nil
}

func (f *fakeQueue) CountParked(_ context.Context) (int, error) {
	return 0, nil
}

func (f *fakeQueue) ListParkedDiagnostic(_ context.Context, _ persistence.Tx) ([]persistence.ParkedDiagnosticRow, error) {
	return nil, nil
}

func (f *fakeQueue) snapshot() []persistence.DispatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]persistence.DispatchRequest, len(f.enqueued))
	copy(out, f.enqueued)
	return out
}

var _ persistence.Queue = (*fakeQueue)(nil)

type pcFixture struct {
	persist persistence.Tables
	driver  persistence.Database
}

func newPureCascadeFixture(t *testing.T) *pcFixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	return &pcFixture{persist: d.Tables(), driver: d}
}

func pcDeployTemplate(ctx context.Context, t *testing.T, b persistence.Tables, name string) persistence.TemplateRow {
	t.Helper()
	return insertDeployedTemplate(ctx, t, b, nodepkg.TemplateSpec{
		Name: name, Version: "v1", Description: "test",
		Nodes: []nodepkg.TemplateNodeDef{},
	})
}

func pcCreateInstance(ctx context.Context, t *testing.T, b persistence.Tables, templateHash string, ck string) persistence.InstanceRow {
	t.Helper()
	ckCopy := ck
	var inst persistence.InstanceRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	inTxTest(t, ctx, b, func(tx persistence.Tx) error {
		if err := b.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instID,
		}); err != nil {
			return err
		}
		row, err := b.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: templateHash, InstanceKey: &ckCopy, Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = row
		return nil
	})
	return inst
}

func pcCreateNode(ctx context.Context, t *testing.T, f *pcFixture, instanceID shared.UUID, executor string, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	_ = deps
	return pcCreateNodeWithType(ctx, t, f, instanceID, "t", executor)
}

func pcCreateNodeWithType(ctx context.Context, t *testing.T, f *pcFixture, instanceID shared.UUID, nodeType, executor string) persistence.NodeRow {
	t.Helper()
	var n persistence.NodeRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		row, err := f.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: uuid.New(), InstanceID: instanceID, NodeType: nodeType,
			Executor: executor,
		}, tx)
		if err != nil {
			return err
		}
		n = row
		return nil
	})
	forceState(ctx, t, f, n.ID, "stale")
	return n
}

func forceState(ctx context.Context, t *testing.T, f *pcFixture, id shared.UUID, state string) {
	t.Helper()
	if state == "fresh" {
		pgtest.ExecForTest(ctx, t, f.driver,
			`DELETE FROM rimsky_node_runs WHERE node_id = $1
			    AND state IN ('pending','stale','running','held','parked')`, id)
		return
	}
	var (
		executorN  sql.NullString
		instanceID shared.UUID
		frameN     sql.NullString
	)
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT executor, instance_id::text FROM rimsky_nodes WHERE id = $1`,
		[]any{id}, &executorN, &instanceID)
	{
		var count int
		pgtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
			[]any{instanceID}, &count)
		var fid shared.UUID
		if count == 0 {
			fid = pcSeedFrame(ctx, t, f, instanceID, id)
		} else {
			pgtest.QueryRowForTest(ctx, t, f.driver,
				`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL LIMIT 1`,
				[]any{instanceID}, &fid)
		}
		frameN = sql.NullString{String: fid.String(), Valid: true}
	}
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'main'`,
		[]any{instanceID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, f.driver,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores,
		                               enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
		 VALUES (gen_random_uuid(), $1, $2, '{}', NOW(), $3, 1, 'cascade', $4::uuid, $5)`,
		id, executorN.String, state, frameN.String, mainScopeID)
}

func pcSeedFrame(ctx context.Context, t *testing.T, f *pcFixture, instanceID, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
		[]any{instanceID}, &count)
	var frameID shared.UUID
	if count == 0 {
		var rootScope shared.UUID
		pgtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT id FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'main'`,
			[]any{instanceID}, &rootScope)
		msgID := uuid.New()
		pgtest.ExecForTest(ctx, t, f.driver, `
            INSERT INTO rimsky_messages
                (id, instance_id, type, sender, sender_kind, received_at)
            VALUES ($1, $2, 'test/seed', 'test', 'operator', now())
        `, msgID, instanceID)
		pgtest.QueryRowForTest(ctx, t, f.driver, `
            INSERT INTO rimsky_frames
                (instance_id, triggering_message_id, root_run_scope_id, started_at)
            VALUES ($1, $2, $3, now())
            RETURNING frame_id
        `, []any{instanceID, msgID, rootScope}, &frameID)
		_ = nodeID
	} else {
		pgtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL LIMIT 1`,
			[]any{instanceID}, &frameID)
	}
	_ = nodeID
	return frameID
}

func pcArgs(b persistence.Tables, q *fakeQueue) PureCascadeArgs {
	return PureCascadeArgs{
		Persist: b, Queue: q, Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}
}

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
	pure := pcCreateNode(ctx, t, f, inst.ID, "")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var latest *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, tx, pure.ID)
		latest = r
		return err
	})
	require.NotNil(t, latest)
	assert.Equal(t, cascade.NodeStateFresh, latest.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &pure.ID, Kind: "terminal/success",
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	require.Len(t, evs.Events, 1)
	require.NotNil(t, evs.Events[0].NodeID)
	assert.Equal(t, pure.ID, *evs.Events[0].NodeID)
	require.NotNil(t, evs.Events[0].InstanceID)
	assert.Equal(t, inst.ID, *evs.Events[0].InstanceID)

	assert.Empty(t, q.snapshot())
}

func TestProcessPureCascade_WithExecutorNodeIsSkipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "alpha")
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")
	execNode := pcCreateNode(ctx, t, f, inst.ID, "worker")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	var latest *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, tx, execNode.ID)
		latest = r
		return err
	})
	require.NotNil(t, latest)
	assert.Equal(t, cascade.NodeStateStale, latest.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &execNode.ID, Kind: "terminal/success",
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	assert.Empty(t, evs.Events)

	assert.Empty(t, q.snapshot())
}

func TestProcessPureCascade_NativeClaimOnly_ReusesStaleRunAcrossTicks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	sum := insertDeployedTemplate(ctx, t, f.persist, nodepkg.TemplateSpec{
		Name: "claim-only", Version: "v1", Description: "test",
		Nodes: []nodepkg.TemplateNodeDef{{
			Type:     "t",
			Executor: "",
			ClaimProducers: []nodepkg.NodeClaimProducerRef{
				{Name: "alpha", Selector: "x", Intent: "rw"},
				{Name: "beta", Selector: "y", Intent: "r"},
			},
		}},
	})
	inst := pcCreateInstance(ctx, t, f.persist, sum.ID, "ck-claim")
	claimNode := pcCreateNode(ctx, t, f, inst.ID, "")
	pcSeedFrame(ctx, t, f, inst.ID, claimNode.ID)

	args := PureCascadeArgs{
		Persist: f.persist, Queue: f.driver.Queue(),
		Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}

	firstCount, err := ProcessPureCascade(ctx, args)
	require.NoError(t, err)
	assert.Equal(t, 1, firstCount, "first tick prepares the existing stale run for claim routing")

	for i := 0; i < 4; i++ {
		c, err := ProcessPureCascade(ctx, args)
		require.NoError(t, err)
		assert.Equal(t, 0, c, "tick %d must not re-process an already-prepared claim run", i+2)
	}

	var inFlight int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_node_runs
		  WHERE node_id = $1
		    AND state IN ('pending','stale','running','held','parked')`,
		[]any{claimNode.ID}, &inFlight)
	assert.Equal(t, 1, inFlight, "five scheduler ticks must yield exactly one in-flight run, not one per tick")

	var required []string
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT required_stores FROM rimsky_node_runs
		  WHERE node_id = $1 AND state = 'stale'`,
		[]any{claimNode.ID}, &required)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, required,
		"the reused stale run carries the claim-producer routing")

	var latest *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, tx, claimNode.ID)
		latest = r
		return err
	})
	require.NotNil(t, latest)
	assert.Equal(t, cascade.NodeStateStale, latest.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &claimNode.ID, Kind: "terminal/success",
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	assert.Empty(t, evs.Events, "a claim-only node must not settle before its claims are acquired")
}

func TestProcessPureCascade_CascadesToDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := insertDeployedTemplate(ctx, t, f.persist, nodepkg.TemplateSpec{
		Name: "alpha-cascade", Version: "v1",
		Nodes: []nodepkg.TemplateNodeDef{
			{Type: "pure-a"},
			{Type: "worker-b", Executor: "worker",
				Subscribes: []nodepkg.SubscriptionEntry{{Node: "pure-a", Type: "terminal/*", ForceUpstreamRefresh: nodepkg.BoolPtr(false)}}},
		},
	})
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")

	pureA := pcCreateNodeWithType(ctx, t, f, inst.ID, "pure-a", "")
	execB := pcCreateNodeWithType(ctx, t, f, inst.ID, "worker-b", "worker")
	pcSeedFrame(ctx, t, f, inst.ID, execB.ID)
	_ = execB

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var latestA *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, tx, pureA.ID)
		latestA = r
		return err
	})
	require.NotNil(t, latestA)
	assert.Equal(t, cascade.NodeStateFresh, latestA.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			Kind: "terminal/success",
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	require.Len(t, evs.Events, 1)
	require.NotNil(t, evs.Events[0].NodeID)
	assert.Equal(t, pureA.ID, *evs.Events[0].NodeID)
}
