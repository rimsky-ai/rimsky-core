// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func seedTemplateInstanceAndMessage(t *testing.T, ctx context.Context, d persistence.Database) (uuid.UUID, uuid.UUID) {
	t.Helper()
	suffix := uuid.NewString()
	suffix = strings.ReplaceAll(suffix, "-", "")
	suffix = (suffix + suffix)[:64]
	templateHash := "sha256-" + suffix
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_templates (id, spec, state)
        VALUES ($1, $2::jsonb, 'deployed')
    `, templateHash, `{}`)

	instanceID := uuid.New()
	mainScopeID := uuid.New()
	messageID := uuid.New()
	tables := d.Tables()
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		ck := "ck-" + instanceID.String()[:8]
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           instanceID,
			TemplateHash: templateHash,
			InstanceKey:  &ck,
		}, tx); err != nil {
			return err
		}
		return tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         foundationshared.UUID(messageID),
			InstanceID: foundationshared.UUID(instanceID),
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
			ReceivedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("seed template+instance+message: %v", err)
	}
	return instanceID, messageID
}

func runTickAgainstDriver(ctx context.Context, d persistence.Database, log frame.Logger) error {
	return frame.RunTick(ctx, d.Tables(), d.Queue(), log, nil, nil)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeMetricsHook struct {
	observed []float64
}

func (f *fakeMetricsHook) ObserveFrameDuration(seconds float64) {
	f.observed = append(f.observed, seconds)
}

func seedNode(t *testing.T, ctx context.Context, d persistence.Database,
	instanceID uuid.UUID, nodeID uuid.UUID, state string, frameID *uuid.UUID) {
	t.Helper()
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_nodes
            (id, instance_id, node_type)
        VALUES ($1, $2, 'n')
    `, nodeID, instanceID)
	if state == "fresh" || state == "" {
		return
	}
	if frameID == nil {
		t.Fatalf("seedNode: state=%q requires a non-nil frame_id (rimsky_node_runs.frame_id NOT NULL)", state)
	}
	seedNodeRun(t, ctx, d, instanceID, nodeID, state, *frameID)
}

func seedNodeRun(t *testing.T, ctx context.Context, d persistence.Database,
	instanceID uuid.UUID, nodeID uuid.UUID, state string, frameID uuid.UUID, claimedBy ...string) {
	t.Helper()
	var claimedByPtr interface{}
	if len(claimedBy) > 0 && claimedBy[0] != "" {
		claimedByPtr = claimedBy[0]
	}
	var mainScopeID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT id FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'main'`,
		[]any{instanceID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, claimed_by, frame_id, run_scope_id)
        VALUES (gen_random_uuid(), $1, NULL, ARRAY[]::text[], NOW(), $2, 1, 'cascade', $3, $4, $5)
    `, nodeID, state, claimedByPtr, frameID, mainScopeID)
}

func seedFrameRow(t *testing.T, ctx context.Context, d persistence.Database,
	instanceID uuid.UUID, triggeringMessageID uuid.UUID, state string,
	startedAt *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var endedAt *time.Time
	if state == "completed" || state == "failed" {
		end := time.Now()
		endedAt = &end
	}
	progressAt := time.Now()
	if startedAt != nil {
		progressAt = *startedAt
	}
	var rootScope uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT id FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'main'`,
		[]any{instanceID}, &rootScope)
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, triggering_message_id, root_run_scope_id, started_at, ended_at, last_progress_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `, id, instanceID, triggeringMessageID, rootScope, startedAt, endedAt, progressAt)
	return id
}

func markMessageDelivered(t *testing.T, ctx context.Context, d persistence.Database, messageID uuid.UUID) {
	t.Helper()
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_messages SET delivered_at = now() WHERE id = $1`, messageID)
}

func TestRunTick_FrameEndDetection_AllFresh_Completed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &now)
	seedNode(t, ctx, d, instanceID, src, "", nil)
	seedNodeRun(t, ctx, d, instanceID, src, "fresh", frameID)
	markMessageDelivered(t, ctx, d, msgID)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var runCount int
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COUNT(*) FROM rimsky_node_runs WHERE frame_id = $1`, []any{frameID}, &runCount)
	require.Equal(t, 1, runCount,
		"this test proves a held terminal-fresh run settles the frame, not the vacuous zero-run case")

	var state string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT
		    CASE
		        WHEN f.ended_at IS NULL THEN 'running'
		        WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed'
		        ELSE 'completed'
		    END
		   FROM rimsky_frames f WHERE frame_id = $1`, []any{frameID}, &state)
	require.Equal(t, "completed", state)
}

func TestRunTick_FrameEndDetection_OneFailed_Failed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &now)
	seedNode(t, ctx, d, instanceID, src, "failed", &frameID)
	markMessageDelivered(t, ctx, d, msgID)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var state string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT
		    CASE
		        WHEN f.ended_at IS NULL THEN 'running'
		        WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed'
		        ELSE 'completed'
		    END
		   FROM rimsky_frames f WHERE frame_id = $1`, []any{frameID}, &state)
	require.Equal(t, "failed", state)
}

func TestRunTick_FrameEndDetection_ObservesDBStampedDuration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	startedAt := time.Now().Add(-5 * time.Second)
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &startedAt)
	seedNode(t, ctx, d, instanceID, src, "fresh", &frameID)
	markMessageDelivered(t, ctx, d, msgID)

	metrics := &fakeMetricsHook{}
	require.NoError(t, frame.RunTick(ctx, d.Tables(), d.Queue(), quietLogger(), nil, metrics))

	require.Len(t, metrics.observed, 1, "exactly one frame-duration observation expected")

	var dbStartedAt, dbEndedAt time.Time
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT started_at, ended_at FROM rimsky_frames WHERE frame_id = $1`, []any{frameID},
		&dbStartedAt, &dbEndedAt)
	require.False(t, dbEndedAt.IsZero(), "frame must have been ended")

	wantSeconds := dbEndedAt.Sub(dbStartedAt).Seconds()
	require.InDelta(t, wantSeconds, metrics.observed[0], 0.001,
		"observed duration must equal the DB-stamped ended_at minus started_at, not an independently-read Go wall clock")
	require.Greater(t, metrics.observed[0], 4.0,
		"duration must reflect the ~5s gap between seeded started_at and the DB-stamped ended_at")
}

// @concept: run-scope
func TestRunTick_FrameEndDetection_ClosesRootScopeTreeAtSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &now)
	seedNode(t, ctx, d, instanceID, src, "fresh", &frameID)

	var rootScope uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT root_run_scope_id FROM rimsky_frames WHERE frame_id = $1`,
		[]any{frameID}, &rootScope)
	parentRunID := uuid.New()
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
        VALUES ($1, $2, NULL, ARRAY[]::text[], NOW(), 'fresh', 2, 'cascade', $3, $4)
    `, parentRunID, src, frameID, rootScope)
	childScope := uuid.New()
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_run_scopes (id, parent_run_scope_id, parent_run_id, graph_name, instance_id, partition_key)
        VALUES ($1, $2, $3, 'sub-flow', $4, '')
    `, childScope, rootScope, parentRunID, instanceID)
	markMessageDelivered(t, ctx, d, msgID)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	for _, scope := range []uuid.UUID{childScope, rootScope} {
		var closed *time.Time
		pgtest.QueryRowForTest(ctx, t, d,
			`SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`,
			[]any{scope}, &closed)
		require.NotNil(t, closed,
			"frame settlement must close every open scope in the frame's tree (scope %s), not defer to instance teardown", scope)
	}
}

func TestRunTick_FrameEndDetection_UndeliveredTriggerNotCompleted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &now)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var endedAtNull bool
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT ended_at IS NULL FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &endedAtNull)
	require.True(t, endedAtNull,
		"a just-opened frame whose triggering message has not yet been delivered has zero node runs "+
			"by construction — end-detection must not mistake that for settlement and phantom-complete it")
}

func TestRunTick_OpenNewFrames_PicksOldestPendingMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	srcA := uuid.New()
	srcB := uuid.New()
	seedNode(t, ctx, d, instanceID, srcA, "fresh", nil)
	seedNode(t, ctx, d, instanceID, srcB, "fresh", nil)

	var preExistingMainScope uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT id FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'main'`,
		[]any{instanceID}, &preExistingMainScope)

	msg2ID := foundationshared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
		INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind, received_at)
		VALUES ($1, $2, 'test/seed', 'test', 'operator', now() + interval '1 second')`,
		msg2ID, instanceID)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var running int
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
		[]any{instanceID}, &running)
	require.Equal(t, 1, running, "at most one running frame per instance")

	var openedTriggeringID string
	var openedRootScope uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT triggering_message_id::text, root_run_scope_id FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
		[]any{instanceID}, &openedTriggeringID, &openedRootScope)
	require.Equal(t, msgID.String(), openedTriggeringID,
		"oldest pending message opens the running frame")
	require.NotEqual(t, uuid.Nil, openedRootScope, "opening a frame must stamp a real root_run_scope_id")
	require.NotEqual(t, preExistingMainScope, openedRootScope,
		"opening a frame must create a fresh root run scope, not reuse the pre-existing 'main' scope")

	var scopeCount int
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COUNT(*) FROM rimsky_run_scopes WHERE id = $1 AND instance_id = $2 AND graph_name = 'main'`,
		[]any{openedRootScope, instanceID}, &scopeCount)
	require.Equal(t, 1, scopeCount, "the frame's root_run_scope_id must resolve to a real, distinct run_scope row")
}

func TestRunTick_ReapOrphanDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "completed", &now)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_frames SET ended_at = now() WHERE frame_id = $1`, frameID)

	seedNode(t, ctx, d, instanceID, src, "fresh", nil)
	seedNodeRun(t, ctx, d, instanceID, src, "running", frameID, "supervisor-1")

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var claimedBy *string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT claimed_by FROM rimsky_node_runs WHERE node_id = $1`, []any{src}, &claimedBy)
	require.Nil(t, claimedBy)
}

func TestEndFrameIfSettled_RefusesFrameWithInFlightRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	node := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &now)
	seedNode(t, ctx, d, instanceID, node, "stale", &frameID)

	var moved bool
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		result, err := d.Tables().Frames().EndFrameIfSettled(ctx, frameID, tx)
		moved = result.Transitioned
		return err
	}))
	require.False(t, moved, "a frame with a non-terminal run must not be ended")

	var endedAtNull bool
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT ended_at IS NULL FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &endedAtNull)
	require.True(t, endedAtNull, "refused end must leave ended_at unset")
}

type endFrameOutcome struct {
	observedSettled bool
	moved           bool
	err             error
}

func TestEndFrameIfSettled_ConcurrentRunInsertCannotEndFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	node := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &now)
	seedNode(t, ctx, d, instanceID, node, "fresh", &frameID)

	endObserved := make(chan struct{})
	insertCommitted := make(chan struct{})
	result := make(chan endFrameOutcome, 1)

	go func() {
		var out endFrameOutcome
		out.err = d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			pend, err := d.Tables().Frames().ListRunningFramesNoPendingNodes(ctx, tx)
			if err != nil {
				return err
			}
			for _, p := range pend {
				if p.FrameID == frameID {
					out.observedSettled = true
				}
			}
			close(endObserved)
			<-insertCommitted
			result, err := d.Tables().Frames().EndFrameIfSettled(ctx, frameID, tx)
			out.moved = result.Transitioned
			return err
		})
		result <- out
	}()

	<-endObserved
	seedNodeRun(t, ctx, d, instanceID, node, "stale", frameID)
	close(insertCommitted)

	out := <-result
	require.NoError(t, out.err)
	require.True(t, out.observedSettled,
		"frame must have looked settled at observation time — that is the window the finding targets")
	require.False(t, out.moved,
		"the stamp must re-check in-tx and refuse: a run committed after observation but before the stamp")

	var endedAtNull bool
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT ended_at IS NULL FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &endedAtNull)
	require.True(t, endedAtNull,
		"a run inserted concurrently with the end-stamp must not end up in an ended frame")
}

func TestRunTick_NoOp_EmptyDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))
}
