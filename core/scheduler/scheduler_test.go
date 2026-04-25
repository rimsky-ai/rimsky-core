// Tests for the scheduler main loop (Start / tick / sweeps). Uses the
// pgtest harness plus the real Postgres storage + Postgres DispatchQueue so
// the advisory-lock path is exercised end-to-end.
package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	nodepkg "github.com/fallguy/rimsky/core/node"
	queuepkg "github.com/fallguy/rimsky/core/queue"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// --- Test harness -----------------------------------------------------

type schedFixture struct {
	pool     *pgxpool.Pool
	storage  *pgstorage.PostgresStorageBackend
	queue    *pgqueue.Queue
	clock    shared.Clock
	log      shared.Logger
	instance storage.InstanceRow
}

func newSchedFixture(t *testing.T) (*schedFixture, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	sb := pgstorage.New(pool)

	tpl, err := sb.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: "sched-loop-" + uuid.NewString(), Version: "v1",
		Nodes: []nodepkg.TemplateNodeDef{},
	}, nil)
	require.NoError(t, err)
	inst, err := sb.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: tpl.ID, ConsumerKey: "ck-" + uuid.NewString(),
		Params: map[string]any{},
	}, nil)
	require.NoError(t, err)

	return &schedFixture{
		pool:     pool,
		storage:  sb,
		queue:    pgqueue.New(pool),
		clock:    shared.SystemClock{},
		log:      shared.SilentLogger{},
		instance: inst,
	}, teardown
}

// createNode inserts a node and forces its state via UPDATE for paths that
// need to skip the state-machine (e.g. directly creating a running node).
func (f *schedFixture) createNode(t *testing.T, executor string, state shared.NodeState, deps ...shared.UUID) storage.NodeRow {
	t.Helper()
	ctx := context.Background()
	n, err := f.storage.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance.ID, NodeType: "t",
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(t, err)
	if state != shared.NodeStateStale {
		_, err := f.pool.Exec(ctx,
			`UPDATE rimsky_nodes SET state = $1 WHERE id = $2`, string(state), n.ID)
		require.NoError(t, err)
		n.State = state
	}
	return n
}

// setHeartbeat forces last_heartbeat_at + assigned_supervisor_id directly.
func (f *schedFixture) setHeartbeat(t *testing.T, nodeID shared.UUID, at time.Time, sup string) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(),
		`UPDATE rimsky_nodes SET last_heartbeat_at = $1, assigned_supervisor_id = $2 WHERE id = $3`,
		at, sup, nodeID,
	)
	require.NoError(t, err)
}

// --- Tests ------------------------------------------------------------

func TestScheduler_TicksAndStops(t *testing.T) {
	t.Parallel()
	f, teardown := newSchedFixture(t)
	t.Cleanup(teardown)

	h := Start(Config{
		Storage:          f.storage,
		Queue:            f.queue,
		Clock:            f.clock,
		Logger:           f.log,
		TickInterval:     50 * time.Millisecond,
		HeartbeatTimeout: 15 * time.Second,
		Pool:             f.pool,
	})

	// Give the loop a couple of ticks to run.
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(ctx))
}

func TestScheduler_ReadySweep_EnqueuesExecutorNodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newSchedFixture(t)
	t.Cleanup(teardown)

	// Dep is fresh; target is stale + executor-backed → ListReadyForDispatch
	// should pick it up.
	dep := f.createNode(t, "worker", shared.NodeStateFresh)
	target := f.createNode(t, "runner", shared.NodeStateStale, dep.ID)

	err := tick(ctx, Config{
		Storage: f.storage, Queue: f.queue,
		Clock: f.clock, Logger: f.log,
		HeartbeatTimeout:     15 * time.Second,
		OrphanedClaimTimeout: 75 * time.Second,
		Pool:                 f.pool,
	})
	require.NoError(t, err)

	// Dispatch row exists for the target.
	var count int
	err = f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_dispatch WHERE node_id = $1`, target.ID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "expected a dispatch row for the ready node")
}

func TestScheduler_StaleHeartbeat_Reenqueues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newSchedFixture(t)
	t.Cleanup(teardown)

	// Create a running node with an old heartbeat.
	n := f.createNode(t, "worker", shared.NodeStateRunning)
	f.setHeartbeat(t, n.ID, time.Now().Add(-5*time.Minute), "sup-dead")

	err := tick(ctx, Config{
		Storage: f.storage, Queue: f.queue,
		Clock: f.clock, Logger: f.log,
		HeartbeatTimeout:     15 * time.Second,
		OrphanedClaimTimeout: 75 * time.Second,
		Pool:                 f.pool,
	})
	require.NoError(t, err)

	// Node should be stale now.
	after, err := f.storage.Nodes().Get(ctx, n.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, shared.NodeStateStale, after.State)

	// heartbeat_lost event appended.
	events, err := f.storage.Events().List(ctx,
		storage.EventListFilter{NodeID: &n.ID, Kind: "heartbeat_lost"},
		storage.ListPagination{Limit: 10}, nil,
	)
	require.NoError(t, err)
	require.NotEmpty(t, events.Events, "expected a heartbeat_lost event")

	// Dispatch row was re-enqueued.
	var count int
	err = f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_dispatch WHERE node_id = $1`, n.ID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "expected a re-enqueued dispatch row")
}

func TestScheduler_OrphanedClaim_Released(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newSchedFixture(t)
	t.Cleanup(teardown)

	// Stale node with a claimed dispatch row whose claimed_at is past the
	// cutoff. ListOrphanedClaims + ReleaseClaim should fire.
	n := f.createNode(t, "worker", shared.NodeStateStale)

	// Enqueue + claim in one supervisor, then backdate claimed_at.
	require.NoError(t, f.queue.Enqueue(ctx, queuepkg.DispatchRequest{
		NodeID: n.ID, ExecutorName: "worker", EnqueuedAt: time.Now(),
	}))
	claimed, err := f.queue.Claim(ctx, "sup-dead", []string{"worker"}, map[string]int{})
	require.NoError(t, err)
	require.NotNil(t, claimed)

	// Backdate claimed_at so it's past 75s (5 × 15s) cutoff.
	_, err = f.pool.Exec(ctx,
		`UPDATE rimsky_dispatch SET claimed_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`,
		claimed.ID,
	)
	require.NoError(t, err)

	err = tick(ctx, Config{
		Storage: f.storage, Queue: f.queue,
		Clock: f.clock, Logger: f.log,
		HeartbeatTimeout:     15 * time.Second,
		OrphanedClaimTimeout: 75 * time.Second,
		Pool:                 f.pool,
	})
	require.NoError(t, err)

	// Claim released: claimed_by IS NULL.
	own, err := f.queue.GetClaimedBy(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "unclaimed", own.Kind,
		"expected orphan claim to be released")

	// orphaned_claim_released event appended.
	events, err := f.storage.Events().List(ctx,
		storage.EventListFilter{NodeID: &n.ID, Kind: "orphaned_claim_released"},
		storage.ListPagination{Limit: 10}, nil,
	)
	require.NoError(t, err)
	require.NotEmpty(t, events.Events, "expected an orphaned_claim_released event")
}

func TestScheduler_AdvisoryLockBlocksSecondReplica(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newSchedFixture(t)
	t.Cleanup(teardown)

	// Create a ready node — if the tick runs it will be enqueued.
	dep := f.createNode(t, "worker", shared.NodeStateFresh)
	target := f.createNode(t, "runner", shared.NodeStateStale, dep.ID)

	// Acquire the advisory lock on a separate connection and hold it while
	// we call tick. tick should see pg_try_advisory_lock → false and skip.
	holder, err := f.pool.Acquire(ctx)
	require.NoError(t, err)
	// Release via wrapper so the deferred release below always runs exactly
	// once even if we unlock early.
	released := false
	defer func() {
		if !released {
			_, _ = holder.Exec(context.Background(),
				"SELECT pg_advisory_unlock($1)", RimskySchedulerTickLockKey)
			holder.Release()
		}
	}()

	var got bool
	require.NoError(t, holder.QueryRow(ctx,
		"SELECT pg_try_advisory_lock($1)", RimskySchedulerTickLockKey,
	).Scan(&got))
	require.True(t, got, "test holder should acquire the lock")

	// The tick needs to Acquire a *different* connection from the pool to
	// attempt its lock. Use a background goroutine + short wait so we don't
	// wedge this test if the pool is single-capacity.
	done := make(chan error, 1)
	go func() {
		done <- tick(ctx, Config{
			Storage: f.storage, Queue: f.queue,
			Clock: f.clock, Logger: f.log,
			HeartbeatTimeout: 15 * time.Second,
			Pool:             f.pool,
		})
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("tick did not return within 10s while other replica held the lock")
	}

	// Skipped tick means the ready-sweep did NOT run → no dispatch row.
	var count int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_dispatch WHERE node_id = $1`, target.ID,
	).Scan(&count))
	assert.Equal(t, 0, count, "tick should have skipped under advisory-lock contention")

	// Release for hygiene.
	_, _ = holder.Exec(ctx,
		"SELECT pg_advisory_unlock($1)", RimskySchedulerTickLockKey)
	holder.Release()
	released = true
}

// --- Compile-time wiring check ----------------------------------------
//
// Ensures the dispatcher adapter continues to satisfy the MessageDispatcher
// surface even after future storage/queue changes.
var _ MessageDispatcher = scheduleDispatcherAdapter{}

// Suppress unused-variable warning from `sync` if the file gets trimmed.
var _ = sync.Mutex{}
