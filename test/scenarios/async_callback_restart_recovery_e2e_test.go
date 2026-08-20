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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @decision: async-callback-persistent-registry
func TestAsyncCallback_SurvivesRegistryLoss_ViaProductionRegisteredAck(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	const ackID = "ack-guard-restart-recovery"
	h.Stub.WhenType("agent").AwaitAsyncCallback(ackID, 5000)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "async-callback-restart-recovery", Version: "1",
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
	iid := h.CreateInstance(tid, "ck-async-callback-restart-recovery", map[string]any{})

	n := h.FindNode(iid, "agent")
	require.NotNil(t, n)

	h.WaitForNodeState(n.ID, cascade.NodeStateRunning)

	var registeredAckID string
	waitForAsyncAckRegistered(t, h, n.ID, &registeredAckID)
	require.Equal(t, ackID, registeredAckID,
		"the production dispatch path (runner_dispatch.go's registerAsyncIfSet+RegisterAsyncAck, "+
			"issued in the same transaction as the transient/await_async signal) must have persisted "+
			"the async_ack_id on rimsky_node_runs — this is the row a restarted supervisor's callback "+
			"handler falls back to when its in-memory registry is empty")

	_, evicted := h.Supervisor.CallbackRegistry().Pop(ackID)
	require.True(t, evicted,
		"the in-memory CallbackRegistry must have held this ack (populated by the same production "+
			"dispatch call) before we evict it to simulate the registry state a fresh supervisor "+
			"process starts with after a restart")

	cbURL := "http://" + h.Supervisor.CallbackAddr() + "/v1/callback/" + ackID
	body, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"done": true},
			"changed":          true,
			"change_summary":   "async-ok-post-registry-loss",
		},
	})
	var respBody []byte
	awaited.Until(t, "the callback endpoint to honour the ack after the in-memory registry entry is gone, "+
		"by falling back to lookupAsyncCtxByAck's DB-backed reconstruction", func() bool {
		resp, err := http.Post(cbURL, "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		status := resp.StatusCode
		respBody, _ = readAllAndClose(resp)
		return status == http.StatusOK
	})

	var ack struct {
		AckStatus string `json:"ack_status"`
	}
	require.NoError(t, json.Unmarshal(respBody, &ack))
	require.Equal(t, "accepted", ack.AckStatus)

	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, n.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "expected node_attributes row to exist after registry-loss recovery commit")
	require.Equal(t, true, row.Data["done"],
		"the reconstructed acquisition must correctly thread the callback's attributes_delta through "+
			"to the committed attributes, proving lookupAsyncCtxByAck rebuilt a working acquisition "+
			"from persisted state alone")
}

func waitForAsyncAckRegistered(t *testing.T, h *scenario.Harness, nodeID shared.UUID, out *string) {
	t.Helper()
	awaited.Until(t, "a node run for "+nodeID.String()+" to carry an async_ack_id", func() bool {
		var count int
		h.QueryRowSQL(`
			SELECT count(*) FROM rimsky_node_runs
			 WHERE node_id = $1 AND async_ack_id IS NOT NULL`,
			[]any{nodeID}, &count)
		if count == 0 {
			return false
		}
		var ackID string
		h.QueryRowSQL(`
			SELECT async_ack_id FROM rimsky_node_runs
			 WHERE node_id = $1 AND async_ack_id IS NOT NULL
			 ORDER BY enqueued_at DESC LIMIT 1`,
			[]any{nodeID}, &ackID)
		*out = ackID
		return true
	})
}

func readAllAndClose(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}
