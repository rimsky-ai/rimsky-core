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

	// @constraint: case 1: a node that has settled with a known canonical signal type.
	inst := seedInstance(t, h, "node-signal-"+uuid.NewString())
	settledNode := firstNode(t, h, inst)

	// @constraint: resolve the node id through the public surface (GET
	// /instances/{id}/nodes) so the test exercises the same resolution an
	// operator would, then key the seed on that id.
	status, listOut := h.httpJSON(t, "GET", "/v1/instances/"+inst.ID.String()+"/nodes", nil)
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

	// @constraint: seed the REAL persisted column the handler projects. A
	// freshly-created node has no run row (settling_signal_type is NULL),
	// so first insert a terminal run row carrying the NOT NULL frame_id /
	// run_scope_id, then write the canonical signal type through the real
	// RunTreeTable writer (UpdateStateAndOutcome) — the same column the
	// nodeSelect LATERAL surfaces into NodeRow.SettlingSignalType.
	const wantSignalType = "terminal/success"
	runID := seedTerminalRunWithSignalType(ctx, t, h, inst, settledNode.ID, wantSignalType)
	require.NotEqual(t, uuid.Nil, runID)

	status, out := h.httpJSON(t, "GET", "/v1/nodes/"+settledNode.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, wantSignalType, out["settling_signal_type"],
		"node detail must carry the persisted settling signal type")

	// @constraint: case 2: a freshly-created node with no settle — the field is
	// absent/empty (omitempty drops it when the projected column is NULL).
	freshInst := seedInstance(t, h, "node-fresh-"+uuid.NewString())
	freshNode := firstNode(t, h, freshInst)
	status, freshOut := h.httpJSON(t, "GET", "/v1/nodes/"+freshNode.ID.String(), nil)
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
	// @constraint: post-spec instance creation is idle (no frame is
	// enqueued), so the test seeds a triggering message + frame
	// directly to satisfy the node_run FK on frame_id.
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1
    `, []any{inst.ID}, &mainScopeID)
	msgID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_messages
            (id, instance_id, type, sender_kind, sender, payload, received_at)
        VALUES ($1, $2, '', 'operator', 'test', E'{}'::bytea, now())
    `, msgID, inst.ID)
	frameID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, state, queued_at, ended_at, triggering_message_id, frame_timeout_ms)
        VALUES ($1, $2, 'completed', now(), now(), $3, 60000)
    `, frameID, inst.ID, msgID)

	// @constraint: insert a completed-terminal run row. State 'fresh' on a completed
	// terminal row matches the nodeSelect contract: a completed run
	// surfaces state='fresh' while still carrying settling_signal_type.
	runID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, active_terminal_at, run_scope_id)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], now(), 'completed', 'fresh', $3, now(), $4)
    `, runID, nodeID, frameID, mainScopeID)

	// @constraint: write the canonical signal type through the real writer.
	sig := signalType
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.RunTree().UpdateStateAndOutcome(ctx, tx, runID, cascade.NodeStateFresh, &sig)
	}))
	return runID
}
