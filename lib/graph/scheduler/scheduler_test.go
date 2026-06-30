// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, n.ID)
	n.FrameID = &frameID
	pgtest.ExecForTest(ctx, t, f.driver,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores,
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
            (instance_id, triggering_message_id, root_run_scope_id, state, queued_at, started_at, frame_timeout_ms)
        VALUES ($1, $2, $3, 'running', now(), now(), 600000)
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

	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(ctx))
}

func TestScheduler_ReadySweep_EnqueuesExecutorNodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	dep := f.createNode(t, "worker", cascade.NodeStateFresh)
	target := f.createNode(t, "runner", cascade.NodeStateStale, dep.ID)

	require.NoError(t, tick(ctx, f.schedConfig(), nil))

	var count int
	pgtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{target.ID}, &count)
	assert.Equal(t, 1, count, "expected a dispatch row for the ready node")
}

func TestScheduler_OrphanedClaim_Released(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	n := f.createNode(t, "worker", cascade.NodeStateStale)
	require.NotNil(t, n.FrameID)
	require.NoError(t, f.queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID: n.ID, ExecutorName: "worker", EnqueuedAt: time.Now().Add(-time.Second),
		FrameID:    *n.FrameID,
		RunScopeID: f.mainScopeID,
	}))

	var dispatchID shared.UUID
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		candidates, err := f.queue.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"worker"},
			AcceptedClaimProducers: []string{},
			Limit:                  1,
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

	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_node_runs
		    SET last_progress_at = NOW() - INTERVAL '10 minutes',
		        async_ack_id = 'orphan-test-ack',
		        effective_max_quiet_period_seconds = 75
		  WHERE id = $1`,
		dispatchID,
	)

	require.NoError(t, tick(ctx, f.schedConfig(), nil))

	own, err := f.queue.GetClaimedBy(ctx, dispatchID)
	require.NoError(t, err)
	assert.Equal(t, "unclaimed", own.Kind,
		"expected orphan claim to be released")

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

func TestScheduler_AdvisoryLockBlocksSecondReplica(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSchedFixture(t)

	dep := f.createNode(t, "worker", cascade.NodeStateFresh)
	target := f.createNode(t, "runner", cascade.NodeStateStale, dep.ID)

	release := pgtest.HoldAdvisoryLock(ctx, t, f.driver, pgSchedulerTickLockKey)
	defer release()

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

	var (
		runState  string
		claimedBy sql.NullString
	)
	pgtest.QueryRowForTest(ctx, t, f.driver,
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

	require.NoError(t, tick(ctx, cfg, nil))
	assert.EqualValues(t, 1, atomic.LoadInt32(&locker.calls),
		"tick should attempt the lock exactly once")

	var (
		runState  string
		claimedBy sql.NullString
	)
	pgtest.QueryRowForTest(ctx, t, f.driver,
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

	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_instance_breakpoints SET expires_at = NOW() - interval '1 hour' WHERE id = $1`,
		expiredID)
	pgtest.ExecForTest(ctx, t, f.driver,
		`UPDATE rimsky_breakpoint_hits SET hit_at = NOW() - interval '1 hour' WHERE id = $1`,
		hitID)

	require.NoError(t, tick(ctx, f.schedConfig(), nil))

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

var _ = sync.Mutex{}
