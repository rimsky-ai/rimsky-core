// Tests for the scheduler main loop (Start / tick / sweeps). Uses the
// pgtest harness plus the real Postgres-backed persistence.Driver so
// the advisory-lock path is exercised end-to-end.
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

// pgSchedulerTickLockKey mirrors the constant in
// core/persistence/postgres/coordinator.go, duplicated here so the
// scheduler-tests package does not need to import the postgres driver
// package directly. Kept in sync via the conformance suite (any drift
// would surface as a test passing here while the postgres coordinator's
// TrySchedulerTick blocks on a different key).
const pgSchedulerTickLockKey int64 = 4853127298010834892

// --- Test harness -----------------------------------------------------

type schedFixture struct {
	persist     persistence.Store
	queue       persistence.Queue
	coordinator persistence.Coordinator
	driver      persistence.Driver
	clock       shared.Clock
	log         shared.Logger
	instance    persistence.InstanceRow
}

func newSchedFixture(t *testing.T) *schedFixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	tpl := insertDeployedTemplate(ctx, t, d.Store(), nodepkg.TemplateSpec{
		Name: "sched-loop-" + uuid.NewString(), Version: "v1",
		FrameResolution: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:  nodepkg.FrameTimeoutDefaultMs,
		Nodes:           []nodepkg.TemplateNodeDef{},
	})
	ck := "ck-" + uuid.NewString()
	inst, err := d.Store().Instances().Create(ctx, persistence.InstanceCreateInput{
		ID: uuid.New(), TemplateHash: tpl.ID, InstanceKey: &ck,
		Params: map[string]any{},
	}, nil)
	require.NoError(t, err)

	return &schedFixture{
		persist:     d.Store(),
		queue:       d.Queue(),
		coordinator: d.Coordinator(),
		driver:      d,
		clock:       shared.SystemClock{},
		log:         shared.SilentLogger{},
		instance:    inst,
	}
}

// createNode inserts a node and forces its state via UPDATE for paths that
// need to skip the state-machine (e.g. directly creating a running node).
// Non-fresh nodes (stale/running) get a frame_id pointing at a freshly-
// seeded running rimsky_frames row so the dispatch enqueue path
// (blessed-invariant 19) can read frame_id from the node row.
func (f *schedFixture) createNode(t *testing.T, executor string, state shared.NodeState, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	ctx := context.Background()
	n, err := f.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance.ID, NodeType: "t",
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(t, err)
	// Always UPDATE: Create() now defaults to 'fresh' (frame-resolution
	// model), so any test asking for a non-fresh state must override.
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_nodes SET state = $1 WHERE id = $2`, string(state), n.ID)
	n.State = state
	if state == shared.NodeStateStale || state == shared.NodeStateRunning {
		// Reuse the existing running frame for this instance if any (the
		// uq_rimsky_frames_running partial unique index limits one running
		// frame per instance); otherwise insert a fresh one.
		frameID, ok := lookupRunningFrame(ctx, t, f, f.instance.ID)
		if !ok {
			frameID = insertRunningFrame(ctx, t, f, f.instance.ID, n.ID)
		}
		pgtest.ExecForTest(ctx, t, f.driver,
			`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, n.ID)
		n.FrameID = &frameID
	}
	return n
}

// lookupRunningFrame returns the running frame_id for instanceID, or
// (zero, false) when none exists. Uses a count-then-fetch pattern so
// the test helper does not fatal on no-rows.
func lookupRunningFrame(ctx context.Context, t *testing.T, f *schedFixture, instanceID shared.UUID) (shared.UUID, bool) {
	t.Helper()
	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver, `
        SELECT COUNT(*) FROM rimsky_frames
        WHERE instance_id = $1 AND state = 'running'
    `, []any{instanceID}, &count)
	if count == 0 {
		return shared.UUID{}, false
	}
	var got shared.UUID
	pgtest.QueryRowForTest(ctx, t, f.driver, `
        SELECT frame_id FROM rimsky_frames
        WHERE instance_id = $1 AND state = 'running'
        ORDER BY started_at DESC LIMIT 1
    `, []any{instanceID}, &got)
	return got, true
}

// insertRunningFrame inserts a new running rimsky_frames row anchored to
// (instanceID, sourceNodeID) and returns its frame_id.
func insertRunningFrame(ctx context.Context, t *testing.T, f *schedFixture, instanceID, sourceNodeID shared.UUID) shared.UUID {
	t.Helper()
	var frameID shared.UUID
	pgtest.QueryRowForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
        VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
        RETURNING frame_id
    `, []any{instanceID, sourceNodeID}, &frameID)
	return frameID
}

// setHeartbeat forces last_heartbeat_at + assigned_supervisor_id directly.
func (f *schedFixture) setHeartbeat(t *testing.T, nodeID shared.UUID, at time.Time, sup string) {
	t.Helper()
	pgtest.ExecForTest(context.Background(), t, f.driver,
		`UPDATE rimsky_nodes SET last_heartbeat_at = $1, assigned_supervisor_id = $2 WHERE id = $3`,
		at, sup, nodeID,
	)
}

// schedConfig returns a Config wired to the fixture's persistence layer.
func (f *schedFixture) schedConfig() Config {
	return Config{
		Persist:              f.persist,
		Queue:                f.queue,
		Coordinator:          f.coordinator,
		Clock:                f.clock,
		Logger:               f.log,
		HeartbeatTimeout:     15 * time.Second,
		OrphanedClaimTimeout: 75 * time.Second,
	}
}

// --- Tests ------------------------------------------------------------

func TestScheduler_TicksAndStops(t *testing.T) {
	t.Parallel()
	f := newSchedFixture(t)

	cfg := f.schedConfig()
	cfg.TickInterval = 50 * time.Millisecond
	h := Start(cfg)

	// Give the loop a couple of ticks to run.
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(ctx))
}

func TestScheduler_ReadySweep_EnqueuesExecutorNodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	// Dep is fresh; target is stale + executor-backed → ListReadyForDispatch
	// should pick it up.
	dep := f.createNode(t, "worker", shared.NodeStateFresh)
	target := f.createNode(t, "runner", shared.NodeStateStale, dep.ID)

	require.NoError(t, tick(ctx, f.schedConfig()))

	// Dispatch row exists for the target.
	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_dispatch WHERE node_id = $1`,
		[]any{target.ID}, &count)
	assert.Equal(t, 1, count, "expected a dispatch row for the ready node")
}

func TestScheduler_StaleHeartbeat_Reenqueues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	// Create a running node with an old heartbeat.
	n := f.createNode(t, "worker", shared.NodeStateRunning)
	f.setHeartbeat(t, n.ID, time.Now().Add(-5*time.Minute), "sup-dead")

	require.NoError(t, tick(ctx, f.schedConfig()))

	// Node should be stale now.
	after, err := f.persist.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shared.NodeStateStale, after.State)

	// heartbeat_lost event appended.
	events, err := f.persist.Events().List(ctx,
		persistence.EventListFilter{NodeID: &n.ID, Kind: "heartbeat_lost"},
		persistence.ListPagination{Limit: 10}, nil,
	)
	require.NoError(t, err)
	require.NotEmpty(t, events.Events, "expected a heartbeat_lost event")

	// Dispatch row was re-enqueued.
	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_dispatch WHERE node_id = $1`,
		[]any{n.ID}, &count)
	assert.Equal(t, 1, count, "expected a re-enqueued dispatch row")
}

func TestScheduler_OrphanedClaim_Released(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	// Stale node with a claimed dispatch row whose claimed_at is past the
	// cutoff. ListOrphanedClaims + ReleaseClaim should fire.
	n := f.createNode(t, "worker", shared.NodeStateStale)
	require.NotNil(t, n.FrameID)
	// EnqueuedAt deliberately in the past so the SelectCandidates
	// `enqueued_at <= NOW()` predicate isn't flaky against the postgres
	// container's clock.
	require.NoError(t, f.queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID: n.ID, ExecutorName: "worker", EnqueuedAt: time.Now().Add(-time.Second),
		FrameID: *n.FrameID,
	}))

	var dispatchID shared.UUID
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		candidates, err := f.queue.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"worker"},
			AcceptedStores:    []string{},
			Limit:             1,
		})
		if err != nil {
			return err
		}
		require.Len(t, candidates, 1)
		dispatchID = candidates[0].DispatchID
		ok, err := f.queue.ClaimDispatchRow(ctx, tx, dispatchID, "sup-dead")
		if err != nil {
			return err
		}
		require.True(t, ok)
		return nil
	}))

	// Backdate last_heartbeat_at so it's past 75s (5 × 15s) cutoff.
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_dispatch SET last_heartbeat_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`,
		dispatchID,
	)

	require.NoError(t, tick(ctx, f.schedConfig()))

	// Claim released: claimed_by IS NULL.
	own, err := f.queue.GetClaimedBy(ctx, dispatchID)
	require.NoError(t, err)
	assert.Equal(t, "unclaimed", own.Kind,
		"expected orphan claim to be released")

	// orphaned_claim_released event appended.
	events, err := f.persist.Events().List(ctx,
		persistence.EventListFilter{NodeID: &n.ID, Kind: "orphaned_claim_released"},
		persistence.ListPagination{Limit: 10}, nil,
	)
	require.NoError(t, err)
	require.NotEmpty(t, events.Events, "expected an orphaned_claim_released event")
}

// TestScheduler_AdvisoryLockBlocksSecondReplica pins blessed-invariant 7:
// when one scheduler replica already holds the per-tick advisory lock
// (TrySchedulerTick has returned held=true), a second tick observes
// held=false and skips its sweeps. The first replica's lock-holding is
// faked via pgtest.HoldAdvisoryLock against the per-tick lock key
// duplicated as `pgSchedulerTickLockKey` above.
func TestScheduler_AdvisoryLockBlocksSecondReplica(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	// Create a ready node — if the tick runs it will be enqueued.
	dep := f.createNode(t, "worker", shared.NodeStateFresh)
	target := f.createNode(t, "runner", shared.NodeStateStale, dep.ID)

	// Acquire the advisory lock on a separate connection and hold it while
	// we call tick. tick should see TrySchedulerTick → held=false and skip.
	release := pgtest.HoldAdvisoryLock(ctx, t, f.driver, pgSchedulerTickLockKey)
	defer release()

	// The tick needs to Acquire a *different* connection from the pool to
	// attempt its lock. Use a background goroutine + short wait so we don't
	// wedge this test if the pool is single-capacity.
	done := make(chan error, 1)
	go func() {
		done <- tick(ctx, f.schedConfig())
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("tick did not return within 10s while other replica held the lock")
	}

	// Skipped tick means the ready-sweep did NOT run → no dispatch row.
	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_dispatch WHERE node_id = $1`,
		[]any{target.ID}, &count)
	assert.Equal(t, 0, count, "tick should have skipped under advisory-lock contention")
}

// --- Compile-time wiring check ----------------------------------------
//
// Ensures the dispatcher adapter continues to satisfy the MessageDispatcher
// surface even after future storage/queue changes.
var _ MessageDispatcher = scheduleDispatcherAdapter{}

// Suppress unused-variable warning from `sync` if the file gets trimmed.
var _ = sync.Mutex{}
