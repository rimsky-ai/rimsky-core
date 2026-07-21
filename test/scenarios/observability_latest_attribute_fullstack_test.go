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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestNodeLatestAttributeBagFullStack(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Success(map[string]any{"value": "first"}, true, "initial")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "latest-attr-bag", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/worker"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/worker", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"value"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-latest-attr", map[string]any{})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)
	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	h.WaitForNodeState(w.ID, cascade.NodeStateFresh)
	firstRow := latestAttrRow(h, w.ID, h.GetLatestFrameRootRunScopeID(iid))
	require.NotNil(t, firstRow, "first run should persist a node_attributes row")
	firstRunID := firstRow.NodeRunID
	require.Equal(t, "first", firstRow.Data["value"])

	h.Stub.WhenType("worker").Success(map[string]any{"value": "second"}, true, "rerun")
	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	var second *persistence.NodeAttributesRow
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		second = latestAttrRow(h, w.ID, h.GetLatestFrameRootRunScopeID(iid))
		if second != nil && second.NodeRunID != firstRunID && second.Data["value"] == "second" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, second, "second run should persist a node_attributes row")
	require.NotEqual(t, firstRunID, second.NodeRunID, "second run should have a new node_run_id")
	require.Equal(t, "second", second.Data["value"],
		"GetLatestByNode must return the most-recent run's bag")
	wantBag := second.Data

	caBody := getJSONMap(t, h.ControlBase+"/v1/nodes/"+w.ID.String())
	caLatest, ok := caBody["latest_attributes"]
	require.True(t, ok,
		"GET /nodes/{id} must carry a latest_attributes key (the most-recent resolved bag)")
	require.Equal(t, wantBag, normalizeBag(t, caLatest),
		"GET /nodes/{id} latest_attributes must equal the second run's GetLatestByNode bag")

	obsBody := getJSONMap(t, h.ControlBase+"/v1/observability/nodes/"+iid.String()+"/worker")
	obsLatest := extractObsLatest(t, obsBody)
	require.NotNil(t, obsLatest,
		"observability node read must carry the most-recent resolved attribute bag")
	require.Equal(t, wantBag, normalizeBag(t, obsLatest),
		"observability latest_attributes must equal the second run's GetLatestByNode bag")

	pausedIID := createPausedInstanceLatestAttr(t, h, tid, "ck-latest-attr-paused")
	pausedW := waitForNodeRow(t, h, pausedIID, "worker")
	var pausedAttrRowCount int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_attributes WHERE node_id = $1`,
		[]any{pausedW.ID}, &pausedAttrRowCount)
	require.Zero(t, pausedAttrRowCount,
		"never-executed node should have no node_attributes row for any scope (paused instance opens no frame, has no root RunScope)")

	caPaused := getJSONMap(t, h.ControlBase+"/v1/nodes/"+pausedW.ID.String())
	requireAbsentOrEmptyBag(t, caPaused["latest_attributes"],
		"GET /nodes/{id} for a never-executed node must omit/empty latest_attributes")

	obsPaused := getJSONMap(t, h.ControlBase+"/v1/observability/nodes/"+pausedIID.String()+"/worker")
	requireAbsentOrEmptyBag(t, obsPaused["latest_attributes"],
		"observability read for a never-executed node must omit/empty latest_attributes")
}

func latestAttrRow(h *scenario.Harness, nodeID, runScopeID shared.UUID) *persistence.NodeAttributesRow {
	var row *persistence.NodeAttributesRow
	require.NoError(h.T, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, nodeID, runScopeID, tx)
		row = r
		return err
	}))
	return row
}

func createPausedInstanceLatestAttr(t *testing.T, h *scenario.Harness, templateHash, consumerKey string) shared.UUID {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"template":     templateHash,
		"params":       map[string]any{},
		"instance_key": consumerKey,
		"paused":       true,
	})
	resp, err := http.Post(h.ControlBase+"/v1/instances", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	id, err := uuid.Parse(out.InstanceID)
	require.NoError(t, err)
	return shared.UUID(id)
}

func waitForNodeRow(t *testing.T, h *scenario.Harness, instanceID shared.UUID, nodeType string) *persistence.NodeRow {
	t.Helper()
	for {
		if n := h.FindNode(instanceID, nodeType); n != nil {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func getJSONMap(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoErrorf(t, err, "GET %s: read body", url)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s: body=%s", url, string(raw))
	var out map[string]any
	require.NoErrorf(t, json.Unmarshal(raw, &out), "GET %s: body=%s", url, string(raw))
	return out
}

func extractObsLatest(t *testing.T, body map[string]any) any {
	t.Helper()
	if v, ok := body["latest_attributes"]; ok && !isEmptyBag(v) {
		return v
	}
	if nodeObj, ok := body["node"].(map[string]any); ok {
		if v, ok := nodeObj["latest_attributes"]; ok && !isEmptyBag(v) {
			return v
		}
	}
	return nil
}

func normalizeBag(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	require.Truef(t, ok, "latest_attributes should decode to an object, got %T", v)
	return m
}

func requireAbsentOrEmptyBag(t *testing.T, v any, msg string) {
	t.Helper()
	require.Truef(t, isEmptyBag(v), "%s: got %#v", msg, v)
}

func isEmptyBag(v any) bool {
	if v == nil {
		return true
	}
	m, ok := v.(map[string]any)
	return ok && len(m) == 0
}
