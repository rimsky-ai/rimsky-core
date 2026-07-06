// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestGetNode_SettlingSignalType(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "node-signal-"+uuid.NewString())
	settledNode := firstNode(t, h, inst)

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

	const wantSignalType = "terminal/success"
	runID := seedTerminalRunWithSignalType(ctx, t, h, inst, settledNode.ID, wantSignalType)
	require.NotEqual(t, uuid.Nil, runID)

	status, out := h.httpJSON(t, "GET", "/v1/nodes/"+settledNode.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, wantSignalType, out["settling_signal_type"],
		"node detail must carry the persisted settling signal type")

	freshInst := seedInstance(t, h, "node-fresh-"+uuid.NewString())
	freshNode := firstNode(t, h, freshInst)
	status, freshOut := h.httpJSON(t, "GET", "/v1/nodes/"+freshNode.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, freshOut)
	got, present := freshOut["settling_signal_type"]
	require.False(t, present && got != nil && got != "",
		"unsettled node must omit settling_signal_type, got %v", got)
}

func seedTerminalRunWithSignalType(
	ctx context.Context, t *testing.T, h *harness,
	inst persistence.InstanceRow, nodeID shared.UUID, signalType string,
) uuid.UUID {
	t.Helper()
	mainScopeID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_run_scopes(id, graph_name, instance_id, partition_key, created_at)
        VALUES ($1, 'main', $2, '', now())
    `, uuid.UUID(mainScopeID), inst.ID)
	msgID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_messages
            (id, instance_id, type, sender_kind, sender, payload, received_at)
        VALUES ($1, $2, '', 'operator', 'test', E'{}'::bytea, now())
    `, msgID, inst.ID)
	frameID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, ended_at, triggering_message_id, root_run_scope_id, frame_timeout_ms)
        VALUES ($1, $2, now(), $3, $4, 60000)
    `, frameID, inst.ID, msgID, mainScopeID)

	runID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, frame_id, active_terminal_at, run_scope_id, sequence)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], now(), 'fresh', $3, now(), $4, 0)
    `, runID, nodeID, frameID, mainScopeID)

	sig := signalType
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.NodeRunTree().UpdateStateAndOutcome(ctx, tx, runID, cascade.NodeStateFresh, &sig)
	}))
	return runID
}
