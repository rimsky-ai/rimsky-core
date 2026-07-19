// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: node-admin
// @decision: node-reset-as-pure-retry-budget-clear
package frame_resolution

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestResetFailedNodeDrivesThroughFrameEngine(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Error("test_failure", nil)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "reset-via-frame-engine", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-reset-frame", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.WaitForNodeState(worker.ID, cascade.NodeStateFailed)

	waitForFramesByState(t, h, iid, "failed", 1)

	var priorFrameID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_node_runs
		  WHERE node_id = $1 AND state = 'failed'
		  ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC LIMIT 1`,
		uuid.UUID(worker.ID)).Scan(&priorFrameID))
	require.NotEqual(t, uuid.Nil, priorFrameID,
		"failed run should carry the frame_id of the failed frame")
	_ = priorFrameID

	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")

	resp, err := http.Post(
		h.ControlBase+"/v1/nodes/"+worker.ID.String()+"/reset",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// @story: node-admin
	// @decision: node-reset-as-pure-retry-budget-clear
	h.PostInstanceMessage(iid, "", nil, "reset-followup-wake-"+iid.String())

	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	waitForFramesByState(t, h, iid, "completed", 1)

	var runFrameID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_node_runs
		  WHERE node_id = $1 AND state = 'fresh'
		  ORDER BY sequence DESC LIMIT 1`,
		uuid.UUID(worker.ID)).Scan(&runFrameID))
	require.NotEqual(t, uuid.Nil, runFrameID,
		"the freshly-settled run must carry its frame_id")
	var inFlight int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_node_runs
		  WHERE node_id = $1 AND state IN ('pending','stale','running','held','parked')`,
		uuid.UUID(worker.ID)).Scan(&inFlight))
	require.Equal(t, 0, inFlight, "no in-flight run may remain after fresh settlement")

	var failedRowSettlingSig *string
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT settling_signal_type FROM rimsky_node_runs
		   WHERE node_id = $1 AND state = 'failed'
		   ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC
		   LIMIT 1`,
		uuid.UUID(worker.ID)).Scan(&failedRowSettlingSig))
	require.Nil(t, failedRowSettlingSig,
		"failed-terminal row's settling_signal_type must be reset to NULL by handleResetNode")
}
