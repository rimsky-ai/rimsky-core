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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func runTickAgainstDriver(ctx context.Context, d persistence.Database, log frame.Logger) error {
	return frame.RunTick(ctx, d.Tables(), d.Queue(), log)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seedNode inserts a minimal rimsky_nodes row plus (when the requested
// state is not 'fresh') a matching in-flight rimsky_node_runs row.
// Post-stage-3 cutover: state lives on the run row; 'fresh' is the
// no-run-row state.
//
// Bypasses NodeTable.Create+UpdateState because the test seeds
// out-of-band states (e.g. failed) that the state machine would reject
// when re-traversed.
func seedNode(t *testing.T, ctx context.Context, d persistence.Database,
	instanceID uuid.UUID, nodeID uuid.UUID, state string, frameID *uuid.UUID) {
	t.Helper()
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_nodes
            (id, instance_id, node_type, frame_id)
        VALUES ($1, $2, 'n', $3)
    `, nodeID, instanceID, frameID)
	if state == "fresh" || state == "" {
		return
	}
	if frameID == nil {
		t.Fatalf("seedNode: state=%q requires a non-nil frame_id (rimsky_node_runs.frame_id NOT NULL)", state)
	}
	phase := "pending"
	switch state {
	case "running":
		phase = "active"
	case "failed":
		phase = "failed"
	case "parked":
		phase = "parked"
	}
	var mainScopeID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1`,
		[]any{instanceID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        VALUES (gen_random_uuid(), $1, NULL, ARRAY[]::text[], NOW(), $2, $3, $4, $5)
    `, nodeID, phase, state, frameID, mainScopeID)
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
	var mainScopeID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d, `
        SELECT i.main_run_scope_id FROM rimsky_instances i
        JOIN rimsky_nodes n ON n.instance_id = i.id
        WHERE n.id = $1
    `, []any{nodeID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, claimed_by, frame_id, run_scope_id)
        VALUES ($1, $2, NULL, '{}', $3, $4, $5)
    `, uuid.New(), nodeID, claimedByPtr, frameID, mainScopeID)
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

	// @deliberate: two queued frames; spacing queued_at makes ordering
	// deterministic.
	id1 := seedFrameRow(t, ctx, d, instanceID, "serial_queue", "queued",
		[]uuid.UUID{srcA}, nil, 600000)
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

	// @deliberate: First frame's source must now be stale with frame_id = id1.
	// Post-stage-3: state lives on the in-flight run row.
	var nodeState string
	var nodeFrameID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COALESCE(r.state, 'fresh'), n.frame_id
		   FROM rimsky_nodes n
		   LEFT JOIN rimsky_node_runs r
		          ON r.node_id = n.id
		         AND r.phase IN ('pending','active','held','parked')
		  WHERE n.id = $1`, []any{srcA}, &nodeState, &nodeFrameID)
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
	// @deliberate: currently-running frame on srcA, all nodes fresh
	// (frame-end candidate).
	runID := seedFrameRow(t, ctx, d, instanceID, "coalesce", "running",
		[]uuid.UUID{srcA}, &now, 600000)
	seedNode(t, ctx, d, instanceID, srcA, "fresh", &runID)
	seedNode(t, ctx, d, instanceID, srcB, "fresh", nil)
	queuedID := seedFrameRow(t, ctx, d, instanceID, "coalesce", "queued",
		[]uuid.UUID{srcB}, nil, 600000)

	// @deliberate: First tick: ends running, advances queued.
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
	// @deliberate: No claimed dispatches => stuck (predicate matches).

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	require.NoError(t, runTickAgainstDriver(ctx, d, logger))

	logged := buf.String()
	require.Contains(t, logged, "frame.stuck.observed",
		"expected stuck-frame observation; got logger output: %q", logged)
	require.Contains(t, logged, frameID.String(),
		"warning should mention frame_id %s; got %q", frameID.String(), logged)

	// @deliberate: Frame stays running — no destructive action.
	var fState, nState string
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_frames WHERE frame_id = $1`, []any{frameID}, &fState)
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COALESCE(r.state, 'fresh')
		   FROM rimsky_nodes n
		   LEFT JOIN rimsky_node_runs r
		          ON r.node_id = n.id
		         AND r.phase IN ('pending','active','held','parked')
		  WHERE n.id = $1`, []any{src}, &nState)
	require.Equal(t, "running", fState,
		"frame must stay running after stuck-frame observation; warning is non-destructive")
	require.Equal(t, "stale", nState,
		"wedged node must keep its state; warning does not fail nodes")

	// @deliberate: Instance terminated_at must NOT be set as a side effect of the warning.
	var terminatedAt *time.Time
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`, []any{instanceID}, &terminatedAt)
	require.Nil(t, terminatedAt,
		"stuck-frame warning must not terminate the instance")
}

// TestDurableByDefaultVsTerminateAfterRun pins the durable-by-default
// instance lifecycle at the frame-engine level: at frame-end the engine
// calls MarkInstanceTerminatedIfDone for every instance, but only an
// instance created with terminate_after_run=true self-terminates. A
// durable instance (the default) survives its own drain — terminated_at
// stays NULL after its frame ends.
//
// Both instances run a single fresh-node frame to terminal via one
// RunTick (frame-end detection flips the running frame to completed and
// evaluates the terminal predicate in the same tx). Instance B's
// terminate_after_run flag is set by a direct UPDATE after seeding (the
// seed helper goes through InstanceCreateInput, which now carries the
// flag, but the column is also settable here so the test reads as a
// targeted lifecycle assertion).
func TestDurableByDefaultVsTerminateAfterRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceA := seedTemplateAndInstance(t, ctx, d, "serial_queue")
	srcA := uuid.New()
	nowA := time.Now()
	frameA := seedFrameRow(t, ctx, d, instanceA, "serial_queue", "running",
		[]uuid.UUID{srcA}, &nowA, 600000)
	seedNode(t, ctx, d, instanceA, srcA, "fresh", &frameA)

	_, instanceB := seedTemplateAndInstance(t, ctx, d, "serial_queue")
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_instances SET terminate_after_run = true WHERE id = $1`, instanceB)
	srcB := uuid.New()
	nowB := time.Now()
	frameB := seedFrameRow(t, ctx, d, instanceB, "serial_queue", "running",
		[]uuid.UUID{srcB}, &nowB, 600000)
	seedNode(t, ctx, d, instanceB, srcB, "fresh", &frameB)

	// @deliberate: One tick: both frames end (all-fresh → completed) and
	// each instance's terminal predicate is evaluated at frame-end.
	require.NoError(t, runTickAgainstDriver(ctx, d, quietLogger()))

	// @deliberate: Both frames must have ended.
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

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "serial_queue")
	src := uuid.New()
	now := time.Now()
	frameID := seedFrameRow(t, ctx, d, instanceID, "serial_queue", "completed",
		[]uuid.UUID{src}, &now, 600000)
	// @deliberate: stamp ended_at — the 'completed' constraint requires it.
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
