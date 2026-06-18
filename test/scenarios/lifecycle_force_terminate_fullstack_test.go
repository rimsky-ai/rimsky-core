// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance
package scenarios

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestForceTerminateAwaitAsyncStuckFullStack(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("agent").AwaitAsyncCallback("ack-stuck", 60000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "stuck-async", Version: "1",
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
	iid := h.CreateInstance(tid, "ck-stuck", map[string]any{})

	n := h.FindNode(iid, "agent")
	require.NotNil(t, n)

	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateRunning, 15*time.Second),
		"agent did not reach running (await-async-stuck precondition)")

	resp, err := http.Post(h.ControlBase+"/v1/instances/"+iid.String()+"/terminate",
		"application/json", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"terminate must return 200 against the live control-api")

	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFailed, 15*time.Second),
		"running node-run must be force-failed to failed by terminate")

	var settling string
	h.QueryRowSQL(
		`SELECT settling_signal_type FROM rimsky_node_runs
		   WHERE node_id = $1 AND state = 'failed'
		   ORDER BY enqueued_at DESC LIMIT 1`,
		[]any{n.ID}, &settling)
	require.Equal(t, "terminal/error/instance_killed", settling,
		"force-failed run must carry the instance_killed settling signal")

	var terminatedAt *time.Time
	h.QueryRowSQL(
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`,
		[]any{iid}, &terminatedAt)
	require.NotNil(t, terminatedAt,
		"terminate must set terminated_at on the instance")

	mainScope := h.GetMainRunScopeID(iid)
	var scopeClosedAt *time.Time
	h.QueryRowSQL(
		`SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`,
		[]any{mainScope}, &scopeClosedAt)
	require.NotNil(t, scopeClosedAt,
		"terminate must close the instance's main run-scope")

	delReq, err := http.NewRequest(http.MethodDelete,
		h.ControlBase+"/v1/instances/"+iid.String(), nil)
	require.NoError(t, err)
	delResp, err := http.DefaultClient.Do(delReq)
	require.NoError(t, err)
	delBody := new(bytes.Buffer)
	_, _ = delBody.ReadFrom(delResp.Body)
	_ = delResp.Body.Close()
	require.Equal(t, http.StatusOK, delResp.StatusCode,
		"DELETE must succeed after terminate: %s", delBody.String())
	require.Contains(t, delBody.String(), `"deleted":true`,
		"DELETE response must report deleted:true")
}
