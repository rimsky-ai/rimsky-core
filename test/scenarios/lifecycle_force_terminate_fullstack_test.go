// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Full-stack force-terminate of an await-async-stuck instance
// (spec S-lifecycle-fullstack-terminate-backfill, facet 1).
//
// An agent node returns AwaitAsyncCallback with an ack and stays running
// because the callback never arrives. The operator force-terminates the
// instance through the REAL running stack (scheduler + supervisor +
// control-api over a testcontainers Postgres) and rescues it:
//
//   - the node's running run-row force-fails to state=failed with the
//     canonical settling signal terminal/error/instance_killed,
//   - the instance's terminated_at is set,
//   - the instance's main run-scope closes, and
//   - a subsequent DELETE succeeds (the terminal guard now passes).
//
// This is the full-stack proof the spec demands: the running run-row is
// produced by the REAL dispatch path (the supervisor claims the node,
// dispatches it to the stub executor, registers the async ack, and leaves
// the node running awaiting a callback we deliberately never POST), NOT a
// hand-INSERTed running row. It supersedes the handler-altitude unit proof
// in lib/control/controlapi/instances_test.go that seeded the running row
// via raw SQL.
//
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

	// Script the agent node to return AwaitAsyncCallback with an ack and a
	// 60s completion window. We NEVER POST the callback, so the node stays
	// running (the completion window is long enough that nothing fires it
	// before the test force-terminates).
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

	// The node reaches running through the REAL dispatch path (supervisor
	// claims it, dispatches to the stub, registers the async ack) and stays
	// there — we never deliver the callback to h.Supervisor.CallbackAddr().
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateRunning, 15*time.Second),
		"agent did not reach running (await-async-stuck precondition)")

	// Force-terminate through the live control-api. Anonymous-mode gates
	// pass (no api-key), per existing scenarios (cascade_invalidate_test.go).
	resp, err := http.Post(h.ControlBase+"/v1/instances/"+iid.String()+"/terminate",
		"application/json", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"terminate must return 200 against the live control-api")

	// (a) The node's projected state is failed (the real persistence
	// projection over its now-terminal run row).
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFailed, 15*time.Second),
		"running node-run must be force-failed to failed by terminate")

	// (b) The failed run-row carries the canonical settling signal
	// terminal/error/instance_killed. Read it straight out of
	// rimsky_node_runs for the node's failed run (single-dispatch scenario:
	// exactly one failed run row exists for this node).
	var settling string
	h.QueryRowSQL(
		`SELECT settling_signal_type FROM rimsky_node_runs
		   WHERE node_id = $1 AND state = 'failed'
		   ORDER BY enqueued_at DESC LIMIT 1`,
		[]any{n.ID}, &settling)
	require.Equal(t, "terminal/error/instance_killed", settling,
		"force-failed run must carry the instance_killed settling signal")

	// (c) terminated_at is set on the instance.
	var terminatedAt *time.Time
	h.QueryRowSQL(
		`SELECT terminated_at FROM rimsky_instances WHERE id = $1`,
		[]any{iid}, &terminatedAt)
	require.NotNil(t, terminatedAt,
		"terminate must set terminated_at on the instance")

	// (d) The instance's main run-scope is closed.
	mainScope := h.GetMainRunScopeID(iid)
	var scopeClosedAt *time.Time
	h.QueryRowSQL(
		`SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`,
		[]any{mainScope}, &scopeClosedAt)
	require.NotNil(t, scopeClosedAt,
		"terminate must close the instance's main run-scope")

	// (e) A subsequent DELETE succeeds now that the terminal guard passes,
	// returning 200 {"deleted":true}.
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
