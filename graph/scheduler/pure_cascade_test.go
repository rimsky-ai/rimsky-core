// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Tests for ProcessPureCascade. Uses the real persistence.Database via
// pgtest (same pattern as invalidate_test.go) and a lightweight fake
// persistence.Queue so assertions can inspect exactly what propagated.
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

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	nodepkg "github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/internal/pgtest"
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
func (f *fakeQueue) RemoveForNode(_ context.Context, _ shared.UUID, _ shared.UUID, _ string) error {
	return nil
}
func (f *fakeQueue) RemoveForNodeInTx(_ context.Context, _ shared.UUID, _ shared.UUID, _ string, _ persistence.Tx) error {
	return nil
}
func (f *fakeQueue) ListOrphanedClaims(_ context.Context, _ time.Time) ([]persistence.DispatchRow, error) {
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

// Park-lifecycle helpers for the 2026-05-08 platform-extensions plan.
// fakeQueue is a fixture used by pure-cascade tests that don't park
// nodes; the helpers are no-ops returning the conventional zero values.
func (f *fakeQueue) ParkActiveInTx(_ context.Context, _ persistence.Tx, _ persistence.ParkActiveInput) error {
	return nil
}
func (f *fakeQueue) ListParkedReadyForResume(_ context.Context, _ time.Time, _ int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeQueue) ListParkedOverdue(_ context.Context, _ time.Time, _ int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeQueue) GetParkedByNode(_ context.Context, _ shared.UUID, _ shared.UUID) (*persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeQueue) ResumeParkedInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string) (bool, error) {
	return false, nil
}
func (f *fakeQueue) RebindRunFrameInTx(_ context.Context, _ persistence.Tx, _, _ shared.UUID) error {
	return nil
}
func (f *fakeQueue) GetRetryNoProgress(_ context.Context, _ shared.UUID) (int, *int, error) {
	return 0, nil, nil
}
func (f *fakeQueue) SetRetryNoProgressForNodeInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ shared.UUID, _ int) error {
	return nil
}
func (f *fakeQueue) UpdateDispatchTuningInTx(_ context.Context, _ persistence.Tx, _ shared.UUID, _ *int, _ *int) error {
	return nil
}
func (f *fakeQueue) LoadResumeMetadataInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) (*persistence.ResumeMetadataRow, error) {
	return nil, nil
}
func (f *fakeQueue) ClearResumeMetadataInTx(_ context.Context, _ persistence.Tx, _ shared.UUID) error {
	return nil
}

func (f *fakeQueue) CountParkedByReason(_ context.Context) (map[string]int, error) {
	return nil, nil
}

func (f *fakeQueue) ListParkedDiagnostic(_ context.Context, _ persistence.Tx, _ string) ([]persistence.ParkedDiagnosticRow, error) {
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

// --- Local fixture helpers --------------------------------------------------

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
		FrameResolutionMode: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:      nodepkg.FrameTimeoutDefaultMs,
		Nodes:               []nodepkg.TemplateNodeDef{},
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
			MainRunScopeID: mainScopeID,
		}, tx)
		if err != nil {
			return err
		}
		inst = row
		return nil
	})
	return inst
}

// pcCreateNode creates a node and forces it to 'stale'. Under the frame
// model Create() defaults to 'fresh'; pure-cascade tests need an
// in-flight stale source to exercise ProcessPureCascade.
func pcCreateNode(ctx context.Context, t *testing.T, f *pcFixture, instanceID shared.UUID, executor string, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	_ = deps // legacy: dependency-edge resolution is now via subscription-edge map
	return pcCreateNodeWithType(ctx, t, f, instanceID, "t", executor)
}

// pcCreateNodeWithType is the post-2026-05-14 helper: NodeType is
// explicit so the test's template can declare matching subscribers.
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
	n.State = cascade.NodeStateStale
	return n
}

// forceState bypasses the state machine and seeds a node's state via a
// directly-inserted in-flight rimsky_node_runs row (post-stage-3
// cutover: state lives on rimsky_node_runs).
//
// Reuses an existing running frame for the instance if one exists;
// otherwise inserts a fresh one (so callers don't have to pre-seed).
func forceState(ctx context.Context, t *testing.T, f *pcFixture, id shared.UUID, state string) {
	t.Helper()
	if state == "fresh" {
		// 'fresh' is the no-run-row state — delete any in-flight rows.
		pgtest.ExecForTest(ctx, t, f.driver,
			`DELETE FROM rimsky_node_runs WHERE node_id = $1
			    AND phase IN ('pending','active','held','parked')`, id)
		return
	}
	// Resolve the node's executor + instance_id + frame_id.
	var (
		executorN  sql.NullString
		instanceID shared.UUID
		frameN     sql.NullString
	)
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT executor, instance_id::text, frame_id::text FROM rimsky_nodes WHERE id = $1`,
		[]any{id}, &executorN, &instanceID, &frameN)
	if !frameN.Valid {
		// Look for an existing running frame for the instance first; if
		// missing, seed a new one.
		var count int
		pgtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND state = 'running'`,
			[]any{instanceID}, &count)
		var fid shared.UUID
		if count == 0 {
			fid = pcSeedFrame(ctx, t, f, instanceID, id)
		} else {
			pgtest.QueryRowForTest(ctx, t, f.driver,
				`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND state = 'running' LIMIT 1`,
				[]any{instanceID}, &fid)
			pgtest.ExecForTest(ctx, t, f.driver,
				`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, fid, id)
		}
		frameN = sql.NullString{String: fid.String(), Valid: true}
	}
	runPhase := "pending"
	if state == "running" {
		runPhase = "active"
	}
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1`,
		[]any{instanceID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, f.driver,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores,
		                               enqueued_at, phase, state, frame_id, run_scope_id)
		 VALUES (gen_random_uuid(), $1, $2, '{}', NOW(), $3, $4, $5::uuid, $6)`,
		id, executorN.String, runPhase, state, frameN.String, mainScopeID)
}

// pcSeedFrame inserts a running rimsky_frames row for the given instance
// (or reuses the existing running frame; only one is allowed per
// instance), assigns the frame_id to the given node, and returns the
// frame id. Used to satisfy blessed-invariant 19 (no NULL frame_id on
// in-flight dispatch enqueue) for tests that drive ProcessPureCascade
// against pre-existing stale nodes.
func pcSeedFrame(ctx context.Context, t *testing.T, f *pcFixture, instanceID, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND state = 'running'`,
		[]any{instanceID}, &count)
	var frameID shared.UUID
	if count == 0 {
		pgtest.QueryRowForTest(ctx, t, f.driver, `
            INSERT INTO rimsky_frames
                (instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
            VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
            RETURNING frame_id
        `, []any{instanceID, nodeID}, &frameID)
	} else {
		pgtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND state = 'running' LIMIT 1`,
			[]any{instanceID}, &frameID)
	}
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, nodeID)
	return frameID
}

func pcArgs(b persistence.Tables, q *fakeQueue) PureCascadeArgs {
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
	var got *persistence.NodeRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().Get(ctx, pure.ID, tx)
		got = r
		return err
	})
	require.NotNil(t, got)
	assert.Equal(t, cascade.NodeStateFresh, got.State)

	// terminal/success signal logged with correct node + instance (per Pass 5).
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

	var got *persistence.NodeRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().Get(ctx, execNode.ID, tx)
		got = r
		return err
	})
	assert.Equal(t, cascade.NodeStateStale, got.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &execNode.ID, Kind: "terminal/success",
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	assert.Empty(t, evs.Events)

	// And no one else transitioned or enqueued.
	assert.Empty(t, q.snapshot())
}

// TestProcessPureCascade_NativeClaimOnly_Enqueues pins the §7.3 step 4b
// branch: an empty-executor node whose template node-def declares at
// least one store with claim=true is treated as native claim-only — the
// scheduler enqueues it onto the dispatch queue with the template's
// RequiredStores, leaves the node stale, and does NOT log
// terminal/success signal. The supervisor's omnibus runner takes it from
// there.
func TestProcessPureCascade_NativeClaimOnly_Enqueues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	// Template has one node def whose Stores include a claim-true entry.
	sum := insertDeployedTemplate(ctx, t, f.persist, nodepkg.TemplateSpec{
		Name: "claim-only", Version: "v1", Description: "test",
		FrameResolutionMode: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:      nodepkg.FrameTimeoutDefaultMs,
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
	var got *persistence.NodeRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().Get(ctx, claimNode.ID, tx)
		got = r
		return err
	})
	assert.Equal(t, cascade.NodeStateStale, got.State)

	// One enqueue with empty ExecutorName and the template's RequiredStores.
	enq := q.snapshot()
	require.Len(t, enq, 1)
	assert.Equal(t, claimNode.ID, enq[0].NodeID)
	assert.Equal(t, "", enq[0].ExecutorName)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, enq[0].RequiredStores)

	// No terminal/success signal for native claim-only nodes (they enqueue, not transition fresh in the sweep).
	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &claimNode.ID, Kind: "terminal/success",
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	assert.Empty(t, evs.Events)
}

func TestProcessPureCascade_CascadesToDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	// Template carries two declared node-types: pure-cascade `pure-a`
	// and executor-backed `worker-b`. Under the post-2026-05-14 model,
	// `worker-b` subscribes to `pure-a` via subscription-edge inference;
	// the pure-cascade sweep computes receivers from that inverse map.
	tpl := insertDeployedTemplate(ctx, t, f.persist, nodepkg.TemplateSpec{
		Name: "alpha-cascade", Version: "v1",
		FrameResolutionMode: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:      nodepkg.FrameTimeoutDefaultMs,
		Nodes: []nodepkg.TemplateNodeDef{
			{Type: "pure-a"},
			{Type: "worker-b", Executor: "worker",
				Subscribes: []nodepkg.SubscriptionEntry{{Node: "pure-a", Type: "terminal/*"}}},
		},
	})
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")

	// A: pure cascade, no deps. B: executor "worker", subscribes to A.
	// Before sweep: A=stale, B=stale (gated by A). Sweep flips A → fresh,
	// then emits recalculate to B; B's wait-set is empty (we didn't seed
	// any wait-set rows) so the recalculate enqueues B onto the dispatch
	// queue.
	pureA := pcCreateNodeWithType(ctx, t, f, inst.ID, "pure-a", "")
	execB := pcCreateNodeWithType(ctx, t, f, inst.ID, "worker-b", "worker")
	// Seed a frame for both (B is the one that gets enqueued).
	pcSeedFrame(ctx, t, f, inst.ID, execB.ID)

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// A is fresh.
	var gotA *persistence.NodeRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().Get(ctx, pureA.ID, tx)
		gotA = r
		return err
	})
	assert.Equal(t, cascade.NodeStateFresh, gotA.State)

	// B was enqueued by the recalculate path.
	enq := q.snapshot()
	require.Len(t, enq, 1)
	assert.Equal(t, execB.ID, enq[0].NodeID)
	assert.Equal(t, "worker", enq[0].ExecutorName)

	// terminal/success logged for A only (B was enqueued, not transitioned).
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
