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
	"time"

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

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 15*time.Second),
		"worker did not reach failed on first fire")

	require.True(t, waitForFramesByState(t, h, iid, "failed", 1, 5*time.Second),
		"first frame should end failed")

	var priorFrameID *uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&priorFrameID))
	require.NotNil(t, priorFrameID,
		"failed node should preserve frame_id from the failed frame")

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

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 20*time.Second),
		"worker did not reach fresh after reset+empty-message; the two-step retry workflow must drive the node back through the cascade")

	require.True(t, waitForFramesByState(t, h, iid, "completed", 1, 5*time.Second),
		"second frame should end completed")

	var finalFrameID *uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, uuid.UUID(worker.ID)).Scan(&finalFrameID))
	require.Nil(t, finalFrameID,
		"fresh node must carry no frame_id after work_completed")

	var failedRowSettlingSig *string
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT settling_signal_type FROM rimsky_node_runs
		   WHERE node_id = $1 AND phase = 'failed'
		   ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC
		   LIMIT 1`,
		uuid.UUID(worker.ID)).Scan(&failedRowSettlingSig))
	require.Nil(t, failedRowSettlingSig,
		"failed-terminal row's settling_signal_type must be reset to NULL by handleResetNode")
}
