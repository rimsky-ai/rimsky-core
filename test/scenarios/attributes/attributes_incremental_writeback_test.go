// Spec §19.1 — incremental attributes writeback callback (spec §12.5).
//
// The executor returns AsyncAccepted to pin the node in `running`; while
// it's there, the test POSTs two `{"delta": {...}}` bodies to
// `/v1/attributes/{node_id}` on the supervisor's callback listener. Each
// call merges its delta into rimsky_node_attributes.data via the SHALLOW
// `data || $1::jsonb` semantics. The test then resolves the async with
// a Complete and asserts the merged data contains the cumulative writes.
//
// Auth uses the supervisor-issued cancel_token (recorded by the stub in
// ObservedRequest.CancelToken) per spec §12.5.
package attributes

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

func TestAttributesIncrementalWriteback(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// AsyncAccepted pins the node in `running` so the test can issue
	// per-field writeback callbacks before the terminal arrives.
	h.Stub.WhenType("worker").AsyncAccepted("ack-incremental", 30000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-incremental", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"first":  map[string]any{"type": "string"},
						"second": map[string]any{"type": "integer"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-incremental", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateRunning, 15*time.Second),
		"worker did not reach running")

	// Pull cancel_token + callback_url from the stub's observation.
	var token, callbackBase string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, obs := range h.Stub.Observed() {
			if obs.NodeID == worker.ID.String() {
				token = obs.CancelToken
				callbackBase = obs.CallbackURL
				break
			}
		}
		if token != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotEmpty(t, token, "stub did not observe cancel_token for worker")
	require.NotEmpty(t, callbackBase, "stub did not observe callback_url for worker")

	// Two incremental POSTs accumulate via the SHALLOW JSONB merge.
	postDelta(t, callbackBase, worker.ID, token, map[string]any{"first": "alpha"})
	postDelta(t, callbackBase, worker.ID, token, map[string]any{"second": 42})

	// Resolve the async so the node reaches fresh.
	completeAck(t, h.Supervisor.CallbackAddr(), "ack-incremental")
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh after async callback")

	// Both incremental writes must be present in the final attributes.data.
	row, err := h.Storage.NodeAttributes().Get(h.Ctx, worker.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "alpha", row.Data["first"], "first incremental delta missing")
	// JSON decode lifts numbers to float64.
	require.Equal(t, float64(42), row.Data["second"], "second incremental delta missing")
}

// postDelta POSTs a single `{"delta": {...}}` body to the
// `/v1/attributes/{node_id}` endpoint. Fails the test on non-204 status.
// The base URL is the supervisor's advertised callback URL (recorded by
// the stub in ObservedRequest.CallbackURL).
func postDelta(t *testing.T, callbackBase string, nodeID shared.UUID, token string, delta map[string]any) {
	t.Helper()
	url := callbackBase + "/v1/attributes/" + nodeID.String()
	body, _ := json.Marshal(map[string]any{"delta": delta})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode,
		"expected 204 from /v1/attributes; got %d", resp.StatusCode)
}

// completeAck POSTs a Complete terminal event to the supervisor's async-
// callback endpoint. Mirrors the helper in the stores scenarios.
func completeAck(t *testing.T, callbackAddr, ackID string) {
	t.Helper()
	cbURL := "http://" + callbackAddr + "/v1/callback/" + ackID
	body, _ := json.Marshal(map[string]any{
		"type":           "complete",
		"changed":        true,
		"change_summary": "ok",
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Post(cbURL, "application/json", bytes.NewReader(body))
		if err == nil {
			status := resp.StatusCode
			_ = resp.Body.Close()
			if status == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("callback %s did not return 200 within deadline", cbURL)
}
