// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// nodes_test.go — HTTP-level tests for the /nodes/{id} detail route.
//
// TestGetNode_SettlingSignalType pins S-control-api-mcp-node-detail-
// resolution-flavor (spec 2026-06-06-comprehensive-gap-closure): the
// node-detail JSON must carry the node's settling signal type read from
// the real persisted rimsky_node_runs.settling_signal_type column that
// `toNodeResponse` projects, and must omit it for an unsettled node.
package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

// TestInvalidateNode_FrameIn_JoinsRunningFrame pins
// S-cascade-operator-frame-in (spec 2026-06-06-comprehensive-gap-closure,
// plan task TEMPLCASCADE-6): an operator invalidate with `{"frame":"in"}`
// against a node whose instance has a currently-OPEN cascade frame F must
// JOIN that running frame — the target acquires frame_id == F — rather
// than being downgraded to a freshly-enqueued next-frame.
//
// Setup drives the genuinely-running-frame state through the real
// persistence writers (the same shapes frame.advanceOneFrame writes):
//   - the instance's queued root frame F is promoted to running and the
//     source node (root) is marked stale-with-frame_id=F (settled inside
//     the running frame);
//   - the target (the dependent `child` node, which subscribes to root)
//     is given an in-flight run row so it is genuinely mid-drain inside
//     the open frame, not hand-rolled fresh state.
//
// The handler then receives POST /nodes/{child}/invalidate {"frame":"in"}.
//
// Observable assertions (real persisted state + event log):
//  1. the target's node row carries frame_id == F (the running frame) —
//     NOT a freshly-enqueued next-frame id, and NOT nil;
//  2. a `state_transition` event for the target carries reason
//     `in_frame_invalidate` and frame_id == F;
//  3. no SECOND running/queued frame was created for the target (the
//     invalidate joined F, it did not enqueue a next frame for the child).
//
// RED today: handleInvalidateNode (nodes.go:191-200) builds
// runtime.InvalidateArgs with Frame=body.Frame but never sets
// SourceNodeID / SourceFrameID, so invalidateInFrame
// (cascade_invalidate.go:238) sees both nil and unconditionally falls
// back to invalidateNextFrame — the target gets a freshly-enqueued
// next-frame id (or stays nil), never F, and the `in_frame_invalidate`
// state_transition event is never emitted.
func TestInvalidateNode_FrameIn_JoinsRunningFrame(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "frame-in-"+uuid.NewString())
	source := nodeOfType(t, h, inst, "root")  // frame source (the settled node)
	target := nodeOfType(t, h, inst, "child") // the dependent we invalidate

	// Resolve the lone queued frame sourced on the root that seedInstance
	// enqueued at instance-create. This becomes the OPEN cascade frame F
	// that `frame: in` must join.
	var frameF uuid.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT frame_id FROM rimsky_frames
         WHERE instance_id = $1 AND state = 'queued' AND $2 = ANY(source_node_ids)
         ORDER BY queued_at DESC LIMIT 1
    `, []any{inst.ID, source.ID}, &frameF)
	require.NotEqual(t, uuid.Nil, frameF)

	// Promote that queued root frame to running and bind the source node
	// to it (settled-in-running-frame), exactly as frame.advanceOneFrame
	// does.
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		moved, err := h.persist.Frames().PromoteQueuedFrameToRunning(ctx, frameF, tx)
		if err != nil {
			return err
		}
		require.True(t, moved, "queued root frame must promote to running")
		matched, err := h.persist.Frames().MarkSourceNodeStale(ctx, inst.ID, source.ID, frameF, tx)
		if err != nil {
			return err
		}
		require.True(t, matched, "source node must bind to the running frame")
		return nil
	}))

	// Give the target an in-flight run row so it is genuinely mid-drain
	// inside the open frame F (the dependent the cascade has not settled
	// yet). Its frame_id starts unbound; the in-frame join must set it to F.
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1
    `, []any{inst.ID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        VALUES (gen_random_uuid(), $1, 'worker', ARRAY[]::text[], now(), 'pending', 'fresh', $2, $3)
    `, target.ID, frameF, mainScopeID)

	// Snapshot the frame count so we can prove no SECOND frame was
	// enqueued for the target (the in-frame join must not create a
	// next-frame).
	var framesBefore int
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1
    `, []any{inst.ID}, &framesBefore)

	// Issue the operator invalidate with frame: in against the target.
	status, out := h.httpJSON(t, "POST", "/nodes/"+target.ID.String()+"/invalidate", map[string]any{
		"reason": "mid-cascade correction",
		"frame":  "in",
	})
	require.Equal(t, http.StatusOK, status, out)

	// Assertion 1: the target now carries frame_id == F (joined the
	// running frame), not a freshly-enqueued next-frame id and not nil.
	var loaded *persistence.NodeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Nodes().Get(ctx, target.ID, tx)
		loaded = r
		return err
	}))
	require.NotNil(t, loaded)
	require.NotNil(t, loaded.FrameID,
		"frame: in must bind the target to the running frame, not leave it unbound")
	require.Equal(t, frameF, *loaded.FrameID,
		"frame: in must join the RUNNING frame F, not enqueue a next-frame id")

	// Assertion 2: a state_transition event for the target carries reason
	// `in_frame_invalidate` and frame_id == F.
	var stReason, stFrameID string
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT payload->>'reason', payload->>'frame_id'
          FROM rimsky_events
         WHERE node_id = $1 AND kind = 'state_transition'
           AND payload->>'reason' = 'in_frame_invalidate'
         ORDER BY occurred_at DESC, id DESC
         LIMIT 1
    `, []any{target.ID}, &stReason, &stFrameID)
	require.Equal(t, "in_frame_invalidate", stReason,
		"frame: in must emit a state_transition with reason in_frame_invalidate")
	require.Equal(t, frameF.String(), stFrameID,
		"the in_frame_invalidate event must carry the running frame's id")

	// Assertion 3: no SECOND frame was enqueued for the target — the
	// invalidate joined F rather than queuing a next-frame.
	var framesAfter int
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1
    `, []any{inst.ID}, &framesAfter)
	require.Equal(t, framesBefore, framesAfter,
		"frame: in must not enqueue a next-frame; it joins the running frame")
}

// nodeOfType returns the instance's node whose node_type matches `typ`.
func nodeOfType(t *testing.T, h *harness, inst persistence.InstanceRow, typ string) persistence.NodeRow {
	t.Helper()
	ctx := context.Background()
	var nodes []persistence.NodeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Nodes().ListByInstance(ctx, inst.ID, tx)
		nodes = r
		return err
	}))
	for _, n := range nodes {
		if n.NodeType == typ {
			return n
		}
	}
	t.Fatalf("no node of type %q in instance %s", typ, inst.ID)
	return persistence.NodeRow{}
}

// TestGetNode_SettlingSignalType asserts GET /nodes/{id} surfaces the
// node's settling signal type. The value is written through the REAL
// persisted column (`rimsky_node_runs.settling_signal_type`) that the
// node-detail projection reads — first by inserting a terminal run row
// (the same shape the reset test uses to satisfy the NOT NULL frame_id /
// run_scope_id constraints), then by writing the signal type via the
// real RunTreeTable.UpdateStateAndOutcome writer. The handler reads the
// same column via the nodeSelect LATERAL, so the response is the
// observable proof that the projection carries it end to end.
//
// RED today: `nodeResponse` has no `settling_signal_type` field, so the
// key is absent on the wire and the require.Equal fails on a nil value.
func TestGetNode_SettlingSignalType(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	// Case 1: a node that has settled with a known canonical signal type.
	inst := seedInstance(t, h, "node-signal-"+uuid.NewString())
	settledNode := firstNode(t, h, inst)

	// Resolve the node id through the public surface (GET
	// /instances/{id}/nodes) so the test exercises the same resolution an
	// operator would, then key the seed on that id.
	status, listOut := h.httpJSON(t, "GET", "/instances/"+inst.ID.String()+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, listOut)
	nodes, _ := listOut["nodes"].([]any)
	require.NotEmpty(t, nodes)
	var resolvedNodeID string
	for _, n := range nodes {
		row, _ := n.(map[string]any)
		if row["id"] == settledNode.ID.String() {
			resolvedNodeID = settledNode.ID.String()
		}
	}
	require.Equal(t, settledNode.ID.String(), resolvedNodeID,
		"settled node id must be discoverable via the instance-nodes listing")

	// Seed the REAL persisted column the handler projects. A
	// freshly-created node has no run row (settling_signal_type is NULL),
	// so first insert a terminal run row carrying the NOT NULL frame_id /
	// run_scope_id, then write the canonical signal type through the real
	// RunTreeTable writer (UpdateStateAndOutcome) — the same column the
	// nodeSelect LATERAL surfaces into NodeRow.SettlingSignalType.
	const wantSignalType = "terminal/success"
	runID := seedTerminalRunWithSignalType(ctx, t, h, inst, settledNode.ID, wantSignalType)
	require.NotEqual(t, uuid.Nil, runID)

	status, out := h.httpJSON(t, "GET", "/nodes/"+settledNode.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, wantSignalType, out["settling_signal_type"],
		"node detail must carry the persisted settling signal type")

	// Case 2: a freshly-created node with no settle — the field is
	// absent/empty (omitempty drops it when the projected column is NULL).
	freshInst := seedInstance(t, h, "node-fresh-"+uuid.NewString())
	freshNode := firstNode(t, h, freshInst)
	status, freshOut := h.httpJSON(t, "GET", "/nodes/"+freshNode.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, freshOut)
	got, present := freshOut["settling_signal_type"]
	require.False(t, present && got != nil && got != "",
		"unsettled node must omit settling_signal_type, got %v", got)
}

// seedTerminalRunWithSignalType inserts a completed-terminal run row for
// the node and writes a canonical settling signal type onto it through
// the real RunTreeTable.UpdateStateAndOutcome writer. Returns the run id.
//
// The INSERT mirrors the reset test's pattern for the run row's NOT NULL
// frame_id (any frame for the instance) and run_scope_id (the instance's
// main run scope). The signal type is written via the production writer
// rather than a raw column poke so the test drives the same persistence
// path the runtime settle path uses.
func seedTerminalRunWithSignalType(
	ctx context.Context, t *testing.T, h *harness,
	inst persistence.InstanceRow, nodeID shared.UUID, signalType string,
) uuid.UUID {
	t.Helper()
	var frameID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 ORDER BY queued_at DESC LIMIT 1
    `, []any{inst.ID}, &frameID)
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1
    `, []any{inst.ID}, &mainScopeID)

	// Insert a completed-terminal run row. State 'fresh' on a completed
	// terminal row matches the nodeSelect contract: a completed run
	// surfaces state='fresh' while still carrying settling_signal_type.
	runID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, active_terminal_at, run_scope_id)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], now(), 'completed', 'fresh', $3, now(), $4)
    `, runID, nodeID, frameID, mainScopeID)

	// Write the canonical signal type through the real writer.
	sig := signalType
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.RunTree().UpdateStateAndOutcome(ctx, tx, runID, cascade.NodeStateFresh, &sig)
	}))
	return runID
}
