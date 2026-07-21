// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: debug-channel
// @concept: breakpoint
package scenarios

import (
	"bytes"
	"context"
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
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStoryDebugChannel_GateAndOverrideAcrossRealStack(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ran")

	tpl := node.TemplateSpec{
		Name: "story-debug-channel", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tag": map[string]any{"type": "string"},
						"ok":  map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	}
	tid := h.DeployTemplate(tpl)

	iidHealthy := h.CreateInstance(tid, "ck-debug-healthy", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iidHealthy)

	workerHealthy := h.FindNode(iidHealthy, "worker")
	require.NotNil(t, workerHealthy)
	h.WaitForNodeState(workerHealthy.ID, cascade.NodeStateFresh)

	respHealthy := postDebugOverride(t, h.ControlBase, iidHealthy, map[string]any{
		"action":    "invalidate_node",
		"node_type": "worker",
	}, "")
	require.Equal(t, http.StatusConflict, respHealthy.status,
		"healthy instance must refuse the override with HTTP 409; body: %s",
		string(respHealthy.raw))
	var healthyBody struct {
		Error  string   `json:"error"`
		States []string `json:"states"`
	}
	require.NoError(t, json.Unmarshal(respHealthy.raw, &healthyBody))
	require.Contains(t, healthyBody.Error, "not in debuggable state",
		"the 409 body must name the gate predicate so the operator sees what would unlock the override")
	require.ElementsMatch(t, []string{"paused", "breakpoint"}, healthyBody.States,
		"the 409 body must list BOTH legal predicates, not just one")

	require.Equal(t, 0, countDebugOverrideAuditRows(t, h, iidHealthy),
		"healthy-instance refusal must not leave an audit row")

	iidPaused := createInstanceWithPaused(t, h.ControlBase, tid, "ck-debug-paused")
	require.NotEqual(t, shared.UUID{}, iidPaused)

	bpIDPaused := installPauseModeBreakpoint(t, h.ControlBase, iidPaused, "worker")

	resumeResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", iidPaused.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, resumeResp.status,
		"create-time pause must release cleanly; body=%s", string(resumeResp.raw))

	h.PostInstanceMessage(iidPaused, "", nil, fmt.Sprintf("test-wake-%s-paused-init", t.Name()))

	hitPaused := waitForFirstHit(t, h, bpIDPaused)
	require.Equal(t, "before_dispatch", string(hitPaused.Checkpoint),
		"the parked dispatch's hit must record the before_dispatch checkpoint")

	pauseResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/pause", iidPaused.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, pauseResp.status,
		"re-pause via /pause must succeed; body=%s", string(pauseResp.raw))

	workerPaused := h.FindNode(iidPaused, "worker")
	require.NotNil(t, workerPaused, "worker node row must exist on the paused instance")
	preMutateRunID := getLatestRunID(t, h, workerPaused.ID)
	require.NotNil(t, preMutateRunID,
		"the parked breakpoint hit must leave an in-flight run row for the worker — "+
			"the invalidate_node mutation arm needs one to stale-mark")

	respPaused := postDebugOverride(t, h.ControlBase, iidPaused, map[string]any{
		"action":    "invalidate_node",
		"node_type": "worker",
	}, "")
	require.Equal(t, http.StatusOK, respPaused.status,
		"paused instance must accept the override with HTTP 200; body=%s",
		string(respPaused.raw))
	var pausedResp struct {
		OK          bool   `json:"ok"`
		GateState   string `json:"gate_state"`
		RunsMutated int    `json:"runs_mutated"`
	}
	require.NoError(t, json.Unmarshal(respPaused.raw, &pausedResp))
	require.True(t, pausedResp.OK)
	require.Equal(t, "paused", pausedResp.GateState,
		"the gate must report `paused` when the /pause flag is on")
	require.GreaterOrEqual(t, pausedResp.RunsMutated, 1,
		"the override must report at least one mutated run — falsifier-side: "+
			"a 200 with runs_mutated=0 means the handler returned a status struct "+
			"without actually mutating the graph")

	// @decision: non-cascade-direct-to-stale
	var newStaleCount int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND state = 'stale' AND creation_reason = 'operator_invalidate'`,
		[]any{workerPaused.ID}, &newStaleCount)
	require.GreaterOrEqual(t, newStaleCount, 1,
		"invalidate_node must create at least one new stale row with creation_reason=operator_invalidate")

	require.Equal(t, 1, countDebugOverrideAuditRows(t, h, iidPaused),
		"a successful override must leave exactly one audit row of kind debug.override.applied")
	auditPaused := readLatestDebugOverrideAudit(t, h, iidPaused)
	require.Equal(t, "invalidate_node", auditPaused["action"])
	require.Equal(t, "worker", auditPaused["node_type"])
	require.Equal(t, "paused", auditPaused["gate_state"])

	observedBeforeResume := stubWorkerCount(h)
	resumeResp2 := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", iidPaused.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, resumeResp2.status,
		"resume after override must succeed; body=%s", string(resumeResp2.raw))
	deleteBreakpoint(t, h.ControlBase, iidPaused, bpIDPaused)

	waitForStubWorkerCount(h, observedBeforeResume+1)

	iidBP := createInstanceWithPaused(t, h.ControlBase, tid, "ck-debug-breakpoint")
	require.NotEqual(t, shared.UUID{}, iidBP)
	bpIDBP := installPauseModeBreakpoint(t, h.ControlBase, iidBP, "worker")
	resumeBPResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", iidBP.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, resumeBPResp.status,
		"create-time pause must release cleanly; body=%s", string(resumeBPResp.raw))
	h.PostInstanceMessage(iidBP, "", nil, fmt.Sprintf("test-wake-%s-bp-init", t.Name()))
	hitBP := waitForFirstHit(t, h, bpIDBP)
	require.Equal(t, "before_dispatch", string(hitBP.Checkpoint))

	workerBP := h.FindNode(iidBP, "worker")
	require.NotNil(t, workerBP)
	preBPRunID := getLatestRunID(t, h, workerBP.ID)
	require.NotNil(t, preBPRunID,
		"the parked breakpoint hit must leave an in-flight run row for the worker — "+
			"the set_attribute mutation arm merges into its attribute row")

	const operatorAttrKey = "tag"
	const operatorAttrValue = "operator-debug-override-value"
	respBP := postDebugOverride(t, h.ControlBase, iidBP, map[string]any{
		"action":          "set_attribute",
		"node_type":       "worker",
		"attribute_key":   operatorAttrKey,
		"attribute_value": operatorAttrValue,
	}, "")
	require.Equal(t, http.StatusOK, respBP.status,
		"breakpoint-stopped instance must accept the override with HTTP 200; body=%s",
		string(respBP.raw))
	var bpResp struct {
		OK          bool   `json:"ok"`
		GateState   string `json:"gate_state"`
		RunsMutated int    `json:"runs_mutated"`
	}
	require.NoError(t, json.Unmarshal(respBP.raw, &bpResp))
	require.True(t, bpResp.OK)
	require.Equal(t, "breakpoint", bpResp.GateState,
		"the gate must report `breakpoint` when only the unresumed-hit predicate holds")
	require.GreaterOrEqual(t, bpResp.RunsMutated, 1,
		"the override must report at least one mutated run — falsifier-side: "+
			"a 200 with runs_mutated=0 means the handler returned a status struct "+
			"without actually mutating the graph")

	require.Equal(t, operatorAttrValue,
		getRunAttributeValue(t, h, *preBPRunID, operatorAttrKey),
		"set_attribute must merge the operator key/value into the in-flight run's attribute row")

	require.Equal(t, 1, countDebugOverrideAuditRows(t, h, iidBP),
		"a successful override must leave exactly one audit row of kind debug.override.applied")
	auditBP := readLatestDebugOverrideAudit(t, h, iidBP)
	require.Equal(t, "set_attribute", auditBP["action"])
	require.Equal(t, "worker", auditBP["node_type"])
	require.Equal(t, "breakpoint", auditBP["gate_state"])
	require.Equal(t, operatorAttrKey, auditBP["attribute_key"])
	require.Equal(t, operatorAttrValue, auditBP["attribute_value"])

	preStubCount := stubWorkerCount(h)
	deleteBreakpoint(t, h.ControlBase, iidBP, bpIDBP)
	waitForStubWorkerCount(h, preStubCount+1)

	adminKey := mintAnonymousAdminKey(t, h.ControlBase)
	restrictedKey := mintRestrictedKey(t, h.ControlBase, adminKey)

	respDenied := postDebugOverride(t, h.ControlBase, iidBP, map[string]any{
		"action":    "invalidate_node",
		"node_type": "worker",
	}, restrictedKey)
	require.Equal(t, http.StatusForbidden, respDenied.status,
		"a key without `instance:debug-override` must receive HTTP 403 from the auth gate; "+
			"body=%s", string(respDenied.raw))
	require.Equal(t, 1, countDebugOverrideAuditRows(t, h, iidBP),
		"403 from the auth gate must not write a debug.override.applied row — "+
			"the row in the handler counts actual mutations only")
}

func postDebugOverride(t *testing.T, controlBase string, instanceID shared.UUID, body map[string]any, bearerKey string) httpResp {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/v1/instances/%s/debug/override", controlBase, instanceID),
		bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if bearerKey != "" {
		req.Header.Set("Authorization", "Bearer "+bearerKey)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, raw: out}
}

func createInstanceWithPaused(t *testing.T, controlBase, templateHash, instanceKey string) shared.UUID {
	t.Helper()
	body := map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"params":       map[string]any{},
		"paused":       true,
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(controlBase+"/v1/instances", "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"createInstanceWithPaused: status=%d body=%s", resp.StatusCode, string(out))
	var decoded struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded))
	id, err := uuid.Parse(decoded.InstanceID)
	require.NoError(t, err)
	return shared.UUID(id)
}

func installPauseModeBreakpoint(t *testing.T, controlBase string, instanceID shared.UUID, nodeType string) shared.UUID {
	t.Helper()
	body := map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": nodeType},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	url := fmt.Sprintf("%s/v1/instances/%s/breakpoints", controlBase, instanceID)
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"installPauseModeBreakpoint: status=%d body=%s", resp.StatusCode, string(out))
	var decoded struct {
		BreakpointID string `json:"breakpoint_id"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded))
	id, err := uuid.Parse(decoded.BreakpointID)
	require.NoError(t, err)
	return shared.UUID(id)
}

func deleteBreakpoint(t *testing.T, controlBase string, instanceID, breakpointID shared.UUID) {
	t.Helper()
	url := fmt.Sprintf("%s/v1/instances/%s/breakpoints/%s",
		controlBase, instanceID, breakpointID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusNoContent, resp.StatusCode,
		"deleteBreakpoint: status=%d body=%s", resp.StatusCode, string(raw))
}

func waitForFirstHit(t *testing.T, h *scenario.Harness, bpID shared.UUID) persistence.BreakpointHitRow {
	t.Helper()
	for {
		var hits []persistence.BreakpointHitRow
		if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 100, tx)
			hits = r
			return err
		}); err != nil {
			t.Fatalf("waitForFirstHit: %v", err)
		}
		if len(hits) > 0 {
			return hits[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func getLatestRunID(t *testing.T, h *scenario.Harness, nodeID shared.UUID) *shared.UUID {
	t.Helper()
	var out *shared.UUID
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := h.Persist.Nodes().GetLatestRunForNode(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if latest == nil {
			return nil
		}
		runID := latest.NodeRunID
		out = &runID
		return nil
	}))
	return out
}

func getRunAttributeValue(t *testing.T, h *scenario.Harness, runID shared.UUID, key string) any {
	t.Helper()
	var out any
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.Persist.NodeAttributes().GetByRun(ctx, runID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row, "attribute row must exist after set_attribute")
		out = row.Data[key]
		return nil
	}))
	return out
}

func countDebugOverrideAuditRows(t *testing.T, h *scenario.Harness, instanceID shared.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := h.Persist.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &instanceID,
			KindIn:     []string{"debug.override.applied"},
		}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		n = len(out.Events)
		return nil
	}))
	return n
}

func readLatestDebugOverrideAudit(t *testing.T, h *scenario.Harness, instanceID shared.UUID) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := h.Persist.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &instanceID,
			KindIn:     []string{"debug.override.applied"},
		}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		require.NotEmpty(t, out.Events, "expected at least one debug.override.applied row")
		payload = out.Events[len(out.Events)-1].Payload
		return nil
	}))
	return payload
}

func stubWorkerCount(h *scenario.Harness) int {
	n := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			n++
		}
	}
	return n
}

func waitForStubWorkerCount(h *scenario.Harness, want int) {
	for stubWorkerCount(h) < want {
		time.Sleep(20 * time.Millisecond)
	}
}

func mintAnonymousAdminKey(t *testing.T, controlBase string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name":        "story-debug-channel-admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	require.NoError(t, err)
	resp, err := http.Post(controlBase+"/v1/auth/keys", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"mintAnonymousAdminKey: status=%d body=%s", resp.StatusCode, string(raw))
	var decoded struct {
		Plaintext string `json:"plaintext"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotEmpty(t, decoded.Plaintext)
	return decoded.Plaintext
}

func mintRestrictedKey(t *testing.T, controlBase, adminKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name":        "story-debug-channel-restricted",
		"permissions": []map[string]any{{"action": "instance:read"}},
	})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost,
		controlBase+"/v1/auth/keys", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"mintRestrictedKey: status=%d body=%s", resp.StatusCode, string(raw))
	var decoded struct {
		Plaintext string `json:"plaintext"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotEmpty(t, decoded.Plaintext)
	return decoded.Plaintext
}
