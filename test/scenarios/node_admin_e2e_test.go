// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: node-admin
// @decision: node-reset-clears-failure-marker
// @decision: empty-message-as-root-trigger
package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	graphshared "github.com/rimsky-ai/rimsky-core/lib/graph/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: node
type nodeRunSummaryResponse struct {
	ActiveCount  int `json:"active_count"`
	PendingCount int `json:"pending_count"`
	FreshCount   int `json:"fresh_count"`
	FailedCount  int `json:"failed_count"`
}

type nodeDetailResponse struct {
	ID         string                  `json:"id"`
	InstanceID string                  `json:"instance_id"`
	NodeType   string                  `json:"node_type"`
	RunSummary *nodeRunSummaryResponse `json:"run_summary,omitempty"`
}

func TestAcceptance_NodeAdmin_GetAndReset(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Success(map[string]any{"w": 1}, true, "worker")
	h.Stub.WhenType("flaky").Error("my_err", map[string]any{"hint": "boom"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "node-admin", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "worker", Executor: "stub",
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "flaky", Executor: "stub",
				MaxRetries: node.IntPtr(2),
				RetryBackoff: &node.RetryBackoffConfig{
					Kind:        graphshared.BackoffExponential,
					BaseDelayMs: 50,
					MaxDelayMs:  200,
				},
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/my_err": {Action: "retry"},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-node-admin", map[string]any{})

	worker := h.FindNode(iid, "worker")
	flaky := h.FindNode(iid, "flaky")
	require.NotNil(t, worker)
	require.NotNil(t, flaky)

	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(flaky.ID, cascade.NodeStateFailed)

	workerDetail := getNodeDetail(t, h, worker.ID)
	require.Equal(t, worker.ID.String(), workerDetail.ID,
		"GET /v1/nodes/{id} must surface the id the operator queried")
	require.NotNil(t, workerDetail.RunSummary,
		"GET /v1/nodes/{id} must surface a run_summary projecting categorical run counts per the no-derived-state model")
	require.GreaterOrEqual(t, workerDetail.RunSummary.FreshCount, 1,
		"settled worker must show at least one fresh run in the run_summary — the categorical evidence of `concept:node-run` success")
	require.Equal(t, 0, workerDetail.RunSummary.FailedCount,
		"settled worker must have zero failed runs in its summary")

	flakyDetail := getNodeDetail(t, h, flaky.ID)
	require.NotNil(t, flakyDetail.RunSummary,
		"GET /v1/nodes/{id} must surface run_summary for the failed-terminal node")
	require.GreaterOrEqual(t, flakyDetail.RunSummary.FailedCount, 1,
		"failed flaky must show at least one failed run in the run_summary — the categorical evidence the policy chain reached terminal failure")

	notFoundResp, err := http.Get(h.ControlBase + "/v1/nodes/00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	notFoundResp.Body.Close()
	require.Equal(t, http.StatusNotFound, notFoundResp.StatusCode,
		"GET /v1/nodes/{unknown} must 404 — silently returning 200 with a stub response is the falsifier")

	freshResetResp, err := http.Post(
		h.ControlBase+"/v1/nodes/"+worker.ID.String()+"/reset",
		"application/json", bytes.NewReader([]byte(`{}`)),
	)
	require.NoError(t, err)
	freshResetResp.Body.Close()
	require.Equalf(t, http.StatusConflict, freshResetResp.StatusCode,
		"POST /v1/nodes/{id}/reset on a non-failed node (state=fresh) must return 409 Conflict; got %d",
		freshResetResp.StatusCode)

	h.Stub.WhenType("flaky").Success(map[string]any{"f": 1}, true, "flaky")

	preResetSuccessCount := countTerminalSuccess(t, h, flaky.ID)
	require.Equal(t, 0, preResetSuccessCount,
		"flaky must have zero successful terminals before reset (only failures so far)")

	resetResp, err := http.Post(
		h.ControlBase+"/v1/nodes/"+flaky.ID.String()+"/reset",
		"application/json", bytes.NewReader([]byte(`{}`)),
	)
	require.NoError(t, err)
	resetResp.Body.Close()
	require.Equal(t, http.StatusOK, resetResp.StatusCode,
		"POST /v1/nodes/{id}/reset on a failed node must return 200")

	h.PostInstanceMessage(iid, "", nil, fmt.Sprintf("test-wake-%s-after-reset", t.Name()))

	waitForTerminalSuccessCountGreaterThan(t, h, flaky.ID, preResetSuccessCount)
	h.WaitForNodeState(flaky.ID, cascade.NodeStateFresh)

	finalDetail := getNodeDetail(t, h, flaky.ID)
	require.NotNil(t, finalDetail.RunSummary,
		"after reset + successful re-fire, run_summary remains observable")
	require.GreaterOrEqual(t, finalDetail.RunSummary.FreshCount, 1,
		"after reset + successful re-fire, run_summary surfaces at least one fresh run")
}

func getNodeDetail(t *testing.T, h *scenario.Harness, nodeID shared.UUID) nodeDetailResponse {
	t.Helper()
	resp, err := http.Get(h.ControlBase + "/v1/nodes/" + nodeID.String())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET /v1/nodes/{id} must return 200")
	var out nodeDetailResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func countTerminalSuccess(t *testing.T, h *scenario.Harness, nodeID shared.UUID) int {
	t.Helper()
	var n int
	h.QueryRowSQL(`
        SELECT count(*) FROM rimsky_events
         WHERE node_id = $1 AND kind = 'terminal/success'
    `, []any{nodeID}, &n)
	return n
}

func waitForTerminalSuccessCountGreaterThan(t *testing.T, h *scenario.Harness, nodeID shared.UUID, baseline int) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("node %s to log more than %d terminal/success events", nodeID, baseline), func() bool {
		return countTerminalSuccess(t, h, nodeID) > baseline
	})
}
