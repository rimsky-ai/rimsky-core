// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/frame"
	"github.com/fallguy/rimsky/internal/pgtest"
)

func runTickAgainstDriver(ctx context.Context, d persistence.Database, log frame.Logger) error {
	return frame.RunTick(ctx, d.Tables(), d.Queue(), log)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seedNode inserts a minimal rimsky_nodes row in the given state. Bypasses
// NodeTable.Create+UpdateState because the test seeds out-of-band states
// (e.g. failed) that the state machine would reject when re-traversed.
func seedNode(t *testing.T, ctx context.Context, d persistence.Database,
	instanceID uuid.UUID, nodeID uuid.UUID, state string, frameID *uuid.UUID) {
	t.Helper()
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_nodes
            (id, instance_id, node_type, state, frame_id)
        VALUES ($1, $2, 'n', $3, $4)
    `, nodeID, instanceID, state, frameID)
}

// seedFrameRow inserts a rimsky_frames row with explicit fields. Goes
// through ExecForTest because some target states (completed/failed) and
// queued_at offsets are not reachable through FrameTable.
//
// last_progress_at is set to startedAt (when non-nil) so the stuck-frame
// warning sees a frame whose progress timestamp matches its perceived
// stuck time — the per-test contract is that a "stuck-since-X" frame
// has had no progress since X. Per the reactive-loops + lifecycle-handlers
// spec §7, frame_timeout_ms compares against last_progress_at.
func seedFrameRow(t *testing.T, ctx context.Context, d persistence.Database,
	instanceID uuid.UUID, mode, state string, sources []uuid.UUID,
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
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, ended_at, frame_timeout_ms, last_progress_at)
        VALUES ($1, $2, $3, $4, $5::UUID[], now(), $6, $7, $8, $9)
    `, id, instanceID, mode, state, sources, startedAt, endedAt, timeoutMs, progressAt)
	return id
}

// seedDispatch inserts a rimsky_node_runs row directly. Bypasses
// Queue.Enqueue+ClaimDispatchRow because the test fixes a static id and
// pre-claims the row in one shot.
func seedDispatch(t *testing.T, ctx context.Context, d persistence.Database,
	nodeID uuid.UUID, frameID uuid.UUID, claimedBy string) {
	t.Helper()
	var claimedByPtr interface{} = claimedBy
	if claimedBy == "" {
		claimedByPtr = nil
	}
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, claimed_by, frame_id)
        VALUES ($1, $2, NULL, '{}', $3, $4)
    `, uuid.New(), nodeID, claimedByPtr, frameID)
}

func TestRunTick_FrameEndDetection_AllFresh_Completed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "serial_queue")
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, "serial_queue", "running",
		[]uuid.UUID{src}, &now, 600000)
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

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "serial_queue")
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, "serial_queue", "running",
		[]uuid.UUID{src}, &now, 600000)
	seedNode(t, ctx, d, instanceID, src, "failed", &frameID)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var state string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &state)
	require.Equal(t, "failed", state)
}

func TestRunTick_AdvanceQueued_SerialQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "serial_queue")
	srcA := uuid.New()
	srcB := uuid.New()
	seedNode(t, ctx, d, instanceID, srcA, "fresh", nil)
	seedNode(t, ctx, d, instanceID, srcB, "fresh", nil)

	// Two queued frames. Need to space queued_at to make ordering deterministic.
	id1 := seedFrameRow(t, ctx, d, instanceID, "serial_queue", "queued",
		[]uuid.UUID{srcA}, nil, 600000)
	// Bump first frame's queued_at to be earlier.
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_frames SET queued_at = now() - interval '1 second' WHERE frame_id = $1`, id1)
	id2 := seedFrameRow(t, ctx, d, instanceID, "serial_queue", "queued",
		[]uuid.UUID{srcB}, nil, 600000)

	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var s1, s2 string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{id1}, &s1)
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{id2}, &s2)
	require.Equal(t, "running", s1)
	require.Equal(t, "queued", s2)

	// First frame's source must now be stale with frame_id = id1.
	var nodeState string
	var nodeFrameID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state, frame_id FROM rimsky_nodes WHERE id = $1`, []any{srcA}, &nodeState, &nodeFrameID)
	require.Equal(t, "stale", nodeState)
	require.Equal(t, id1, nodeFrameID)
}

func TestRunTick_AdvanceTrailing_Coalesce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "coalesce")
	srcA := uuid.New()
	srcB := uuid.New()
	now := time.Now()
	// Currently-running frame on srcA, all nodes fresh (frame-end candidate).
	runID := seedFrameRow(t, ctx, d, instanceID, "coalesce", "running",
		[]uuid.UUID{srcA}, &now, 600000)
	seedNode(t, ctx, d, instanceID, srcA, "fresh", &runID)
	seedNode(t, ctx, d, instanceID, srcB, "fresh", nil)
	// Pending coalesce frame on srcB.
	queuedID := seedFrameRow(t, ctx, d, instanceID, "coalesce", "queued",
		[]uuid.UUID{srcB}, nil, 600000)

	// First tick: ends running, advances queued.
	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	var runState, queuedState string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{runID}, &runState)
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{queuedID}, &queuedState)
	require.Equal(t, "completed", runState)
	require.Equal(t, "running", queuedState)
}

// TestRunTick_WarnStuckFrame asserts the stuck-frame path is purely
// observational: the `frame.stuck.observed` slog warning fires, but the
// frame stays running, the wedged node keeps its state, and the
// instance is NOT terminated.
func TestRunTick_WarnStuckFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "serial_queue")
	src := uuid.New()
	stuckStart := time.Now().Add(-11 * time.Minute)
	frameID := seedFrameRow(t, ctx, d, instanceID, "serial_queue", "running",
		[]uuid.UUID{src}, &stuckStart, 600000)
	seedNode(t, ctx, d, instanceID, src, "stale", &frameID)
	// No claimed dispatches => stuck (predicate matches).

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	require.NoError(t, runTickAgainstDriver(ctx, d, logger))

	// Warning fired with frame_id reference.
	logged := buf.String()
	require.Contains(t, logged, "frame.stuck.observed",
		"expected stuck-frame observation; got logger output: %q", logged)
	require.Contains(t, logged, frameID.String(),
		"warning should mention frame_id %s; got %q", frameID.String(), logged)

	// Frame stays running — no destructive action.
	var fState, nState string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &fState)
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_nodes WHERE id = $1`, []any{src}, &nState)
	require.Equal(t, "running", fState,
		"frame must stay running after stuck-frame observation; warning is non-destructive")
	require.Equal(t, "stale", nState,
		"wedged node must keep its state; warning does not fail nodes")

	// Instance terminated_at must NOT be set as a side effect of the warning.
	var terminatedAt *time.Time
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`, []any{instanceID}, &terminatedAt)
	require.Nil(t, terminatedAt,
		"stuck-frame warning must not terminate the instance")
}

func TestRunTick_ReapOrphanDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "serial_queue")
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, "serial_queue", "completed",
		[]uuid.UUID{src}, &now, 600000)
	// Mark ended_at since 'completed' constraint requires it.
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
