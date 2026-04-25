// Scenario 9 — agentic-style async handoff. Executor returns AsyncAccepted
// with an ack; node stays running. The test then POSTs a Complete body to
// the supervisor's callback endpoint with the same ack; the node reaches
// fresh.
package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestAgenticExecutorAsyncHandoff(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("agent").AsyncAccepted("ack-1", 5000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "async", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "agent", Executor: "stub"},
		},
	})
	iid := h.CreateInstance(tid, "ck-async", map[string]any{})

	n := h.FindNode(iid, "agent")
	require.NotNil(t, n)

	// Wait for node to enter running (supervisor holds pending claim).
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateRunning, 15*time.Second),
		"agent did not reach running")

	// POST callback with matching ackID. Retry briefly so we don't race the
	// supervisor's registerAsync that runs after state→running.
	cbURL := "http://" + h.Supervisor.CallbackAddr() + "/v1/callback/ack-1"
	body, _ := json.Marshal(map[string]any{
		"type":           "complete",
		"result":         map[string]any{"done": true},
		"changed":        true,
		"change_summary": "async-ok",
	})
	deadline := time.Now().Add(10 * time.Second)
	var status int
	for time.Now().Before(deadline) {
		resp, err := http.Post(cbURL, "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		status = resp.StatusCode
		_ = resp.Body.Close()
		if status == http.StatusOK {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.Equal(t, http.StatusOK, status, "callback did not become available")

	// Node reaches fresh via the callback path.
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFresh, 15*time.Second),
		"agent did not reach fresh after async callback")
}
