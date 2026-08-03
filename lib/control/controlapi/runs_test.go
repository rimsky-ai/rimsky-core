// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: node-run

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func seedRunForRunStateTest(t *testing.T, h *mcpParityHarness, admin string) string {
	t.Helper()
	status, out := h.http(t, "POST", "/v1/templates", admin, validTemplateBody("run-state-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)

	status, out = h.http(t, "POST", "/v1/templates/"+tplID+"/deploy", admin, map[string]any{})
	require.Equal(t, http.StatusOK, status, out)

	status, out = h.http(t, "POST", "/v1/instances", admin, map[string]any{
		"template":     tplID,
		"instance_key": "run-state-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instanceID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instanceID)

	status, out = h.http(t, "GET", "/v1/instances/"+instanceID+"/nodes", admin, nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)
	require.NotEmpty(t, nodes, "the created instance must have nodes: %v", out)
	nodeID, _ := nodes[0].(map[string]any)["id"].(string)
	require.NotEmpty(t, nodeID)

	instUUID := shared.UUID(uuid.MustParse(instanceID))
	scopeID := shared.UUID(uuid.New())
	ctx := context.Background()
	require.NoError(t, h.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.db.Tables().RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: scopeID, GraphName: "main", InstanceID: instUUID,
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := h.db.Tables().Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instUUID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		frameID, err := h.db.Tables().Frames().InsertRunningFrame(ctx, instUUID, msgID, scopeID, tx)
		if err != nil {
			return err
		}
		return h.db.Queue().Enqueue(ctx, persistence.DispatchRequest{
			NodeID:         shared.UUID(uuid.MustParse(nodeID)),
			ExecutorName:   "worker",
			EnqueuedAt:     time.Now().UTC(),
			FrameID:        frameID,
			RunScopeID:     scopeID,
			CreationReason: cascade.CreationReasonCascade,
		}, tx)
	}))

	page, err := h.db.Queue().ListLive(ctx, persistence.DispatchListFilter{}, persistence.ListPagination{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, page.Rows, "the seeded run must be readable by the run-state endpoint")
	return page.Rows[0].ID.String()
}

// @decision: node-state-retired-from-operator-api
func TestRunStateEndpointReadsOneRunByID(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)
	runID := seedRunForRunStateTest(t, h, admin)

	status, out := h.http(t, "GET", "/v1/runs/"+runID, admin, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, runID, out["id"])
	require.NotEmpty(t, out["node_id"], "the run's node must be identified: %v", out)
	require.NotEmpty(t, out["frame_id"], "the run's frame must be identified: %v", out)
	require.NotEmpty(t, out["state"], "the run's lifecycle state is the whole point of the endpoint: %v", out)
	require.Contains(t, out, "terminal", "an operator must be able to tell in-flight from terminal without a state lookup table: %v", out)
}

// @decision: node-state-retired-from-operator-api
func TestRunStateEndpointRejectsAMalformedAndUnknownRunID(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)

	status, _ := h.http(t, "GET", "/v1/runs/not-a-uuid", admin, nil)
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = h.http(t, "GET", "/v1/runs/"+uuid.NewString(), admin, nil)
	require.Equal(t, http.StatusNotFound, status)
}

// @decision: node-state-retired-from-operator-api
func TestRunStateEndpointGatesOnItsOwnAction(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)
	runID := seedRunForRunStateTest(t, h, admin)

	nodeReader := h.mintKey(t, "node-reader", `[{"action":"node:read"}]`)
	status, _ := h.http(t, "GET", "/v1/runs/"+runID, nodeReader, nil)
	require.Equal(t, http.StatusForbidden, status,
		"reading a run's state is its own authorization action, not a rider on node:read")

	runReader := h.mintKey(t, "run-reader", `[{"action":"run:read"}]`)
	status, out := h.http(t, "GET", "/v1/runs/"+runID, runReader, nil)
	require.Equal(t, http.StatusOK, status, out)
}

// @decision: mcp-http-parity
// @story: mcp-transport
func TestRunStateIsReachableThroughMCPWithTheSameResult(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)
	runID := seedRunForRunStateTest(t, h, admin)

	status, viaHTTP := h.http(t, "GET", "/v1/runs/"+runID, admin, nil)
	require.Equal(t, http.StatusOK, status, viaHTTP)

	status, out := h.http(t, "POST", "/v1/mcp", admin, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "run_get",
			"arguments": map[string]any{"run_id": runID},
		},
	})
	require.Equal(t, http.StatusOK, status, out)

	result, ok := out["result"].(map[string]any)
	require.True(t, ok, "expected a JSON-RPC result envelope: %v", out)
	content, ok := result["content"].([]any)
	require.True(t, ok, "expected result.content: %v", result)
	require.NotEmpty(t, content)
	text, _ := content[0].(map[string]any)["text"].(string)
	require.NotEmpty(t, text)

	var viaMCP map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &viaMCP))
	require.Equal(t, viaHTTP, viaMCP,
		"the MCP tool re-dispatches through the same route, so both transports must return the same body")
}

// @decision: mcp-http-parity
func TestObservabilityToolBuildsThePathFromItsSuffixArgument(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)

	status, out := h.http(t, "POST", "/v1/mcp", admin, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "observability_get", "arguments": map[string]any{}},
	})
	require.Equal(t, http.StatusOK, status, out)
	rpcErr, ok := out["error"].(map[string]any)
	require.True(t, ok, "invoking the wildcard tool with no path_suffix must be an invalid-params error, got: %v", out)
	require.Contains(t, rpcErr["message"], "path_suffix")
}
