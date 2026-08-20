// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const healthProbeSupervisorID = "scenario-supervisor"

func TestHealthCheck_ActiveNodeCountComputedOnDemand(t *testing.T) {
	t.Parallel()

	h := scenario.Start(t, scenario.HarnessOpts{})

	const ackID = "ack-health-active-count"
	h.Stub.WhenType("agent").AwaitAsyncCallback(ackID, 5000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "health-active-node-count", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "agent", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"done": map[string]any{"type": "boolean"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-health-active-node-count", map[string]any{})

	n := h.FindNode(iid, "agent")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateRunning)

	require.GreaterOrEqual(t, activeNodeCountFor(t, h, healthProbeSupervisorID), 1,
		"while the 'agent' dispatch is genuinely running, /v1/health's active_node_count for its "+
			"supervisor must reflect it — a cached/stale counter (e.g. one only bumped at claim time "+
			"and never decremented) could still show a stale value here, so this alone isn't "+
			"conclusive; the settle-then-recheck below is what proves 'on demand'")

	cbURL := "http://" + h.Supervisor.CallbackAddr() + "/v1/callback/" + ackID
	body, _ := json.Marshal(map[string]any{
		"success": map[string]any{"attributes_delta": map[string]any{"done": true}, "changed": true},
	})
	awaited.Until(t, "the supervisor's async-callback endpoint to accept the success body", func() bool {
		resp, err := http.Post(cbURL, "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		status := resp.StatusCode
		_ = resp.Body.Close()
		return status == http.StatusOK
	})
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	awaited.Until(t, "the health probe's active node count to fall back to zero", func() bool {
		return activeNodeCountFor(t, h, healthProbeSupervisorID) == 0
	})
}

func activeNodeCountFor(t *testing.T, h *scenario.Harness, supervisorID string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.ControlBase+"/v1/health", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Supervisors []struct {
			ID              string `json:"id"`
			ActiveNodeCount int    `json:"active_node_count"`
		} `json:"supervisors"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	for _, s := range out.Supervisors {
		if s.ID == supervisorID {
			return s.ActiveNodeCount
		}
	}
	t.Fatalf("/v1/health response carried no supervisor entry for %q; got %+v", supervisorID, out.Supervisors)
	return -1
}
