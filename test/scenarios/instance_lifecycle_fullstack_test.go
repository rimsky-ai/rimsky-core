// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: instance-lifecycle
// @concept: instance
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

func TestInstanceLifecycleFullStack(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Success(map[string]any{"value": 1}, true, "ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "instance-lifecycle", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "integer"},
					},
				}),
			),
		},
	})

	iid := postCreateInstanceIdle(t, h.ControlBase, tid, "ck-instance-lifecycle-idle")
	time.Sleep(500 * time.Millisecond)
	idleFrames := getInstanceFramesInst(t, h.ControlBase, iid)
	require.Emptyf(t, idleFrames,
		"STORY-instance-lifecycle falsifier: instance must be idle after create — got %d frames", len(idleFrames))
	idleMessages := getInstanceMessagesInst(t, h.ControlBase, iid)
	require.Emptyf(t, idleMessages,
		"STORY-instance-lifecycle falsifier: instance must be idle after create — got %d messages in ledger", len(idleMessages))

	w := h.FindNode(iid, "worker")
	require.NotNil(t, w, "worker node must materialize on create")
	preWakeSuccessCount := countEvents(t, h.ControlBase, iid, w.ID, "terminal/success")
	require.Equal(t, 0, preWakeSuccessCount,
		"STORY-instance-create-is-idle falsifier: a terminal/success "+
			"event appeared for the worker before any wake message was "+
			"posted — the supervisor must not dispatch against an idle "+
			"instance")

	h.PostInstanceMessage(iid, "", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	postWakeDeadline := time.Now().Add(15 * time.Second)
	postWakeSuccessCount := 0
	for time.Now().Before(postWakeDeadline) {
		postWakeSuccessCount = countEvents(t, h.ControlBase, iid, w.ID, "terminal/success")
		if postWakeSuccessCount > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Greater(t, postWakeSuccessCount, 0,
		"STORY-instance-lifecycle falsifier: no terminal/success event "+
			"appeared for the worker after the empty-message wake — the "+
			"supervisor must begin dispatching against the instance after "+
			"the empty-message wake")
	require.True(t, h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh after the empty-message wake")

	listBody := getJSONMapInst(t, h.ControlBase+"/v1/instances?template_hash="+tid)
	listed := listBody["instances"].([]any)
	require.NotEmpty(t, listed, "list must return at least one instance for the template")
	foundInList := false
	for _, it := range listed {
		m := it.(map[string]any)
		if m["id"] == iid.String() {
			foundInList = true
			break
		}
	}
	require.True(t, foundInList, "list response must include the created instance id")

	getBody := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, iid.String(), getBody["id"])
	require.Equal(t, false, getBody["paused"], "fresh instance must report paused=false")

	delNonTerminalResp := doDelete(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, http.StatusConflict, delNonTerminalResp.status,
		"DELETE on a non-terminal instance must return 409 (terminal guard): %s",
		string(delNonTerminalResp.raw))

	preSuccessCount := countEvents(t, h.ControlBase, iid, w.ID, "terminal/success")

	pauseResp := doPost(t, h.ControlBase+"/v1/instances/"+iid.String()+"/pause", nil)
	require.Equal(t, http.StatusOK, pauseResp.status,
		"pause must return 200: %s", string(pauseResp.raw))

	getAfterPause := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, true, getAfterPause["paused"], "paused must be true after /pause")

	h.PostInstanceMessage(iid, "", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	time.Sleep(2 * time.Second)
	midSuccessCount := countEvents(t, h.ControlBase, iid, w.ID, "terminal/success")
	require.Equal(t, preSuccessCount, midSuccessCount,
		"while paused, no new terminal/success event must appear on /v1/events — "+
			"the supervisor must stop claiming new dispatches for the paused instance "+
			"(spec falsifier: pause is recorded but the supervisor keeps dispatching)")

	var pendingMessages int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE`,
		[]any{iid}, &pendingMessages)
	require.GreaterOrEqual(t, pendingMessages, 1,
		"under pause the wake message must sit pending on the instance queue "+
			"(pause blocks the frame engine from picking it up; no frame opens, no dispatch is created)")

	resumeResp := doPost(t, h.ControlBase+"/v1/instances/"+iid.String()+"/resume", nil)
	require.Equal(t, http.StatusOK, resumeResp.status,
		"resume must return 200: %s", string(resumeResp.raw))

	getAfterResume := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, false, getAfterResume["paused"], "paused must be false after /resume")

	deadline := time.Now().Add(15 * time.Second)
	postResumeSuccessCount := preSuccessCount
	for time.Now().Before(deadline) {
		postResumeSuccessCount = countEvents(t, h.ControlBase, iid, w.ID, "terminal/success")
		if postResumeSuccessCount > preSuccessCount {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Greater(t, postResumeSuccessCount, preSuccessCount,
		"after /resume the supervisor must pick the queued dispatch back up "+
			"and a new terminal/success event must appear on /v1/events")

	termResp := doPost(t, h.ControlBase+"/v1/instances/"+iid.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, termResp.status,
		"terminate must return 200: %s", string(termResp.raw))
	require.True(t, waitForInstanceTerminatedInst(t, h, iid, 15*time.Second),
		"terminate must set terminated_at on the instance")

	delResp := doDelete(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, http.StatusOK, delResp.status,
		"DELETE on a terminated instance must succeed: %s", string(delResp.raw))
	require.Contains(t, string(delResp.raw), `"deleted":true`,
		"DELETE response must report deleted:true")

	getGone, err := http.Get(h.ControlBase + "/v1/instances/" + iid.String())
	require.NoError(t, err)
	defer getGone.Body.Close()
	require.Equal(t, http.StatusNotFound, getGone.StatusCode,
		"GET after DELETE must return 404 (row removed)")
}

func countEvents(t *testing.T, controlBase string, instanceID, nodeID shared.UUID, kind string) int {
	t.Helper()
	u := controlBase + "/v1/events?instance_id=" + instanceID.String() +
		"&node_id=" + nodeID.String() + "&kind=" + kind + "&limit=1000"
	resp, err := http.Get(u)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"GET %s: status=%d body=%s", u, resp.StatusCode, string(raw))
	var body struct {
		Events []map[string]any `json:"events"`
	}
	require.NoErrorf(t, json.Unmarshal(raw, &body),
		"countEvents: decode %s: %s", u, string(raw))
	return len(body.Events)
}

func getJSONMapInst(t *testing.T, url string) map[string]any {
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

type httpResp struct {
	status int
	raw    []byte
}

func doPost(t *testing.T, url string, body []byte) httpResp {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(http.MethodPost, url, reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, raw: raw}
}

func doDelete(t *testing.T, url string) httpResp {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, raw: raw}
}

func waitForInstanceTerminatedInst(t *testing.T, h *scenario.Harness, iid shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var terminatedAt *time.Time
		h.QueryRowSQL(
			`SELECT terminated_at FROM rimsky_instances WHERE id = $1`,
			[]any{iid}, &terminatedAt)
		if terminatedAt != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func postCreateInstanceIdle(t *testing.T, controlBase, templateHash, instanceKey string) shared.UUID {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	require.NoError(t, err)
	resp, err := http.Post(controlBase+"/v1/instances", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"POST /v1/instances: status=%d body=%s", resp.StatusCode, string(raw))
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoErrorf(t, json.Unmarshal(raw, &out),
		"POST /v1/instances: decode: %s", string(raw))
	parsed, err := uuid.Parse(out.InstanceID)
	require.NoErrorf(t, err, "POST /v1/instances: bad instance_id %q", out.InstanceID)
	return shared.UUID(parsed)
}

func getInstanceFramesInst(t *testing.T, controlBase string, instanceID shared.UUID) []any {
	t.Helper()
	body := getJSONMapInst(t, controlBase+"/v1/instances/"+instanceID.String()+"/frames")
	frames, _ := body["frames"].([]any)
	return frames
}

func getInstanceMessagesInst(t *testing.T, controlBase string, instanceID shared.UUID) []any {
	t.Helper()
	body := getJSONMapInst(t, controlBase+"/v1/instances/"+instanceID.String()+"/messages")
	messages, _ := body["messages"].([]any)
	return messages
}
