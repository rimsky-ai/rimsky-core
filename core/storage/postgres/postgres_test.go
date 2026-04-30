// Integration tests for the Postgres storage backend. Each top-level Test*
// function starts a fresh containerized Postgres via the pgtest harness,
// applies migrations, and exercises one store's public surface.
//
// Tests are t.Parallel()-safe because each spawns its own container.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

func newBackend(t *testing.T) (*PostgresStorageBackend, *pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	return New(pool), pool, teardown
}

func deployedTemplate(ctx context.Context, t *testing.T, b *PostgresStorageBackend, name, version string) storage.TemplateSummary {
	t.Helper()
	spec := nodepkg.TemplateSpec{
		Name: name, Version: version, Description: "test",
		Nodes: []nodepkg.TemplateNodeDef{},
	}
	sum, err := b.Templates().Deploy(ctx, spec, nil)
	require.NoError(t, err)
	return sum
}

func createInstance(ctx context.Context, t *testing.T, b *PostgresStorageBackend, templateID shared.UUID, ck string) storage.InstanceRow {
	t.Helper()
	inst, err := b.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: templateID, ConsumerKey: ck, Params: map[string]any{"k": "v"},
	}, nil)
	require.NoError(t, err)
	return inst
}

// createNode creates a node and forces it to 'stale' so storage tests can
// continue to exercise the pre-frame-model state-machine paths
// (dispatch_claimed, work_completed, etc.). The new Create() default is
// 'fresh' under the frame-resolution model
// (docs/specs/2026-04-26-frame-resolution-design.md §3.1); tests of the
// state machine still want a stale starting point.
func createNode(ctx context.Context, t *testing.T, b *PostgresStorageBackend, instanceID shared.UUID, executor string, deps ...shared.UUID) storage.NodeRow {
	t.Helper()
	in := storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: instanceID, NodeType: "t",
		Executor: executor, Dependencies: deps,
	}
	n, err := b.Nodes().Create(ctx, in, nil)
	require.NoError(t, err)
	// Force to 'stale' for state-machine tests.
	if _, err := b.pool.Exec(ctx,
		`UPDATE rimsky_nodes SET state = 'stale' WHERE id = $1`, n.ID,
	); err != nil {
		t.Fatalf("createNode: force stale: %v", err)
	}
	n.State = shared.NodeStateStale
	return n
}

// -------- Templates --------

func TestTemplateStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	// Deploy + idempotent re-deploy.
	sum1, err := b.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: "alpha", Version: "v1", Nodes: []nodepkg.TemplateNodeDef{},
	}, nil)
	require.NoError(t, err)
	sum2, err := b.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: "alpha", Version: "v1", Nodes: []nodepkg.TemplateNodeDef{},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, sum1.ID, sum2.ID)

	// Divergent re-deploy rejects with ErrTemplateInUse.
	_, err = b.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: "alpha", Version: "v1", Description: "changed",
		Nodes: []nodepkg.TemplateNodeDef{},
	}, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, shared.ErrTemplateInUse)

	// Get.
	got, err := b.Templates().Get(ctx, sum1.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "alpha", got.Name)

	// Get missing.
	missing, err := b.Templates().Get(ctx, uuid.New(), nil)
	require.NoError(t, err)
	require.Nil(t, missing)

	// List + filter + pagination.
	deployedTemplate(ctx, t, b, "beta", "v1")
	listing, err := b.Templates().List(ctx, storage.TemplateListFilter{}, storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	require.Len(t, listing.Rows, 2)

	named, err := b.Templates().List(ctx, storage.TemplateListFilter{Name: "beta"}, storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	require.Len(t, named.Rows, 1)
	require.Equal(t, "beta", named.Rows[0].Name)

	// Delete empty template.
	tombstone := deployedTemplate(ctx, t, b, "gamma", "v1")
	require.NoError(t, b.Templates().Delete(ctx, tombstone.ID, nil))

	// Delete missing template returns ErrTemplateNotFound.
	err = b.Templates().Delete(ctx, uuid.New(), nil)
	require.Error(t, err)
	require.ErrorIs(t, err, shared.ErrTemplateNotFound)

	// Delete template with instance returns ErrTemplateInUse.
	used := deployedTemplate(ctx, t, b, "delta", "v1")
	createInstance(ctx, t, b, used.ID, "cons-1")
	err = b.Templates().Delete(ctx, used.ID, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, shared.ErrTemplateInUse)
}

// -------- Instances --------

func TestInstanceStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, pool, teardown := newBackend(t)
	t.Cleanup(teardown)

	tpl := deployedTemplate(ctx, t, b, "alpha", "v1")

	// Create + duplicate.
	inst := createInstance(ctx, t, b, tpl.ID, "ck-1")
	require.NotEqual(t, shared.UUID{}, inst.ID)
	require.Equal(t, "v", inst.Params["k"])

	_, err := b.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: tpl.ID, ConsumerKey: "ck-1", Params: map[string]any{},
	}, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, shared.ErrConsumerKeyConflict)

	// Get + GetByConsumerKey.
	got, err := b.Instances().Get(ctx, inst.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, inst.ID, got.ID)

	byKey, err := b.Instances().GetByConsumerKey(ctx, tpl.ID, "ck-1", nil)
	require.NoError(t, err)
	require.NotNil(t, byKey)
	require.Equal(t, inst.ID, byKey.ID)

	// GetByConsumerKey missing.
	missing, err := b.Instances().GetByConsumerKey(ctx, tpl.ID, "nope", nil)
	require.NoError(t, err)
	require.Nil(t, missing)

	// List filter.
	_ = createInstance(ctx, t, b, tpl.ID, "ck-2")
	list, err := b.Instances().List(ctx, storage.InstanceListFilter{TemplateID: tpl.ID}, storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	require.Len(t, list.Rows, 2)

	// Delete cascades to nodes.
	node := createNode(ctx, t, b, inst.ID, "exec")
	require.NoError(t, b.Instances().Delete(ctx, inst.ID, nil))
	var count int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM rimsky_nodes WHERE id = $1`, node.ID).Scan(&count)
	require.NoError(t, err)
	require.Zero(t, count)
}

// -------- Nodes --------

func TestNodeStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, pool, teardown := newBackend(t)
	t.Cleanup(teardown)

	tpl := deployedTemplate(ctx, t, b, "alpha", "v1")
	inst := createInstance(ctx, t, b, tpl.ID, "ck-1")

	// Pure cascade node: no executor, no deps -> ready.
	pureDep := createNode(ctx, t, b, inst.ID, "") // cascade
	executorNode := createNode(ctx, t, b, inst.ID, "worker", pureDep.ID)
	detachedNode := createNode(ctx, t, b, inst.ID, "worker")

	// Get.
	got, err := b.Nodes().Get(ctx, executorNode.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "worker", got.Executor)
	require.Equal(t, shared.NodeStateStale, got.State)

	// Get missing.
	missing, err := b.Nodes().Get(ctx, uuid.New(), nil)
	require.NoError(t, err)
	require.Nil(t, missing)

	// ListByInstance.
	list, err := b.Nodes().ListByInstance(ctx, inst.ID, nil)
	require.NoError(t, err)
	require.Len(t, list, 3)

	// Pure cascade ready: pureDep and detachedNode is NOT cascade (has executor).
	pureReady, err := b.Nodes().ListPureCascadeReady(ctx, nil)
	require.NoError(t, err)
	require.Len(t, pureReady, 1)
	require.Equal(t, pureDep.ID, pureReady[0].ID)

	// Ready for dispatch: only `detachedNode` has all deps fresh (trivially, no
	// deps). executorNode depends on pureDep which is stale, so not ready.
	ready, err := b.Nodes().ListReadyForDispatch(ctx, nil)
	require.NoError(t, err)
	require.Len(t, ready, 1)
	require.Equal(t, detachedNode.ID, ready[0].ID)

	// Advance pureDep to fresh via pure_cascade.
	require.NoError(t, b.Nodes().UpdateState(ctx, pureDep.ID, shared.NodeStateFresh, nodepkg.ReasonPureCascade, nil))

	// Now executorNode is dispatch-ready.
	ready2, err := b.Nodes().ListReadyForDispatch(ctx, nil)
	require.NoError(t, err)
	ids := map[shared.UUID]bool{}
	for _, r := range ready2 {
		ids[r.ID] = true
	}
	require.True(t, ids[executorNode.ID])
	require.True(t, ids[detachedNode.ID])

	// Valid transition: stale → running under dispatch_claimed.
	require.NoError(t, b.Nodes().UpdateState(ctx, executorNode.ID, shared.NodeStateRunning, nodepkg.ReasonDispatchClaimed, nil))

	// ListRunning finds it.
	running, err := b.Nodes().ListRunning(ctx, nil)
	require.NoError(t, err)
	require.Len(t, running, 1)
	require.Equal(t, executorNode.ID, running[0].ID)

	// Invalid transition: running → running under dispatch_claimed is REJECTED
	// (blessed invariant §17).
	err = b.Nodes().UpdateState(ctx, executorNode.ID, shared.NodeStateRunning, nodepkg.ReasonDispatchClaimed, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, shared.ErrIllegalTransition)

	// Invalid transition: fresh → fresh is rejected too.
	err = b.Nodes().UpdateState(ctx, pureDep.ID, shared.NodeStateFresh, nodepkg.ReasonPureCascade, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, shared.ErrIllegalTransition)

	// Work completed → fresh.
	require.NoError(t, b.Nodes().UpdateState(ctx, executorNode.ID, shared.NodeStateFresh, nodepkg.ReasonWorkCompleted, nil))

	// UpdateError.
	require.NoError(t, b.Nodes().UpdateError(ctx, executorNode.ID,
		nodepkg.EvaluatorState{ActionIndex: 2, RetryCounter: 1, CurrentErrorClass: "timeout"}, nil))
	after, err := b.Nodes().Get(ctx, executorNode.ID, nil)
	require.NoError(t, err)
	require.Equal(t, 2, after.ActionIndex)
	require.Equal(t, 1, after.RetryCounter)
	require.Equal(t, "timeout", after.CurrentErrorClass)

	// CountByState.
	counts, err := b.Nodes().CountByState(ctx, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, counts[shared.NodeStateFresh], 2)

	// ListDependentsOf.
	deps, err := b.Nodes().ListDependentsOf(ctx, pureDep.ID, nil)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Equal(t, executorNode.ID, deps[0].ID)

	// UpdateHeartbeat + ListWithStaleHeartbeat.
	require.NoError(t, b.Nodes().UpdateState(ctx, detachedNode.ID, shared.NodeStateRunning, nodepkg.ReasonDispatchClaimed, nil))
	hbTime := time.Now().Add(-time.Hour)
	require.NoError(t, b.Nodes().UpdateHeartbeat(ctx, detachedNode.ID, hbTime, "sup-1", nil))
	stale, err := b.Nodes().ListWithStaleHeartbeat(ctx, time.Now().Add(-time.Minute), nil)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	require.Equal(t, detachedNode.ID, stale[0].ID)

	// ClearSupervisorAssignment.
	require.NoError(t, b.Nodes().ClearSupervisorAssignment(ctx, detachedNode.ID, nil))
	cleared, err := b.Nodes().Get(ctx, detachedNode.ID, nil)
	require.NoError(t, err)
	require.Equal(t, "", cleared.AssignedSupervisorID)
	require.Nil(t, cleared.LastHeartbeatAt)

	// SetFrameID — write a frame row first, then attach.
	var frameID shared.UUID
	require.NoError(t, pool.QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms)
        VALUES ($1, 'serial_queue', 'queued', ARRAY[$2]::UUID[], now(), 600000)
        RETURNING frame_id`, inst.ID, detachedNode.ID).Scan(&frameID))
	require.NoError(t, b.Nodes().SetFrameID(ctx, detachedNode.ID, &frameID, nil))
	withFrame, err := b.Nodes().Get(ctx, detachedNode.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, withFrame.FrameID)
	require.Equal(t, frameID, *withFrame.FrameID)

	// Clear it back.
	require.NoError(t, b.Nodes().SetFrameID(ctx, detachedNode.ID, nil, nil))
	cleared2, err := b.Nodes().Get(ctx, detachedNode.ID, nil)
	require.NoError(t, err)
	require.Nil(t, cleared2.FrameID)

	// DeleteByInstance.
	require.NoError(t, b.Nodes().DeleteByInstance(ctx, inst.ID, nil))
	remaining, err := b.Nodes().ListByInstance(ctx, inst.ID, nil)
	require.NoError(t, err)
	require.Empty(t, remaining)
}

// TestNodeStore_UpdateStateRejectsRunningToRunningUnderDispatchClaimed checks
// the blessed invariant §17 verbatim — this is the one that can enable
// double-execute if it regresses.
func TestNodeStore_UpdateStateRejectsRunningToRunningUnderDispatchClaimed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	tpl := deployedTemplate(ctx, t, b, "inv", "v1")
	inst := createInstance(ctx, t, b, tpl.ID, "ck")
	n := createNode(ctx, t, b, inst.ID, "worker")

	require.NoError(t, b.Nodes().UpdateState(ctx, n.ID, shared.NodeStateRunning, nodepkg.ReasonDispatchClaimed, nil))

	err := b.Nodes().UpdateState(ctx, n.ID, shared.NodeStateRunning, nodepkg.ReasonDispatchClaimed, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, shared.ErrIllegalTransition)

	// State must still be running (the second call MUST NOT corrupt the row).
	after, err := b.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateRunning, after.State)
}

// -------- Events --------

func TestEventStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	tpl := deployedTemplate(ctx, t, b, "e", "v1")
	inst := createInstance(ctx, t, b, tpl.ID, "ck")
	n := createNode(ctx, t, b, inst.ID, "worker")

	iid := inst.ID
	nid := n.ID

	for i := 0; i < 5; i++ {
		require.NoError(t, b.Events().Append(ctx, storage.EventAppendInput{
			InstanceID: &iid, NodeID: &nid,
			Kind:    "test_kind",
			Payload: map[string]any{"i": i},
		}, nil))
	}

	// List page-1 (limit=2) -> 2 rows + cursor.
	page1, err := b.Events().List(ctx, storage.EventListFilter{NodeID: &nid},
		storage.ListPagination{Limit: 2}, nil)
	require.NoError(t, err)
	require.Len(t, page1.Events, 2)
	require.NotEmpty(t, page1.NextCursor)

	// Page 2.
	page2, err := b.Events().List(ctx, storage.EventListFilter{NodeID: &nid},
		storage.ListPagination{Limit: 2, Cursor: page1.NextCursor}, nil)
	require.NoError(t, err)
	require.Len(t, page2.Events, 2)

	// Page 3 (final single row).
	page3, err := b.Events().List(ctx, storage.EventListFilter{NodeID: &nid},
		storage.ListPagination{Limit: 2, Cursor: page2.NextCursor}, nil)
	require.NoError(t, err)
	require.Len(t, page3.Events, 1)
	require.Empty(t, page3.NextCursor)

	// Tail with no cursor == first page.
	tail, err := b.Events().Tail(ctx, "", 10, nil)
	require.NoError(t, err)
	require.Len(t, tail.Events, 5)

	// Filter by kind.
	byKind, err := b.Events().List(ctx, storage.EventListFilter{Kind: "nope"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.Empty(t, byKind.Events)
}

// -------- Schedules --------

func TestScheduleStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	tpl := deployedTemplate(ctx, t, b, "s", "v1")
	inst := createInstance(ctx, t, b, tpl.ID, "ck")
	n := createNode(ctx, t, b, inst.ID, "worker")

	nextFire := time.Now().Add(-time.Minute) // due
	require.NoError(t, b.Schedules().Register(ctx, storage.ScheduleRegisterInput{
		NodeID: n.ID, CronExpr: "* * * * *", NextFireAt: nextFire,
	}, nil))

	// ListAll.
	all, err := b.Schedules().ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "* * * * *", all[0].CronExpr)

	// DueBefore requires a transaction to use FOR UPDATE SKIP LOCKED.
	err = b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		due, err := b.Schedules().DueBefore(ctx, time.Now(), tx)
		if err != nil {
			return err
		}
		if len(due) != 1 || due[0].NodeID != n.ID {
			return errors.New("expected one due schedule")
		}
		// Record fired to advance next_fire_at.
		nf := time.Now().Add(time.Minute)
		return b.Schedules().RecordFired(ctx, n.ID, nf, time.Now(), tx)
	})
	require.NoError(t, err)

	// After advance no schedule is due.
	err = b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		due, err := b.Schedules().DueBefore(ctx, time.Now(), tx)
		if err != nil {
			return err
		}
		if len(due) != 0 {
			return errors.New("expected no due schedules after advance")
		}
		return nil
	})
	require.NoError(t, err)

	// Upsert under the same node_id (Register acts as upsert).
	require.NoError(t, b.Schedules().Register(ctx, storage.ScheduleRegisterInput{
		NodeID: n.ID, CronExpr: "*/5 * * * *", NextFireAt: time.Now().Add(time.Hour),
	}, nil))
	all2, err := b.Schedules().ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, all2, 1)
	require.Equal(t, "*/5 * * * *", all2[0].CronExpr)
}

// -------- Supervisors --------

func TestSupervisorStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	// Register + re-register (upsert).
	require.NoError(t, b.Supervisors().Register(ctx, storage.SupervisorRegisterInput{
		ID: "sup-1", AcceptedExecutors: []string{"alpha", "beta"},
		AcceptedStores: []string{"content", "topics"},
		Concurrency:    4,
		CallbackHost:   "localhost", CallbackPort: 9000,
	}, nil))

	got, err := b.Supervisors().Get(ctx, "sup-1", nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, []string{"alpha", "beta"}, got.AcceptedExecutors)
	require.Equal(t, []string{"content", "topics"}, got.AcceptedStores)
	require.Equal(t, "localhost", got.CallbackHost)
	require.Equal(t, 9000, got.CallbackPort)

	// Heartbeat updates active_node_count + last_heartbeat_at.
	require.NoError(t, b.Supervisors().Heartbeat(ctx, "sup-1", 2, nil))
	after, err := b.Supervisors().Get(ctx, "sup-1", nil)
	require.NoError(t, err)
	require.Equal(t, 2, after.ActiveNodeCount)

	// ListStale: simulate stale by writing an old heartbeat via Transaction.
	err = b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		pool := b.pool
		_, err := pool.Exec(ctx,
			`UPDATE rimsky_supervisors SET last_heartbeat_at = $1 WHERE id = $2`,
			time.Now().Add(-time.Hour), "sup-1")
		return err
	})
	require.NoError(t, err)
	stale, err := b.Supervisors().ListStale(ctx, time.Now().Add(-time.Minute), nil)
	require.NoError(t, err)
	require.Len(t, stale, 1)

	// List returns all.
	all, err := b.Supervisors().List(ctx, nil)
	require.NoError(t, err)
	require.Len(t, all, 1)

	// Unregister.
	require.NoError(t, b.Supervisors().Unregister(ctx, "sup-1", nil))
	gone, err := b.Supervisors().Get(ctx, "sup-1", nil)
	require.NoError(t, err)
	require.Nil(t, gone)
}

// -------- Transaction semantics smoke test --------

func TestTransaction_RollbackOnError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	tpl := deployedTemplate(ctx, t, b, "tx", "v1")

	boom := errors.New("boom")
	err := b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		_, err := b.Instances().Create(ctx, storage.InstanceCreateInput{
			TemplateID: tpl.ID, ConsumerKey: "rollback", Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		return boom
	})
	require.ErrorIs(t, err, boom)

	// Instance must not persist.
	got, err := b.Instances().GetByConsumerKey(ctx, tpl.ID, "rollback", nil)
	require.NoError(t, err)
	require.Nil(t, got)
}

// -------- Node attributes --------

func TestNodeAttributesStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	tpl := deployedTemplate(ctx, t, b, "attrs", "v1")
	inst := createInstance(ctx, t, b, tpl.ID, "ck")
	n := createNode(ctx, t, b, inst.ID, "worker")

	// Get on missing row → (nil, nil).
	missing, err := b.NodeAttributes().Get(ctx, n.ID)
	require.NoError(t, err)
	require.Nil(t, missing)

	// Upsert creates the row.
	require.NoError(t, b.NodeAttributes().Upsert(ctx, n.ID, 0, map[string]any{
		"a": float64(1), "b": "two",
	}))
	got, err := b.NodeAttributes().Get(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 0, got.RunAttempt)
	require.Equal(t, float64(1), got.Data["a"])
	require.Equal(t, "two", got.Data["b"])

	// MergeDelta does shallow merge.
	require.NoError(t, b.NodeAttributes().MergeDelta(ctx, n.ID, map[string]any{
		"b": "TWO", "c": float64(3),
	}))
	merged, err := b.NodeAttributes().Get(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, float64(1), merged.Data["a"])
	require.Equal(t, "TWO", merged.Data["b"])
	require.Equal(t, float64(3), merged.Data["c"])

	// IncrementRunAttempt bumps run_attempt.
	naStore := b.NodeAttributes().(*NodeAttributesStore)
	newAttempt, err := naStore.IncrementRunAttempt(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, 1, newAttempt)

	// ClearExecutorPopulated preserves source-driven fields, drops the rest.
	schema := map[string]any{
		"properties": map[string]any{
			"a": map[string]any{"type": "number", "source": "{{params.a}}"},
			"b": map[string]any{"type": "string"}, // executor-populated
			// c is not in the schema → kept verbatim.
		},
	}
	require.NoError(t, naStore.ClearExecutorPopulated(ctx, n.ID, schema))
	cleared, err := b.NodeAttributes().Get(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, float64(1), cleared.Data["a"], "source-driven 'a' preserved")
	_, hasB := cleared.Data["b"]
	require.False(t, hasB, "executor-populated 'b' cleared")
	require.Equal(t, float64(3), cleared.Data["c"], "schema-absent 'c' kept verbatim")

	// MergeDelta on a non-existent node returns ErrNoRows.
	err = b.NodeAttributes().MergeDelta(ctx, uuid.New(), map[string]any{"x": "y"})
	require.Error(t, err)
}

// -------- Lock holders --------

func TestLockHoldersStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	require.NoError(t, b.Supervisors().Register(ctx, storage.SupervisorRegisterInput{
		ID: "sup-A", AcceptedExecutors: []string{"exec"}, Concurrency: 1,
	}, nil))
	require.NoError(t, b.Supervisors().Register(ctx, storage.SupervisorRegisterInput{
		ID: "sup-B", AcceptedExecutors: []string{"exec"}, Concurrency: 1,
	}, nil))

	tpl := deployedTemplate(ctx, t, b, "locks", "v1")
	inst := createInstance(ctx, t, b, tpl.ID, "ck")
	n1 := createNode(ctx, t, b, inst.ID, "worker")
	n2 := createNode(ctx, t, b, inst.ID, "worker")

	// Insert a named-lock row (must be inside a tx).
	storeName := "content"
	regionRowID := uuid.New()
	namedRowID := uuid.New()
	now := time.Now().UTC()
	expiresFar := now.Add(10 * time.Minute)

	err := b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		lockName := "global-mutex"
		if err := b.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: namedRowID, LockKind: storage.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "sup-A", HolderNodeID: n1.ID,
			ExpiresAt: expiresFar,
		}, tx); err != nil {
			return err
		}
		regionData := json.RawMessage(`{"glob":"a/*"}`)
		intent := "rw"
		return b.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: regionRowID, LockKind: storage.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         regionData,
			Intent:             &intent,
			HolderSupervisorID: "sup-A", HolderNodeID: n2.ID,
			ExpiresAt: expiresFar,
		}, tx)
	})
	require.NoError(t, err)

	// UpdateAddress: writes the substrate-supplied address into a region
	// row inside the acquisition tx (§7.3 step-4e). Verify the round-trip.
	addr := json.RawMessage(`{"path":"a/abc"}`)
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return b.LockHolders().UpdateAddress(ctx, regionRowID, "sup-A", addr, tx)
	}))
	withAddr, err := b.LockHolders().Get(ctx, regionRowID, nil)
	require.NoError(t, err)
	require.NotNil(t, withAddr)
	require.JSONEq(t, string(addr), string(withAddr.Address))

	// Get + ListByHolderNode + ListBySupervisor work.
	got, err := b.LockHolders().Get(ctx, namedRowID, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, storage.LockKindNamed, got.LockKind)
	require.NotNil(t, got.LockName)
	require.Equal(t, "global-mutex", *got.LockName)

	rowsByNode, err := b.LockHolders().ListByHolderNode(ctx, n2.ID, nil)
	require.NoError(t, err)
	require.Len(t, rowsByNode, 1)
	require.Equal(t, regionRowID, rowsByNode[0].ID)

	rowsBySup, err := b.LockHolders().ListBySupervisor(ctx, "sup-A", nil)
	require.NoError(t, err)
	require.Len(t, rowsBySup, 2)

	// Set the region row to expired and verify ListExpired surfaces only it.
	pool := b.pool
	_, err = pool.Exec(ctx,
		`UPDATE rimsky_lock_holders SET expires_at = $1 WHERE id = $2`,
		now.Add(-time.Hour), regionRowID,
	)
	require.NoError(t, err)
	expired, err := b.LockHolders().ListExpired(ctx, nil)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	require.Equal(t, regionRowID, expired[0].ID)

	// Delete is claimant-guarded: wrong supervisor → no-op. Delete
	// requires a non-nil tx so the lock-holder DELETE commits atomically
	// with the rimsky-side terminal bookkeeping (the substrate-side
	// Release / Abandon runs in its own decoupled tx per v3 spec §7.3).
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return b.LockHolders().Delete(ctx, regionRowID, "sup-B", tx)
	}))
	stillThere, err := b.LockHolders().Get(ctx, regionRowID, nil)
	require.NoError(t, err)
	require.NotNil(t, stillThere, "wrong-supervisor delete must no-op")

	// Right supervisor → row gone.
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return b.LockHolders().Delete(ctx, regionRowID, "sup-A", tx)
	}))
	gone, err := b.LockHolders().Get(ctx, regionRowID, nil)
	require.NoError(t, err)
	require.Nil(t, gone)

	// RefreshHeartbeat only touches rows whose holder_node_id is currently
	// running and assigned to the supervisor (§7.5 invariant).
	client := b.LockHoldersClient()

	// Move n1 to running with assigned_supervisor_id = sup-A.
	require.NoError(t, b.Nodes().UpdateState(ctx, n1.ID, shared.NodeStateRunning, nodepkg.ReasonDispatchClaimed, nil))
	require.NoError(t, b.Nodes().UpdateHeartbeat(ctx, n1.ID, time.Now(), "sup-A", nil))

	// Capture pre-refresh expires_at.
	preRefresh, err := b.LockHolders().Get(ctx, namedRowID, nil)
	require.NoError(t, err)
	preExpires := preRefresh.ExpiresAt

	// Refresh with a far-future bound (1 hour) so the row's expires_at
	// observably advances past the original 10-minute mark.
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, client.RefreshHeartbeat(ctx, "sup-A", 3600))

	postRefresh, err := b.LockHolders().Get(ctx, namedRowID, nil)
	require.NoError(t, err)
	require.True(t, postRefresh.ExpiresAt.After(preExpires),
		"running-node lock-holder row's expires_at should advance")

	// And a row whose holder_node_id is NOT in 'running' state must NOT
	// be refreshed by RefreshHeartbeat (the §7.5 filter).
	// Insert another lock-holder row anchored to n2 (state='stale') and
	// verify its expires_at is unchanged after a refresh.
	otherRowID := uuid.New()
	otherStoreName := "topics"
	otherRegion := json.RawMessage(`{"id":"i-77"}`)
	otherIntent := "rw"
	otherExpires := time.Now().UTC().Add(5 * time.Minute)
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return b.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: otherRowID, LockKind: storage.LockKindRegion,
			StoreName: &otherStoreName, RegionData: otherRegion, Intent: &otherIntent,
			HolderSupervisorID: "sup-A", HolderNodeID: n2.ID,
			ExpiresAt: otherExpires,
		}, tx)
	}))
	preOther, err := b.LockHolders().Get(ctx, otherRowID, nil)
	require.NoError(t, err)
	require.NoError(t, client.RefreshHeartbeat(ctx, "sup-A", 7200))
	postOther, err := b.LockHolders().Get(ctx, otherRowID, nil)
	require.NoError(t, err)
	require.WithinDuration(t, preOther.ExpiresAt, postOther.ExpiresAt, 1*time.Millisecond,
		"non-running-node lock-holder row must NOT be refreshed (§7.5 filter)")
}

// -------- Claim holders --------

func TestClaimHoldersStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	require.NoError(t, b.Supervisors().Register(ctx, storage.SupervisorRegisterInput{
		ID: "sup-A", AcceptedExecutors: []string{"worker"}, Concurrency: 1,
	}, nil))

	tpl := deployedTemplate(ctx, t, b, "ch", "v1")
	inst := createInstance(ctx, t, b, tpl.ID, "ck")
	acquirer := createNode(ctx, t, b, inst.ID, "worker")
	terminalA := createNode(ctx, t, b, inst.ID, "worker")
	terminalB := createNode(ctx, t, b, inst.ID, "worker")

	// The parent lock_holder row must exist before any claim_holders FK
	// inserts can land — under v2 claim_holders cascades on its
	// lock_holder_id.
	storeName := "topics"
	regionData := json.RawMessage(`{"id":"item-42"}`)
	intent := "rw"
	lockHolderID := uuid.New()
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return b.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: lockHolderID, LockKind: storage.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         regionData,
			Intent:             &intent,
			HolderSupervisorID: "sup-A",
			HolderNodeID:       acquirer.ID,
			ExpiresAt:          time.Now().UTC().Add(10 * time.Minute),
		}, tx)
	}))

	// Insert one claim_holders row per terminal.
	rowAID := uuid.New()
	rowBID := uuid.New()
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		if err := b.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID:           rowAID,
			LockHolderID: lockHolderID,
			HolderNodeID: terminalA.ID,
		}, tx); err != nil {
			return err
		}
		return b.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID:           rowBID,
			LockHolderID: lockHolderID,
			HolderNodeID: terminalB.ID,
		}, tx)
	}))

	all, err := b.ClaimHolders().ListByLockHolderID(ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Len(t, all, 2)

	active, err := b.ClaimHolders().ListActiveByLockHolderID(ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Len(t, active, 2)

	// ListByHolderNode returns terminalA's single row.
	byNodeA, err := b.ClaimHolders().ListByHolderNode(ctx, terminalA.ID, nil)
	require.NoError(t, err)
	require.Len(t, byNodeA, 1)
	require.Equal(t, rowAID, byNodeA[0].ID)

	// Complete terminalA's row → 'completed'. The active list shrinks to
	// one and Get returns the completed row.
	require.NoError(t, b.ClaimHolders().Complete(ctx, rowAID, storage.ClaimHolderStateCompleted, nil))

	gotA, err := b.ClaimHolders().Get(ctx, rowAID, nil)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	require.Equal(t, storage.ClaimHolderStateCompleted, gotA.State)
	require.NotNil(t, gotA.CompletedAt)

	activeAfter, err := b.ClaimHolders().ListActiveByLockHolderID(ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Len(t, activeAfter, 1)
	require.Equal(t, terminalB.ID, activeAfter[0].HolderNodeID)

	// Complete is idempotent: re-running on an already-completed row is a no-op.
	// The state must remain 'completed' even if a different terminal state is requested.
	require.NoError(t, b.ClaimHolders().Complete(ctx, rowAID, storage.ClaimHolderStateFailed, nil))
	gotA2, err := b.ClaimHolders().Get(ctx, rowAID, nil)
	require.NoError(t, err)
	require.Equal(t, storage.ClaimHolderStateCompleted, gotA2.State,
		"already-completed row's state must not be overwritten")

	// Failing terminalB → 'failed' is a valid first-time transition.
	require.NoError(t, b.ClaimHolders().Complete(ctx, rowBID, storage.ClaimHolderStateFailed, nil))
	gotB, err := b.ClaimHolders().Get(ctx, rowBID, nil)
	require.NoError(t, err)
	require.Equal(t, storage.ClaimHolderStateFailed, gotB.State)

	// Cascade: deleting the parent lock_holder row removes both
	// claim_holders rows.
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return b.LockHolders().Delete(ctx, lockHolderID, "sup-A", tx)
	}))
	gone, err := b.ClaimHolders().Get(ctx, rowAID, nil)
	require.NoError(t, err)
	require.Nil(t, gone, "claim_holders row must cascade-delete with parent lock_holder")
}

// TestClaimHoldersStore_CompleteByLockHolderAndNode exercises the
// targeted UPDATE path used by the supervisor's terminal release loop:
// per-node-per-lock-holder flip in a single round-trip, idempotent on
// already-completed rows, no cross-row clobber.
func TestClaimHoldersStore_CompleteByLockHolderAndNode(t *testing.T) {
	t.Parallel()
	b, _, teardown := newBackend(t)
	defer teardown()
	ctx := context.Background()

	tmpl := deployedTemplate(ctx, t, b, "complete-targeted", "1")
	inst := createInstance(ctx, t, b, tmpl.ID, "ck-complete-targeted")
	acquirer := createNode(ctx, t, b, inst.ID, "acq")
	terminalA := createNode(ctx, t, b, inst.ID, "tA")
	terminalB := createNode(ctx, t, b, inst.ID, "tB")

	storeName := "scenario-store"
	intent := "rw"
	lockHolderID := uuid.New()
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return b.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: lockHolderID, LockKind: storage.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         json.RawMessage(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-X",
			HolderNodeID:       acquirer.ID,
			ExpiresAt:          time.Now().UTC().Add(10 * time.Minute),
		}, tx)
	}))
	rowAID := uuid.New()
	rowBID := uuid.New()
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		if err := b.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: rowAID, LockHolderID: lockHolderID, HolderNodeID: terminalA.ID,
		}, tx); err != nil {
			return err
		}
		return b.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: rowBID, LockHolderID: lockHolderID, HolderNodeID: terminalB.ID,
		}, tx)
	}))

	// Targeted complete on terminalA only.
	require.NoError(t, b.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, terminalA.ID, storage.ClaimHolderStateCompleted, nil,
	))
	gotA, err := b.ClaimHolders().Get(ctx, rowAID, nil)
	require.NoError(t, err)
	require.Equal(t, storage.ClaimHolderStateCompleted, gotA.State)
	gotB, err := b.ClaimHolders().Get(ctx, rowBID, nil)
	require.NoError(t, err)
	require.Equal(t, storage.ClaimHolderStateActive, gotB.State,
		"sibling row must not be touched by targeted complete")

	// Re-running on already-completed row is a no-op (state filter).
	require.NoError(t, b.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, terminalA.ID, storage.ClaimHolderStateFailed, nil,
	))
	gotA2, err := b.ClaimHolders().Get(ctx, rowAID, nil)
	require.NoError(t, err)
	require.Equal(t, storage.ClaimHolderStateCompleted, gotA2.State,
		"already-completed row must not be overwritten")
}

// TestLockHoldersStore_FrameIDRoundTrip confirms the storage-layer
// insert/update path persists FrameID on rimsky_lock_holders. Per spec
// §12.10 the column is observability-only — no algorithm consults it,
// but operators read it, and this test pins the storage adapter
// contract.
func TestLockHoldersStore_FrameIDRoundTrip(t *testing.T) {
	t.Parallel()
	b, pool, teardown := newBackend(t)
	defer teardown()
	ctx := context.Background()

	tmpl := deployedTemplate(ctx, t, b, "frame-id-roundtrip", "1")
	inst := createInstance(ctx, t, b, tmpl.ID, "ck-frame-id-roundtrip")
	n := createNode(ctx, t, b, inst.ID, "worker")

	frameID := shared.UUID(uuid.New())
	// Seed a frame row so the rimsky_lock_holders frame_id observability
	// reference points at a real frame (no FK in the v2 schema; this
	// keeps the test self-documenting).
	_, err := pool.Exec(ctx, `
INSERT INTO rimsky_frames (frame_id, instance_id, mode, state, source_node_ids, frame_timeout_ms)
VALUES ($1, $2, 'serial_queue', 'queued', ARRAY[$3]::uuid[], 60000)
`, frameID, inst.ID, n.ID)
	require.NoError(t, err)

	storeName := "scenario-store"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, b.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return b.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: lockHolderID, LockKind: storage.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         json.RawMessage(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-Y",
			HolderNodeID:       n.ID,
			ExpiresAt:          time.Now().UTC().Add(10 * time.Minute),
			FrameID:            &frameID,
		}, tx)
	}))

	row, err := b.LockHolders().Get(ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.NotNil(t, row.FrameID)
	require.Equal(t, frameID, *row.FrameID)
}
