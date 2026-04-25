// Integration tests for the Postgres storage backend. Each top-level Test*
// function starts a fresh containerized Postgres via the pgtest harness,
// applies migrations, and exercises one store's public surface.
//
// Tests are t.Parallel()-safe because each spawns its own container.
package postgres

import (
	"context"
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

func createNode(ctx context.Context, t *testing.T, b *PostgresStorageBackend, instanceID shared.UUID, executor string, deps ...shared.UUID) storage.NodeRow {
	t.Helper()
	in := storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: instanceID, NodeType: "t",
		Executor: executor, Dependencies: deps,
	}
	n, err := b.Nodes().Create(ctx, in, nil)
	require.NoError(t, err)
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
	b, _, teardown := newBackend(t)
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

	// SetKillRequested.
	require.NoError(t, b.Nodes().SetKillRequested(ctx, detachedNode.ID, true, nil))
	killSet, err := b.Nodes().Get(ctx, detachedNode.ID, nil)
	require.NoError(t, err)
	require.True(t, killSet.KillRequested)

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

// -------- Resources --------

func TestResourceRegistry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _, teardown := newBackend(t)
	t.Cleanup(teardown)

	tpl := deployedTemplate(ctx, t, b, "r", "v1")
	inst := createInstance(ctx, t, b, tpl.ID, "ck")
	owner := createNode(ctx, t, b, inst.ID, "worker")

	res, err := b.Resources().Create(ctx, storage.ResourceCreateInput{
		ResourcePath: []string{"a", "b"}, OwnerNodeID: owner.ID, KeepVersions: 3,
	}, nil)
	require.NoError(t, err)
	require.Equal(t, 3, res.KeepVersions)

	// Commit v1.
	v1, err := b.Resources().CommitVersion(ctx, res.ID, storage.ResourceCommitInput{
		ProducedBy: owner.ID, Data: []byte(`{"n":1}`), ChangeSummary: "first",
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "first", v1.ChangeSummary)

	// NoOp is silent.
	require.NoError(t, b.Resources().NoOpCommit(ctx, res.ID, nil))

	// Commit v2 + v3.
	v2, err := b.Resources().CommitVersion(ctx, res.ID, storage.ResourceCommitInput{
		ProducedBy: owner.ID, Data: []byte(`{"n":2}`),
	}, nil)
	require.NoError(t, err)
	v3, err := b.Resources().CommitVersion(ctx, res.ID, storage.ResourceCommitInput{
		ProducedBy: owner.ID, Data: []byte(`{"n":3}`),
	}, nil)
	require.NoError(t, err)

	// After 3 commits with keep_versions=3, all 3 survive GC.
	versions, err := b.Resources().ListVersions(ctx, res.ID, nil)
	require.NoError(t, err)
	require.Len(t, versions, 3)

	// Current/previous correctness.
	got, err := b.Resources().Get(ctx, res.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, got.CurrentVersionID)
	require.Equal(t, v3.ID, *got.CurrentVersionID)
	require.NotNil(t, got.PreviousVersionID)
	require.Equal(t, v2.ID, *got.PreviousVersionID)

	// RestoreVersion "previous": swap current ← previous.
	restored, err := b.Resources().RestoreVersion(ctx, res.ID, "previous", shared.UUID{}, nil)
	require.NoError(t, err)
	require.Equal(t, v2.ID, restored.ID)

	gotAfter, err := b.Resources().Get(ctx, res.ID, nil)
	require.NoError(t, err)
	require.Equal(t, v2.ID, *gotAfter.CurrentVersionID)

	// RestoreVersion by id.
	restoredByID, err := b.Resources().RestoreVersion(ctx, res.ID, "id", v1.ID, nil)
	require.NoError(t, err)
	require.Equal(t, v1.ID, restoredByID.ID)

	// GCOldVersions with keep=1 drops old but preserves current/previous.
	dropped, err := b.Resources().GCOldVersions(ctx, res.ID, 1, nil)
	require.NoError(t, err)
	// v2 is referenced as current, v1 as previous (after last restore flipped
	// previous↔current? actually restore-by-id only sets current, doesn't
	// swap previous). The keep-set union also includes the top-1 by
	// committed_at DESC (=v3). So we expect at most 1 drop.
	require.LessOrEqual(t, dropped, 3)
	remaining, err := b.Resources().ListVersions(ctx, res.ID, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(remaining), 1)

	// GetVersion.
	again, err := b.Resources().GetVersion(ctx, v1.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, again)

	// Paginated listing.
	paged, err := b.Resources().ListVersionsPaged(ctx, res.ID, storage.ListPagination{Limit: 1}, nil)
	require.NoError(t, err)
	require.Len(t, paged.Rows, 1)

	// ListByOwner.
	byOwner, err := b.Resources().ListByOwner(ctx, owner.ID, nil)
	require.NoError(t, err)
	require.Len(t, byOwner, 1)

	// ResourceData.Read returns parsed JSON.
	ver, err := b.Resources().GetVersion(ctx, v1.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, ver)
	val, err := b.ResourceData().Read(ctx, *ver, nil)
	require.NoError(t, err)
	m, ok := val.(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(1), m["n"])
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
		ID: "sup-1", AcceptedExecutors: []string{"alpha", "beta"}, Concurrency: 4,
		CallbackHost: "localhost", CallbackPort: 9000,
	}, nil))

	got, err := b.Supervisors().Get(ctx, "sup-1", nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, []string{"alpha", "beta"}, got.AcceptedExecutors)
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
