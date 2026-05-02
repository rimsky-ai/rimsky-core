package frame_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/internal/pgtest"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seedNode inserts a minimal rimsky_nodes row in the given state.
func seedNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	instanceID uuid.UUID, nodeID uuid.UUID, state string, frameID *uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx, `
        INSERT INTO rimsky_nodes
            (id, instance_id, node_type, state, dependencies, frame_id)
        VALUES ($1, $2, 'n', $3, ARRAY[]::UUID[], $4)
    `, nodeID, instanceID, state, frameID)
	require.NoError(t, err)
}

// seedFrameRow inserts a rimsky_frames row with explicit fields.
// Terminal states (completed/failed) require ended_at to be set; the helper
// fills it with now() when caller-provided startedAt is non-nil and state is
// terminal.
func seedFrameRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	instanceID uuid.UUID, mode, state string, sources []uuid.UUID,
	startedAt *time.Time, timeoutMs int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var endedAt *time.Time
	if state == "completed" || state == "failed" {
		end := time.Now()
		endedAt = &end
	}
	_, err := pool.Exec(ctx, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, mode, state, source_node_ids, queued_at, started_at, ended_at, frame_timeout_ms)
        VALUES ($1, $2, $3, $4, $5::UUID[], now(), $6, $7, $8)
    `, id, instanceID, mode, state, sources, startedAt, endedAt, timeoutMs)
	require.NoError(t, err)
	return id
}

// seedDispatch inserts a rimsky_dispatch row.
func seedDispatch(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	nodeID uuid.UUID, frameID uuid.UUID, claimedBy string) {
	t.Helper()
	var claimedByPtr interface{} = claimedBy
	if claimedBy == "" {
		claimedByPtr = nil
	}
	_, err := pool.Exec(ctx, `
        INSERT INTO rimsky_dispatch
            (id, node_id, executor_name, required_stores, claimed_by, frame_id)
        VALUES ($1, $2, NULL, '{}', $3, $4)
    `, uuid.New(), nodeID, claimedByPtr, frameID)
	require.NoError(t, err)
}

func TestRunTick_FrameEndDetection_AllFresh_Completed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "serial_queue")
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, pool, instanceID, "serial_queue", "running",
		[]uuid.UUID{src}, &now, 600000)
	seedNode(t, ctx, pool, instanceID, src, "fresh", &frameID)

	require.NoError(t, frame.RunTick(ctx, pool, quietLogger()))

	var state string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, frameID).Scan(&state))
	require.Equal(t, "completed", state)
}

func TestRunTick_FrameEndDetection_OneFailed_Failed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "serial_queue")
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, pool, instanceID, "serial_queue", "running",
		[]uuid.UUID{src}, &now, 600000)
	seedNode(t, ctx, pool, instanceID, src, "failed", &frameID)

	require.NoError(t, frame.RunTick(ctx, pool, quietLogger()))

	var state string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, frameID).Scan(&state))
	require.Equal(t, "failed", state)
}

func TestRunTick_AdvanceQueued_SerialQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "serial_queue")
	srcA := uuid.New()
	srcB := uuid.New()
	seedNode(t, ctx, pool, instanceID, srcA, "fresh", nil)
	seedNode(t, ctx, pool, instanceID, srcB, "fresh", nil)

	// Two queued frames. Need to space queued_at to make ordering deterministic.
	id1 := seedFrameRow(t, ctx, pool, instanceID, "serial_queue", "queued",
		[]uuid.UUID{srcA}, nil, 600000)
	// Bump first frame's queued_at to be earlier.
	_, err := pool.Exec(ctx,
		`UPDATE rimsky_frames SET queued_at = now() - interval '1 second' WHERE frame_id = $1`, id1)
	require.NoError(t, err)
	id2 := seedFrameRow(t, ctx, pool, instanceID, "serial_queue", "queued",
		[]uuid.UUID{srcB}, nil, 600000)

	require.NoError(t, frame.RunTick(ctx, pool, quietLogger()))

	var s1, s2 string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, id1).Scan(&s1))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, id2).Scan(&s2))
	require.Equal(t, "running", s1)
	require.Equal(t, "queued", s2)

	// First frame's source must now be stale with frame_id = id1.
	var nodeState string
	var nodeFrameID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state, frame_id FROM rimsky_nodes WHERE id = $1`, srcA).Scan(&nodeState, &nodeFrameID))
	require.Equal(t, "stale", nodeState)
	require.Equal(t, id1, nodeFrameID)
}

func TestRunTick_AdvanceTrailing_Coalesce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "coalesce")
	srcA := uuid.New()
	srcB := uuid.New()
	now := time.Now()
	// Currently-running frame on srcA, all nodes fresh (frame-end candidate).
	runID := seedFrameRow(t, ctx, pool, instanceID, "coalesce", "running",
		[]uuid.UUID{srcA}, &now, 600000)
	seedNode(t, ctx, pool, instanceID, srcA, "fresh", &runID)
	seedNode(t, ctx, pool, instanceID, srcB, "fresh", nil)
	// Pending coalesce frame on srcB.
	queuedID := seedFrameRow(t, ctx, pool, instanceID, "coalesce", "queued",
		[]uuid.UUID{srcB}, nil, 600000)

	// First tick: ends running, advances queued.
	require.NoError(t, frame.RunTick(ctx, pool, quietLogger()))

	var runState, queuedState string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, runID).Scan(&runState))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, queuedID).Scan(&queuedState))
	require.Equal(t, "completed", runState)
	require.Equal(t, "running", queuedState)
}

func TestRunTick_ReapStuckFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "serial_queue")
	src := uuid.New()
	stuckStart := time.Now().Add(-11 * time.Minute)
	frameID := seedFrameRow(t, ctx, pool, instanceID, "serial_queue", "running",
		[]uuid.UUID{src}, &stuckStart, 600000)
	seedNode(t, ctx, pool, instanceID, src, "stale", &frameID)
	// No claimed dispatches => stuck.

	require.NoError(t, frame.RunTick(ctx, pool, quietLogger()))

	var fState, nState string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, frameID).Scan(&fState))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_nodes WHERE id = $1`, src).Scan(&nState))
	require.Equal(t, "failed", fState)
	require.Equal(t, "failed", nState)
}

// TestRunTick_ReapStuckFrame_TerminatesInstance pins the
// terminated_at write inside reapOneStuckFrame: when the only frame
// for an instance is wedged and gets reaped, the instance row's
// terminated_at must be populated so the OnInstanceTerminated
// fan-out can fire downstream. Without this set, the instance
// would stay un-terminated forever and the lifecycle event would
// leak.
func TestRunTick_ReapStuckFrame_TerminatesInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "serial_queue")
	src := uuid.New()
	stuckStart := time.Now().Add(-11 * time.Minute)
	frameID := seedFrameRow(t, ctx, pool, instanceID, "serial_queue", "running",
		[]uuid.UUID{src}, &stuckStart, 600000)
	seedNode(t, ctx, pool, instanceID, src, "stale", &frameID)

	require.NoError(t, frame.RunTick(ctx, pool, quietLogger()))

	var frameState string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, frameID).Scan(&frameState))
	require.Equal(t, "failed", frameState)

	var terminatedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`, instanceID,
	).Scan(&terminatedAt))
	require.NotNil(t, terminatedAt,
		"reaping the only stuck frame must set rimsky_instances.terminated_at so the lifecycle terminate fan-out fires")
}

func TestRunTick_ReapOrphanDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "serial_queue")
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, pool, instanceID, "serial_queue", "completed",
		[]uuid.UUID{src}, &now, 600000)
	// Mark ended_at since 'completed' constraint requires it.
	_, err := pool.Exec(ctx,
		`UPDATE rimsky_frames SET ended_at = now() WHERE frame_id = $1`, frameID)
	require.NoError(t, err)

	seedNode(t, ctx, pool, instanceID, src, "fresh", nil)
	seedDispatch(t, ctx, pool, src, frameID, "supervisor-1")

	require.NoError(t, frame.RunTick(ctx, pool, quietLogger()))

	var claimedBy *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT claimed_by FROM rimsky_dispatch WHERE node_id = $1`, src).Scan(&claimedBy))
	require.Nil(t, claimedBy)
}

func TestRunTick_NoOp_EmptyDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	require.NoError(t, frame.RunTick(ctx, pool, quietLogger()))
}
