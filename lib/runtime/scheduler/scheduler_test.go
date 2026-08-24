// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

const pgSchedulerTickLockKey int64 = 4853127298010834892

type schedFixture struct {
	persist     persistence.Tables
	queue       persistence.Queue
	coordinator persistence.AdvisoryLocker
	driver      persistence.Database
	clock       shared.Clock
	log         shared.Logger
	instance    persistence.InstanceRow
	mainScopeID shared.UUID
}

func newSchedFixture(t *testing.T) *schedFixture {
	t.Helper()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

	tpl := insertDeployedTemplate(ctx, t, d.Tables(), nodepkg.TemplateSpec{
		Name: "sched-loop-" + uuid.NewString(), Version: "v1",
		Nodes: []nodepkg.TemplateNodeDef{},
	})
	ck := "ck-" + uuid.NewString()
	var inst persistence.InstanceRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	inTxTest(t, ctx, d.Tables(), func(tx persistence.Tx) error {
		if err := d.Tables().RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		row, err := d.Tables().Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
			ID: instID, TemplateHash: tpl.ID, InstanceKey: &ck,
			Params: map[string]any{},
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
		mainScopeID: mainScopeID,
	}
}

func (f *schedFixture) createNode(t *testing.T, executor string, state cascade.NodeState, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	ctx := context.Background()
	var n persistence.NodeRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
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
	if state == cascade.NodeStateFresh {
		return n
	}
	frameID, ok := lookupRunningFrame(ctx, t, f, f.instance.ID)
	if !ok {
		frameID = insertRunningFrame(ctx, t, f, f.instance.ID, n.ID)
	}
	pgdbtest.ExecForTest(ctx, t, f.driver,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_claim_producers,
		                               enqueued_at, claimed_by, claimed_at,
		                               state, sequence, creation_reason, frame_id, run_scope_id)
		 VALUES (gen_random_uuid(), $1, $2, '{}', NOW(), NULL, NULL,
		         $3, 1, 'cascade', $4, $5)`,
		n.ID, executor, string(state), frameID, f.mainScopeID)
	return n
}

func lookupRunningFrame(ctx context.Context, t *testing.T, f *schedFixture, instanceID shared.UUID) (shared.UUID, bool) {
	t.Helper()
	var count int
	pgdbtest.QueryRowForTest(ctx, t, f.driver, `
        SELECT COUNT(*) FROM rimsky_frames
        WHERE instance_id = $1 AND ended_at IS NULL
    `, []any{instanceID}, &count)
	if count == 0 {
		return shared.UUID{}, false
	}
	var got shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver, `
        SELECT frame_id FROM rimsky_frames
        WHERE instance_id = $1 AND ended_at IS NULL
        ORDER BY started_at DESC LIMIT 1
    `, []any{instanceID}, &got)
	return got, true
}

func insertRunningFrame(ctx context.Context, t *testing.T, f *schedFixture, instanceID, sourceNodeID shared.UUID) shared.UUID {
	t.Helper()
	_ = sourceNodeID
	msgID := uuid.New()
	pgdbtest.ExecForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_messages
            (id, instance_id, type, sender, sender_kind, received_at)
        VALUES ($1, $2, 'test/seed', 'test', 'operator', now())
    `, msgID, instanceID)
	var frameID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver, `
        INSERT INTO rimsky_frames
            (instance_id, triggering_message_id, root_run_scope_id, started_at)
        VALUES ($1, $2, $3, now())
        RETURNING frame_id
    `, []any{instanceID, msgID, f.mainScopeID}, &frameID)
	return frameID
}

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

	awaited.Until(t, "the scheduler to complete three ticks before the shutdown",
		func() bool { return h.TicksCompleted() >= 3 })

	require.NoError(t, h.Shutdown(context.Background()))
}

func TestScheduler_ConcurrentShutdownDoesNotPanic(t *testing.T) {
	t.Parallel()
	f := newSchedFixture(t)

	cfg := f.schedConfig()
	cfg.TickInterval = 50 * time.Millisecond
	h := Start(cfg)

	const shutdownCallers = 8
	var wg sync.WaitGroup
	errs := make([]error, shutdownCallers)
	for i := 0; i < shutdownCallers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = h.Shutdown(context.Background())
		}()
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "concurrent Shutdown call %d must not error", i)
	}
}

func TestScheduler_Tick_SweepsPureCascadeReadyButSkipsExecutorNodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	pure := f.createNode(t, "", cascade.NodeStateStale)
	execNode := f.createNode(t, "runner", cascade.NodeStateStale)

	require.NoError(t, tick(ctx, f.schedConfig()))

	var pureState string
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT state FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{pure.ID}, &pureState)
	assert.Equal(t, "fresh", pureState,
		"tick must sweep an executorless pure-cascade-ready node to fresh")

	var (
		execState string
		claimedBy sql.NullString
	)
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT state, claimed_by FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{execNode.ID}, &execState, &claimedBy)
	assert.Equal(t, "stale", execState,
		"tick's pure-cascade sweep must not touch an executor node's dispatch row")
	assert.False(t, claimedBy.Valid, "executor node run must remain unclaimed")
}

func TestScheduler_Tick_NilClockDoesNotPanic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	cfg := f.schedConfig()
	cfg.Clock = nil

	require.NotPanics(t, func() {
		require.NoError(t, tick(ctx, cfg))
	}, "tick must not panic when Config.Clock is nil")
}

func TestScheduler_Start_DefaultsNilClockSoTheClockGatedSweepStillRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	frameID := insertRunningFrame(ctx, t, f, f.instance.ID, shared.UUID{})
	var triggeringMessageID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT triggering_message_id FROM rimsky_frames WHERE frame_id = $1`,
		[]any{frameID}, &triggeringMessageID)

	cfg := f.schedConfig()
	cfg.Clock = nil
	cfg.TickInterval = 10 * time.Millisecond

	h := Start(cfg)
	defer func() {
		require.NoError(t, h.Shutdown(context.Background()))
	}()

	awaited.Until(t, "the triggering-message delivery the tick performs only when a clock is configured",
		func() bool {
			var deliveredAt sql.NullTime
			pgdbtest.QueryRowForTest(ctx, t, f.driver,
				`SELECT delivered_at FROM rimsky_messages WHERE id = $1`,
				[]any{triggeringMessageID}, &deliveredAt)
			return deliveredAt.Valid
		})
}

func TestScheduler_OrphanedClaim_Released(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	n := f.createNode(t, "worker", cascade.NodeStateStale)
	frameID, ok := lookupRunningFrame(ctx, t, f, f.instance.ID)
	require.True(t, ok, "instance must have a running frame after createNode")
	require.NoError(t, f.queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID: n.ID, ExecutorName: "worker", EnqueuedAt: time.Now().Add(-time.Second),
		FrameID:    frameID,
		RunScopeID: f.mainScopeID,
	}, nil))

	var nodeRunID shared.UUID
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		candidates, err := f.queue.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 1,
		}, tx)
		if err != nil {
			return err
		}
		require.Len(t, candidates, 1)
		nodeRunID = candidates[0].NodeRunID
		ok, err := f.queue.ClaimDispatchRow(ctx, nodeRunID, "sup-dead", tx)
		if err != nil {
			return err
		}
		require.True(t, ok)
		return nil
	}))

	pgdbtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_node_runs
		    SET last_progress_at = NOW() - INTERVAL '10 minutes',
		        async_ack_id = 'orphan-test-ack',
		        async_callback_url = 'http://unreachable-advertise-host:9099',
		        effective_max_quiet_period_seconds = 75
		  WHERE id = $1`,
		nodeRunID,
	)

	require.NoError(t, tick(ctx, f.schedConfig()))

	own, err := f.queue.GetClaimedBy(ctx, nodeRunID)
	require.NoError(t, err)
	assert.Equal(t, "unclaimed", own.Kind,
		"expected orphan claim to be released")

	var events persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		e, err := f.persist.Events().List(ctx,
			persistence.EventListFilter{NodeID: &n.ID, KindIn: []string{"orphaned_claim_released"}},
			persistence.ListPagination{Limit: 10}, tx,
		)
		events = e
		return err
	})
	require.NotEmpty(t, events.Events, "expected an orphaned_claim_released event")
	require.Equal(t, "http://unreachable-advertise-host:9099", events.Events[0].Payload.Map()["callback_url"],
		"the reap diagnostics must name the callback URL nobody called, so a misconfigured advertise host names its own cause")
}

func TestScheduler_AdvisoryLockBlocksSecondReplica(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	dep := f.createNode(t, "worker", cascade.NodeStateFresh)
	target := f.createNode(t, "runner", cascade.NodeStateStale, dep.ID)

	release := pgdbtest.HoldAdvisoryLock(ctx, t, f.driver, pgSchedulerTickLockKey)
	defer release()

	done := make(chan error, 1)
	go func() {
		done <- tick(ctx, f.schedConfig())
	}()

	require.NoError(t, <-done)

	var (
		runState  string
		claimedBy sql.NullString
	)
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT state, claimed_by FROM rimsky_node_runs
		   WHERE node_id = $1
		     AND state IN ('pending','stale','running','held','parked')`,
		[]any{target.ID}, &runState, &claimedBy)
	assert.Equal(t, "stale", runState, "tick should have skipped (run row not claimed) under advisory-lock contention")
	assert.False(t, claimedBy.Valid, "run row should not be claimed under advisory-lock contention")
}

type erroringAdvisoryLocker struct {
	persistence.AdvisoryLocker
	calls int32
}

func (l *erroringAdvisoryLocker) TrySchedulerTick(context.Context) (bool, func(), error) {
	atomic.AddInt32(&l.calls, 1)
	return false, nil, errors.New("simulated advisory-lock failure")
}

func TestScheduler_AdvisoryLockErrorSkipsSweepPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	dep := f.createNode(t, "worker", cascade.NodeStateFresh)
	target := f.createNode(t, "runner", cascade.NodeStateStale, dep.ID)

	cfg := f.schedConfig()
	locker := &erroringAdvisoryLocker{}
	cfg.AdvisoryLocker = locker

	require.NoError(t, tick(ctx, cfg))
	assert.EqualValues(t, 1, atomic.LoadInt32(&locker.calls),
		"tick should attempt the lock exactly once")

	var (
		runState  string
		claimedBy sql.NullString
	)
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT state, claimed_by FROM rimsky_node_runs
		   WHERE node_id = $1
		     AND state IN ('pending','stale','running','held','parked')`,
		[]any{target.ID}, &runState, &claimedBy)
	assert.Equal(t, "stale", runState,
		"sweep pass must be skipped (run row untouched) when the lock attempt errors")
	assert.False(t, claimedBy.Valid,
		"run row must stay unclaimed when the lock attempt errors")
}

func TestScheduler_BreakpointSweeps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

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

	pgdbtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_instance_breakpoints SET expires_at = NOW() - interval '1 hour' WHERE id = $1`,
		expiredID)
	pgdbtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_breakpoint_hits SET hit_at = NOW() - interval '1 hour' WHERE id = $1`,
		hitID)

	require.NoError(t, tick(ctx, f.schedConfig()))

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

func TestScheduler_OrphanedBreakpointHitReap_ExcludesNotifyOnlyAndLiveBlockedRunner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	bp := persistence.BreakpointRow{
		InstanceID:     f.instance.ID,
		Matcher:        map[string]any{"label": "block"},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowBlockDispatch,
		HitTTLSeconds:  300,
		CreatedByKey:   "test-key",
	}
	var bpID shared.UUID
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		bpID, err = f.persist.Breakpoints().Create(ctx, bp, tx)
		return err
	}))

	liveNode := f.createNode(t, "stub", cascade.NodeStateRunning)
	var liveRunID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1`, []any{liveNode.ID}, &liveRunID)
	pgdbtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_node_runs SET claimed_by = 'supervisor-live', claimed_at = NOW() WHERE id = $1`,
		liveRunID)

	deadNode := f.createNode(t, "stub", cascade.NodeStateRunning)
	var deadRunID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1`, []any{deadNode.ID}, &deadRunID)

	var notifyOnlyHitID, liveBlockedHitID, orphanedHitID shared.UUID
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, _, err := f.persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: bpID, InstanceID: f.instance.ID,
			Checkpoint: persistence.CheckpointBeforeDispatch,
			Mode:       persistence.BreakpointModeNotifyOnly,
			Snapshot:   map[string]any{"label": "notify-only"},
		}, tx)
		if err != nil {
			return err
		}
		notifyOnlyHitID = id

		id, _, err = f.persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: bpID, InstanceID: f.instance.ID, NodeRunID: &liveRunID,
			Checkpoint: persistence.CheckpointBeforeDispatch,
			Mode:       persistence.BreakpointModePause,
			Snapshot:   map[string]any{"label": "live-blocked"},
		}, tx)
		if err != nil {
			return err
		}
		liveBlockedHitID = id

		id, _, err = f.persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: bpID, InstanceID: f.instance.ID, NodeRunID: &deadRunID,
			Checkpoint: persistence.CheckpointBeforeDispatch,
			Mode:       persistence.BreakpointModePause,
			Snapshot:   map[string]any{"label": "orphaned"},
		}, tx)
		if err != nil {
			return err
		}
		orphanedHitID = id
		return nil
	}))

	for _, id := range []shared.UUID{notifyOnlyHitID, liveBlockedHitID, orphanedHitID} {
		pgdbtest.ExecForTest(ctx, t, f.driver,
			`UPDATE rimsky_breakpoint_hits SET hit_at = NOW() - interval '1 hour' WHERE id = $1`, id)
	}

	require.NoError(t, tick(ctx, f.schedConfig()))

	var gotNotifyOnly, gotLiveBlocked, gotOrphaned *persistence.BreakpointHitRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		var err error
		gotNotifyOnly, err = f.persist.BreakpointHits().Get(ctx, notifyOnlyHitID, tx)
		if err != nil {
			return err
		}
		gotLiveBlocked, err = f.persist.BreakpointHits().Get(ctx, liveBlockedHitID, tx)
		if err != nil {
			return err
		}
		gotOrphaned, err = f.persist.BreakpointHits().Get(ctx, orphanedHitID, tx)
		return err
	})
	require.NotNil(t, gotNotifyOnly, "a notify_only hit must never be reaped by the orphaned-hit sweeper")
	require.NotNil(t, gotLiveBlocked, "a pause hit whose node run is still claimed (a live blocked runner) must not be force-resumed by the orphan reap")
	require.Nil(t, gotOrphaned, "a pause hit whose node run is no longer claimed (truly orphaned) must still be reaped")
}
