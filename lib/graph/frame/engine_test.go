// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_test

import (
	"bytes"
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
	return frame.RunTick(ctx, d.Tables(), d.Queue(), log)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
	var mainScopeID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT id FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'main'`,
		[]any{instanceID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
        VALUES (gen_random_uuid(), $1, NULL, ARRAY[]::text[], NOW(), $2, 1, 'cascade', $3, $4)
    `, nodeID, state, frameID, mainScopeID)
}

func seedFrameRow(t *testing.T, ctx context.Context, d persistence.Database,
	instanceID uuid.UUID, triggeringMessageID uuid.UUID, state string,
	startedAt *time.Time, timeoutMs int64) uuid.UUID {
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
            (frame_id, instance_id, triggering_message_id, root_run_scope_id, state, started_at, ended_at, frame_timeout_ms, last_progress_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
    `, id, instanceID, triggeringMessageID, rootScope, state, startedAt, endedAt, timeoutMs, progressAt)
	return id
}

func seedDispatch(t *testing.T, ctx context.Context, d persistence.Database,
	nodeID uuid.UUID, frameID uuid.UUID, claimedBy string) {
	t.Helper()
	var claimedByPtr interface{} = claimedBy
	if claimedBy == "" {
		claimedByPtr = nil
	}
	var mainScopeID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d, `
        SELECT s.id FROM rimsky_run_scopes s
        JOIN rimsky_nodes n ON n.instance_id = s.instance_id
        WHERE n.id = $1 AND s.graph_name = 'main'
    `, []any{nodeID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, claimed_by, frame_id, run_scope_id)
        VALUES ($1, $2, NULL, '{}', NOW(), 'running', 1, 'cascade', $3, $4, $5)
    `, uuid.New(), nodeID, claimedByPtr, frameID, mainScopeID)
}

func TestRunTick_FrameEndDetection_AllFresh_Completed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &now, 600000)
	seedNode(t, ctx, d, instanceID, src, "fresh", &frameID)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var state string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &state)
	require.Equal(t, "completed", state)
}

func TestRunTick_FrameEndDetection_OneFailed_Failed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &now, 600000)
	seedNode(t, ctx, d, instanceID, src, "failed", &frameID)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var state string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &state)
	require.Equal(t, "failed", state)
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

	msg2ID := foundationshared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
		INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind, received_at)
		VALUES ($1, $2, 'test/seed', 'test', 'operator', now() + interval '1 second')`,
		msg2ID, instanceID)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var running int
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND state = 'running'`,
		[]any{instanceID}, &running)
	require.Equal(t, 1, running, "at most one running frame per instance")

	var openedTriggeringID string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT triggering_message_id::text FROM rimsky_frames WHERE instance_id = $1 AND state = 'running'`,
		[]any{instanceID}, &openedTriggeringID)
	require.Equal(t, msgID.String(), openedTriggeringID,
		"oldest pending message opens the running frame")
}

func TestRunTick_WarnStuckFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	stuckStart := time.Now().Add(-11 * time.Minute)
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "running", &stuckStart, 600000)
	seedNode(t, ctx, d, instanceID, src, "stale", &frameID)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	require.NoError(t, runTickAgainstDriver(ctx, d, logger))

	logged := buf.String()
	require.Contains(t, logged, "frame.stuck.observed",
		"expected stuck-frame observation; got logger output: %q", logged)
	require.Contains(t, logged, frameID.String(),
		"warning should mention frame_id %s; got %q", frameID.String(), logged)

	var fState, nState string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &fState)
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COALESCE(r.state, 'fresh')
		   FROM rimsky_nodes n
		   LEFT JOIN rimsky_node_runs r
		          ON r.node_id = n.id
		         AND r.state IN ('pending','stale','running','held','parked')
		  WHERE n.id = $1`, []any{src}, &nState)
	require.Equal(t, "running", fState,
		"frame must stay running after stuck-frame observation; warning is non-destructive")
	require.Equal(t, "stale", nState,
		"wedged node must keep its state; warning does not fail nodes")

	var terminatedAt *time.Time
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`, []any{instanceID}, &terminatedAt)
	require.Nil(t, terminatedAt,
		"stuck-frame warning must not terminate the instance")
}

func TestDurableByDefaultVsTerminateAfterRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceA, msgA := seedTemplateInstanceAndMessage(t, ctx, d)
	srcA := uuid.New()
	nowA := time.Now()
	frameA := seedFrameRow(t, ctx, d, instanceA, msgA, "running", &nowA, 600000)
	seedNode(t, ctx, d, instanceA, srcA, "fresh", &frameA)

	instanceB, msgB := seedTemplateInstanceAndMessage(t, ctx, d)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_instances SET terminate_after_run = true WHERE id = $1`, instanceB)
	srcB := uuid.New()
	nowB := time.Now()
	frameB := seedFrameRow(t, ctx, d, instanceB, msgB, "running", &nowB, 600000)
	seedNode(t, ctx, d, instanceB, srcB, "fresh", &frameB)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var stateA, stateB string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{frameA}, &stateA)
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{frameB}, &stateB)
	require.Equal(t, "completed", stateA, "instance A's frame should end")
	require.Equal(t, "completed", stateB, "instance B's frame should end")

	var termA, termB *time.Time
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`, []any{instanceA}, &termA)
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`, []any{instanceB}, &termB)

	require.Nil(t, termA,
		"durable-by-default instance A must survive its own drain; terminated_at must stay NULL")
	require.NotNil(t, termB,
		"terminate_after_run instance B must self-terminate after its frame ends")
}

func TestRunTick_ReapOrphanDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	instanceID, msgID := seedTemplateInstanceAndMessage(t, ctx, d)
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, msgID, "completed", &now, 600000)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_frames SET ended_at = now() WHERE frame_id = $1`, frameID)

	seedNode(t, ctx, d, instanceID, src, "fresh", nil)
	seedDispatch(t, ctx, d, src, frameID, "supervisor-1")

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var claimedBy *string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT claimed_by FROM rimsky_node_runs WHERE node_id = $1`, []any{src}, &claimedBy)
	require.Nil(t, claimedBy)
}

func TestRunTick_NoOp_EmptyDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))
}
