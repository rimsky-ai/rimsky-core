// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

type instanceProjection struct {
	ID                string     `json:"id"`
	TerminateAfterRun bool       `json:"terminate_after_run"`
	TerminatedAt      *time.Time `json:"terminated_at,omitempty"`
}

func TestAsyncCallbackHoldsFrame_EndToEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").AwaitAsyncCallback("ack-async-holds", 60_000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "async-callback-holds-frame", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})

	iid := createInstanceTerminateAfterRun(t, h, tid)

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateRunning, 30*time.Second),
		"worker should reach running with the async ack outstanding")

	require.Nil(t, getInstance(t, h, iid).TerminatedAt,
		"instance must NOT be terminated while async work is outstanding, even with terminate_after_run set")

	cbURL := "http://" + h.Supervisor.CallbackAddr() + "/v1/callback/ack-async-holds"
	cbBody, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{},
			"changed":          true,
			"change_summary":   "after-callback",
		},
	})
	cbDeadline := time.Now().Add(10 * time.Second)
	var cbStatus int
	for time.Now().Before(cbDeadline) {
		resp, err := http.Post(cbURL, "application/json", bytes.NewReader(cbBody))
		require.NoError(t, err)
		cbStatus = resp.StatusCode
		_ = resp.Body.Close()
		if cbStatus == http.StatusOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, http.StatusOK, cbStatus, "supervisor callback did not become available")

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should resolve to Success after the async callback")

	require.True(t, waitForInstanceTerminated(t, h, iid, 30*time.Second),
		"instance must terminate only after the async work resolves and the frame ends (terminate_after_run)")
}

func createInstanceTerminateAfterRun(t *testing.T, h *scenario.Harness, templateHash string) shared.UUID {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"template":            templateHash,
		"params":              map[string]any{},
		"terminate_after_run": true,
	})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/instances", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("create instance: status %d: %s", resp.StatusCode, string(buf))
	}
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	id, err := uuid.Parse(out.InstanceID)
	require.NoError(t, err)

	require.True(t, getInstance(t, h, id).TerminateAfterRun,
		"created instance should report terminate_after_run=true")

	// @decision: empty-message-as-root-trigger
	h.PostInstanceMessage(id, "", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs d
		               JOIN rimsky_nodes n ON n.id = d.node_id
		               WHERE n.instance_id = $1`, []any{id}, &count)
		if count > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return id
}

func getInstance(t *testing.T, h *scenario.Harness, id shared.UUID) instanceProjection {
	t.Helper()
	resp, err := http.Get(h.ControlBase + "/v1/instances/" + id.String())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET /instances/{id} should return 200")
	var item instanceProjection
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&item))
	return item
}

func waitForInstanceTerminated(t *testing.T, h *scenario.Harness, id shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if getInstance(t, h, id).TerminatedAt != nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
