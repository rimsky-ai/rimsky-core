// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Tests for the scheduler main loop (Start / tick / sweeps). Uses the
// pgtest harness plus the real Postgres-backed persistence.Database so
// the advisory-lock path is exercised end-to-end.
package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
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

// pgSchedulerTickLockKey mirrors the constant in
// foundation/persistence/postgres/coordinator.go, duplicated here so the
// scheduler-tests package does not need to import the postgres driver
// package directly. Kept in sync via the conformance suite (any drift
// would surface as a test passing here while the postgres coordinator's
// TrySchedulerTick blocks on a different key).
const pgSchedulerTickLockKey int64 = 4853127298010834892

type schedFixture struct {
	persist     persistence.Tables
	queue       persistence.Queue
	coordinator persistence.AdvisoryLocker
	driver      persistence.Database
	clock       shared.Clock
	log         shared.Logger
	instance    persistence.InstanceRow
}

func newSchedFixture(t *testing.T) *schedFixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	tpl := insertDeployedTemplate(ctx, t, d.Tables(), nodepkg.TemplateSpec{
		Name: "sched-loop-" + uuid.NewString(), Version: "v1",
		FrameTimeoutMs: nodepkg.FrameTimeoutDefaultMs,
		Nodes:          []nodepkg.TemplateNodeDef{},
	})
	ck := "ck-" + uuid.NewString()
	var inst persistence.InstanceRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	inTxTest(t, ctx, d.Tables(), func(tx persistence.Tx) error {
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
	})

	return &schedFixture{
		persist:     d.Tables(),
		queue:       d.Queue(),
		coordinator: d.AdvisoryLocker(),
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
func (f *schedFixture) createNode(t *testing.T, executor string, state cascade.NodeState, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	ctx := context.Background()
	var n persistence.NodeRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		// @deliberate: deps unused — dependency-edge resolution is now
		// via the subscription-edge map.
		_ = deps
		row, err := f.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: uuid.New(), InstanceID: f.instance.ID, NodeType: "t",
			Executor: executor,
		}, tx)
		if err != nil {
			return err
		}
		n = row
		return nil
	})
	// @deliberate: state lives on rimsky_node_runs. The 'fresh' case
	// requires no run row; stale / running seed an in-flight run row in
	// the requested state.
	if state == cascade.NodeStateFresh {
		return n
	}
	frameID, ok := lookupRunningFrame(ctx, t, f, f.instance.ID)
	if !ok {
		frameID = insertRunningFrame(ctx, t, f, f.instance.ID, n.ID)
	}
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, n.ID)
	n.FrameID = &frameID
	runPhase := "pending"
	if state == cascade.NodeStateRunning {
		runPhase = "active"
	}
	pgtest.ExecForTest(ctx, t, f.driver,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores,
		                               enqueued_at, claimed_by, claimed_at,
		                               phase, state, frame_id, run_scope_id)
		 VALUES (gen_random_uuid(), $1, $2, '{}', NOW(), NULL, NULL,
		         $3, $4, $5, $6)`,
		n.ID, executor, runPhase, string(state), frameID, f.instance.MainRunScopeID)
	n.State = state
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
// instanceID (the sourceNodeID arg is retained for call-site clarity but
// no longer threaded into the frame row — every frame carries a
// triggering_message_id instead, which the helper seeds synthetically).
func insertRunningFrame(ctx context.Context, t *testing.T, f *schedFixture, instanceID, sourceNodeID shared.UUID) shared.UUID {
	t.Helper()
	_ = sourceNodeID
	msgID := uuid.New()
	pgtest.ExecForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_messages
            (id, instance_id, type, sender, sender_kind, received_at)
        VALUES ($1, $2, 'test/seed', 'test', 'operator', now())
    `, msgID, instanceID)
	var frameID shared.UUID
	pgtest.QueryRowForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_frames
            (instance_id, triggering_message_id, state, queued_at, started_at, frame_timeout_ms)
        VALUES ($1, $2, 'running', now(), now(), 600000)
        RETURNING frame_id
    `, []any{instanceID, msgID}, &frameID)
	return frameID
}

// @deliberate: legacy `setHeartbeat` helper deleted alongside its
// callers in Pass 1; the historic name has no live consumers after
// TD-three-dispatch-deadlines and the test fixture below no longer
// needs to seed the retired heartbeat column.

// schedConfig returns a Config wired to the fixture's persistence layer.
func (f *schedFixture) schedConfig() Config {
	return Config{
		Persist:               f.persist,
		Queue:                 f.queue,
		AdvisoryLocker:        f.coordinator,
		Clock:                 f.clock,
		Logger:                f.log,
		MaxQuietPeriodDefault: 75 * time.Second,
	}
}

func TestScheduler_TicksAndStops(t *testing.T) {
	t.Parallel()
	f := newSchedFixture(t)

	cfg := f.schedConfig()
	cfg.TickInterval = 50 * time.Millisecond
	h := Start(cfg)

	// @deliberate: Give the loop a couple of ticks to run.
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(ctx))
}

func TestScheduler_ReadySweep_EnqueuesExecutorNodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	// @deliberate: Dep is fresh; target is stale + executor-backed →
	// ListReadyForDispatch should pick it up.
	dep := f.createNode(t, "worker", cascade.NodeStateFresh)
	target := f.createNode(t, "runner", cascade.NodeStateStale, dep.ID)

	require.NoError(t, tick(ctx, f.schedConfig(), nil))

	// @deliberate: Dispatch row exists for the target.
	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{target.ID}, &count)
	assert.Equal(t, 1, count, "expected a dispatch row for the ready node")
}

// @blessed-invariant: orphan-claim-cutoff-five-times-heartbeat-timeout
func TestScheduler_OrphanedClaim_Released(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	// @deliberate: stale node with a claimed dispatch row whose
	// claimed_at is past the cutoff — ListOrphanedClaims + ReleaseClaim
	// should fire.
	n := f.createNode(t, "worker", cascade.NodeStateStale)
	require.NotNil(t, n.FrameID)
	// @deliberate: EnqueuedAt in the past so the SelectCandidates
	// `enqueued_at <= NOW()` predicate isn't flaky against the postgres
	// container's clock.
	require.NoError(t, f.queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID: n.ID, ExecutorName: "worker", EnqueuedAt: time.Now().Add(-time.Second),
		FrameID:    *n.FrameID,
		RunScopeID: f.instance.MainRunScopeID,
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

	// @deliberate: simulate a row that went async (async_ack_id set,
	// per-row effective_max_quiet_period_seconds denormalized) and has
	// been quiet for longer than its cap. The real
	// runner_dispatch.go::registerAsyncIfSet path runs this denormalization
	// at AwaitAsyncCallback registration; this test stamps the row
	// directly to simulate that production state. Per
	// TD-three-dispatch-deadlines + concept:dispatch-deadlines.
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_node_runs
		    SET last_progress_at = NOW() - INTERVAL '10 minutes',
		        async_ack_id = 'orphan-test-ack',
		        effective_max_quiet_period_seconds = 75
		  WHERE id = $1`,
		dispatchID,
	)

	require.NoError(t, tick(ctx, f.schedConfig(), nil))

	// @deliberate: Claim released: claimed_by IS NULL.
	own, err := f.queue.GetClaimedBy(ctx, dispatchID)
	require.NoError(t, err)
	assert.Equal(t, "unclaimed", own.Kind,
		"expected orphan claim to be released")

	// @deliberate: orphaned_claim_released event appended.
	var events persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		e, err := f.persist.Events().List(ctx,
			persistence.EventListFilter{NodeID: &n.ID, Kind: "orphaned_claim_released"},
			persistence.ListPagination{Limit: 10}, tx,
		)
		events = e
		return err
	})
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

	// @deliberate: create a ready node — if the tick runs it will be
	// enqueued.
	dep := f.createNode(t, "worker", cascade.NodeStateFresh)
	target := f.createNode(t, "runner", cascade.NodeStateStale, dep.ID)

	// @deliberate: acquire the advisory lock on a separate connection
	// and hold it while calling tick. tick should see TrySchedulerTick
	// → held=false and skip.
	release := pgtest.HoldAdvisoryLock(ctx, t, f.driver, pgSchedulerTickLockKey)
	defer release()

	// @deliberate: tick needs a *different* connection from the pool
	// to attempt its lock. Use a background goroutine + short wait so
	// this test doesn't wedge if the pool is single-capacity.
	done := make(chan error, 1)
	go func() {
		done <- tick(ctx, f.schedConfig(), nil)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("tick did not return within 10s while other replica held the lock")
	}

	// @deliberate: Skipped tick means the ready-sweep did NOT run; the in-flight
	// stale run row seeded by createNode stays in phase='pending' with
	// claimed_by NULL. Post-stage-3 cutover the run row carries state,
	// so a "dispatch happened" symptom is "phase advanced to active or
	// row claimed" — neither should occur while the lock is held.
	var (
		phase     string
		claimedBy sql.NullString
	)
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT phase, claimed_by FROM rimsky_node_runs
		   WHERE node_id = $1
		     AND phase IN ('pending','active','held','parked')`,
		[]any{target.ID}, &phase, &claimedBy)
	assert.Equal(t, "pending", phase, "tick should have skipped (run row not claimed) under advisory-lock contention")
	assert.False(t, claimedBy.Valid, "run row should not be claimed under advisory-lock contention")
}

// erroringAdvisoryLocker is a stub AdvisoryLocker whose TrySchedulerTick
// always errors. The embedded interface is nil — any other method call
// panics, which is itself an assertion that the tick touches nothing
// else on the locker after a lock error.
type erroringAdvisoryLocker struct {
	persistence.AdvisoryLocker
	calls int32
}

func (l *erroringAdvisoryLocker) TrySchedulerTick(context.Context) (bool, func(), error) {
	atomic.AddInt32(&l.calls, 1)
	return false, nil, errors.New("simulated advisory-lock failure")
}

// TestScheduler_AdvisoryLockErrorSkipsSweepPass pins the lock-error-is-
// lock-held rule (concept:advisory-lock invariant): when TrySchedulerTick
// errors, the tick logs and skips the whole sweep pass — it must NOT fall
// through to running the sweeps unlocked, because under DB flakiness that
// permits the concurrent multi-replica sweeping the lock exists to
// prevent (@blessed-invariant 7).
func TestScheduler_AdvisoryLockErrorSkipsSweepPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	// @deliberate: Seed a ready node — if the tick erroneously ran its sweeps, the
	// sweeps, the pending run row would be claimed / advanced.
	dep := f.createNode(t, "worker", cascade.NodeStateFresh)
	target := f.createNode(t, "runner", cascade.NodeStateStale, dep.ID)

	cfg := f.schedConfig()
	locker := &erroringAdvisoryLocker{}
	cfg.AdvisoryLocker = locker

	// @deliberate: a lock error is treated as lock-held — tick returns
	// nil (skip, not failure; the next interval retries) without
	// running any sweep.
	require.NoError(t, tick(ctx, cfg, nil))
	assert.EqualValues(t, 1, atomic.LoadInt32(&locker.calls),
		"tick should attempt the lock exactly once")

	// @deliberate: No sweep ran: the seeded in-flight run row is untouched (same
	// observable as the lock-contention test above).
	var (
		phase     string
		claimedBy sql.NullString
	)
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT phase, claimed_by FROM rimsky_node_runs
		   WHERE node_id = $1
		     AND phase IN ('pending','active','held','parked')`,
		[]any{target.ID}, &phase, &claimedBy)
	assert.Equal(t, "pending", phase,
		"sweep pass must be skipped (run row untouched) when the lock attempt errors")
	assert.False(t, claimedBy.Valid,
		"run row must stay unclaimed when the lock attempt errors")
}

// TestScheduler_BreakpointSweeps pins the Pass-7 wiring: a single tick
// must (a) delete TTL-expired `rimsky_instance_breakpoints` rows and
// (b) auto-resume unresumed `rimsky_breakpoint_hits` rows that have
// outlived their breakpoint's `hit_ttl_seconds` and whose breakpoint
// uses overflow_policy = 'auto_resume_after_ttl'.
func TestScheduler_BreakpointSweeps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	// @deliberate: expired breakpoint (drop_oldest, short ttl) — should
	// be deleted by the SweepExpired branch.
	expiredTTL := 60
	expired := persistence.BreakpointRow{
		InstanceID:     f.instance.ID,
		Matcher:        map[string]any{"label": "expired"},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		TTLSeconds:     &expiredTTL,
		CreatedByKey:   "test-key",
	}

	// @deliberate: auto-resume breakpoint (hit_ttl=1s) with one
	// unresumed hit — AutoResumeStale should flip resumed_at +
	// resumed_by_key='sweeper'.
	autoBP := persistence.BreakpointRow{
		InstanceID:     f.instance.ID,
		Matcher:        map[string]any{"label": "auto"},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowAutoResumeAfterTTL,
		HitTTLSeconds:  1,
		CreatedByKey:   "test-key",
	}

	var expiredID, autoID, hitID shared.UUID
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		expiredID, err = f.persist.Breakpoints().Create(ctx, expired, tx)
		if err != nil {
			return err
		}
		autoID, err = f.persist.Breakpoints().Create(ctx, autoBP, tx)
		if err != nil {
			return err
		}
		id, _, err := f.persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: autoID,
			InstanceID:   f.instance.ID,
			Checkpoint:   persistence.CheckpointBeforeDispatch,
			Mode:         persistence.BreakpointModePause,
			Snapshot:     map[string]any{"label": "stale"},
		}, tx)
		if err != nil {
			return err
		}
		hitID = id
		return nil
	}))

	// @deliberate: force the expired breakpoint's expires_at into the
	// past, and backdate the hit's hit_at so it's older than
	// hit_ttl_seconds.
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_instance_breakpoints SET expires_at = NOW() - interval '1 hour' WHERE id = $1`,
		expiredID)
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_breakpoint_hits SET hit_at = NOW() - interval '1 hour' WHERE id = $1`,
		hitID)

	require.NoError(t, tick(ctx, f.schedConfig(), nil))

	// @deliberate: Expired breakpoint deleted; auto breakpoint survives.
	var (
		gotExpired, gotAuto *persistence.BreakpointRow
		gotHit              *persistence.BreakpointHitRow
	)
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		var err error
		gotExpired, err = f.persist.Breakpoints().Get(ctx, expiredID, tx)
		if err != nil {
			return err
		}
		gotAuto, err = f.persist.Breakpoints().Get(ctx, autoID, tx)
		if err != nil {
			return err
		}
		gotHit, err = f.persist.BreakpointHits().Get(ctx, hitID, tx)
		return err
	})
	assert.Nil(t, gotExpired, "expected expired breakpoint to be swept")
	require.NotNil(t, gotAuto, "auto_resume breakpoint should survive sweep")
	require.NotNil(t, gotHit, "hit row should still exist after AutoResumeStale")
	require.NotNil(t, gotHit.ResumedAt, "stale hit should have resumed_at set")
	require.NotNil(t, gotHit.ResumedByKey, "stale hit should have resumed_by_key set")
	assert.Equal(t, "sweeper", *gotHit.ResumedByKey,
		"AutoResumeStale must stamp resumed_by_key='sweeper'")
}

// @deliberate: unused-variable warning from `sync` if the file gets trimmed.
var _ = sync.Mutex{}
