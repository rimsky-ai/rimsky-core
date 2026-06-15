// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Full-stack scenario for S-observability-forensic-last-attribute: a
// node's most-recent resolved attribute bag (its forensic last-attribute
// snapshot) must surface on BOTH node-read surfaces — the control-api
// `GET /nodes/{id}` detail and the observability
// `GET /v1/observability/nodes/{instance_id}/{node_type}` read — served
// by the real `NodeAttributes().GetLatestByNode` primitive end to end.
//
// RED phase (Pass SENSLIFEOBS-9): neither surface emits the bag today, so
// this test FAILS — the `latest_attributes` key is absent. Pass
// SENSLIFEOBS-10 adds it to both surfaces and flips this green. The test
// is deliberately coupled to the missing behavior: every assertion reads
// the value the live `GetLatestByNode(nodeID, MainRunScopeID)` primitive
// returns, never a hardcoded literal, so it can only pass once the real
// primitive is wired through both handlers.
package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestNodeLatestAttributeBagFullStack drives a single-worker node across
// two runs with DIFFERENT attribute deltas, then asserts both node-read
// surfaces carry the SECOND (most-recent) run's resolved bag, equal to
// what `NodeAttributes().GetLatestByNode` returns. A separate paused
// instance proves a never-executed node yields an absent/empty
// `latest_attributes` (no panic on the nil row).
func TestNodeLatestAttributeBagFullStack(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Success(map[string]any{"value": "first"}, true, "initial")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "latest-attr-bag", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string"},
					},
					"required": []any{"value"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-latest-attr", map[string]any{})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)

	require.True(t, h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh on first run")
	firstRow := latestAttrRow(h, w.ID, h.GetMainRunScopeID(iid))
	require.NotNil(t, firstRow, "first run should persist a node_attributes row")
	firstRunID := firstRow.NodeRunID
	require.Equal(t, "first", firstRow.Data["value"])

	// @constraint: Re-prime the stub and invalidate so the node re-runs with a
	// DIFFERENT delta value — satisfies the "most-recent of two runs"
	// clause: the latest bag must differ from the first.
	h.Stub.WhenType("worker").Success(map[string]any{"value": "second"}, true, "rerun")
	adminInvalidateLatestAttr(t, h, iid, w.ID)

	// @deliberate: Wait until the live primitive reports the SECOND run's bag (new run
	// id + new value). This is the canonical value both surfaces must echo.
	var second *persistence.NodeAttributesRow
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		second = latestAttrRow(h, w.ID, h.GetMainRunScopeID(iid))
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

	// @constraint: (3) control-api GET /nodes/{id} must carry latest_attributes equal to
	// the SECOND run's resolved bag (the GetLatestByNode value).
	caBody := getJSONMap(t, h.ControlBase+"/v1/nodes/"+w.ID.String())
	caLatest, ok := caBody["latest_attributes"]
	require.True(t, ok,
		"GET /nodes/{id} must carry a latest_attributes key (the most-recent resolved bag)")
	require.Equal(t, wantBag, normalizeBag(t, caLatest),
		"GET /nodes/{id} latest_attributes must equal the second run's GetLatestByNode bag")

	// @constraint: (4) observability GET /v1/observability/nodes/{instance_id}/{node_type}
	// must carry the same most-recent bag (today it returns only
	// {node,events,holdings}).
	obsBody := getJSONMap(t, h.ControlBase+"/v1/observability/nodes/"+iid.String()+"/worker")
	obsLatest := extractObsLatest(t, obsBody)
	require.NotNil(t, obsLatest,
		"observability node read must carry the most-recent resolved attribute bag")
	require.Equal(t, wantBag, normalizeBag(t, obsLatest),
		"observability latest_attributes must equal the second run's GetLatestByNode bag")

	// @constraint: (5) A node that has NEVER executed must yield an absent/empty
	// latest_attributes — no panic on the nil GetLatestByNode row. A paused
	// instance never dispatches its worker until resumed, so its node row
	// has no persisted attribute bag.
	pausedIID := createPausedInstanceLatestAttr(t, h, tid, "ck-latest-attr-paused")
	pausedW := waitForNodeRow(t, h, pausedIID, "worker", 10*time.Second)
	require.Nil(t, latestAttrRow(h, pausedW.ID, h.GetMainRunScopeID(pausedIID)),
		"never-executed node should have no node_attributes row")

	caPaused := getJSONMap(t, h.ControlBase+"/v1/nodes/"+pausedW.ID.String())
	requireAbsentOrEmptyBag(t, caPaused["latest_attributes"],
		"GET /nodes/{id} for a never-executed node must omit/empty latest_attributes")

	obsPaused := getJSONMap(t, h.ControlBase+"/v1/observability/nodes/"+pausedIID.String()+"/worker")
	requireAbsentOrEmptyBag(t, obsPaused["latest_attributes"],
		"observability read for a never-executed node must omit/empty latest_attributes")
}

// latestAttrRow reads the live GetLatestByNode primitive — the canonical
// value the surfaces must echo.
func latestAttrRow(h *scenario.Harness, nodeID, runScopeID shared.UUID) *persistence.NodeAttributesRow {
	var row *persistence.NodeAttributesRow
	require.NoError(h.T, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, nodeID, runScopeID, tx)
		row = r
		return err
	}))
	return row
}

// adminInvalidateLatestAttr POSTs an admin invalidate to drive a re-run.
func adminInvalidateLatestAttr(t *testing.T, h *scenario.Harness, instanceID, nodeID shared.UUID) {
	t.Helper()
	resp, err := http.Post(
		h.ControlBase+"/v1/admin/instances/"+instanceID.String()+"/nodes/"+nodeID.String()+"/invalidate",
		"application/json", nil,
	)
	require.NoError(t, err)
	resp.Body.Close()
}

// createPausedInstanceLatestAttr POSTs /instances with paused:true so the
// node never dispatches — a never-executed node for the nil-row assertion.
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

// waitForNodeRow polls until the named node row exists for the instance.
func waitForNodeRow(t *testing.T, h *scenario.Harness, instanceID shared.UUID, nodeType string, timeout time.Duration) *persistence.NodeRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n := h.FindNode(instanceID, nodeType); n != nil {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("node %q never materialized for instance %s", nodeType, instanceID)
	return nil
}

// getJSONMap GETs a URL and decodes the 200 body into a generic map.
func getJSONMap(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s: body=%s", url, string(raw))
	var out map[string]any
	require.NoErrorf(t, json.Unmarshal(raw, &out), "GET %s: body=%s", url, string(raw))
	return out
}

// extractObsLatest reads the latest-attribute bag from the observability
// node read, accepting either a top-level `latest_attributes` sibling key
// (the plan's preferred shape) or one nested under the `node` object.
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

// normalizeBag coerces a decoded JSON value into a map[string]any for
// equality against the persisted Data map.
func normalizeBag(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	require.Truef(t, ok, "latest_attributes should decode to an object, got %T", v)
	return m
}

// requireAbsentOrEmptyBag asserts a never-executed node's latest_attributes
// is absent (nil) or an empty object — never populated.
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
