// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: instance
package scenarios

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestForceTerminate_MultiNodeKillAuditsAsOneInstanceTerminatedEvent(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("agent-a").AwaitAsyncCallback("ack-a", 60000)
	h.Stub.WhenType("agent-b").AwaitAsyncCallback("ack-b", 60000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "multi-node-force-terminate", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "agent-a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "agent-b", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-multi-node-force-terminate", map[string]any{})

	a := h.FindNode(iid, "agent-a")
	b := h.FindNode(iid, "agent-b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.WaitForNodeState(a.ID, cascade.NodeStateRunning)
	h.WaitForNodeState(b.ID, cascade.NodeStateRunning)

	resp, err := http.Post(h.ControlBase+"/v1/instances/"+iid.String()+"/terminate",
		"application/json", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"terminate must return 200 against the live control-api")

	h.WaitForNodeState(a.ID, cascade.NodeStateFailed)
	h.WaitForNodeState(b.ID, cascade.NodeStateFailed)

	var terminatedEventCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_events WHERE instance_id = $1 AND kind = 'instance_terminated'`,
		[]any{iid}, &terminatedEventCount)
	require.Equal(t, 1, terminatedEventCount,
		"a multi-node force-terminate must audit as exactly one instance_terminated event, "+
			"not one per killed node-run")

	var killedKindCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_events WHERE instance_id = $1 AND kind = 'instance_killed'`,
		[]any{iid}, &killedKindCount)
	require.Equal(t, 0, killedKindCount,
		"instance_killed is a node-run transition reason, never an event-log kind; "+
			"the audit trail must carry only instance_terminated")
}
