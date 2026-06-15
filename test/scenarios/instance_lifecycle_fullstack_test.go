// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Full-stack proof for STORY-instance-lifecycle's non-force-terminate legs:
// create / list / get / pause / resume / delete-non-terminal-rejected /
// delete-terminal-succeeds. The sibling lifecycle_force_terminate_fullstack
// _test.go covers the force-terminate leg end-to-end; this file completes
// the story.
//
// Load-bearing property the test protects against the spec's pause
// falsifier ("pause is recorded but the supervisor keeps dispatching
// against the instance"): we don't just assert the `paused` column was
// flipped; we drive the supervisor's claim layer by INVALIDATING the
// node (which the frame engine turns into a fresh pending dispatch row)
// while the instance is paused, then assert that no new
// `terminal/success` event for the node appears on
// `GET /v1/events?instance_id=...&kind=terminal/success` during a fixed
// pause window — i.e. the supervisor stops CLAIMING new dispatches for
// the paused instance even when there is queued work waiting to run.
// After /resume the same dispatch is picked up and a fresh
// terminal/success event arrives. This is the spec's required acceptance:
// the cheaper "the pause flag was written" shape is NOT the acceptance.
//
// @concept: instance
package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestInstanceLifecycleFullStack(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: Worker emits a small attribute delta on each dispatch. The
	// terminal/success event count for this node is the supervisor-
	// observable proxy for "a new dispatch was claimed and run" — the
	// load-bearing signal under pause.
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

	// @deliberate: (1) CREATE: POST /v1/instances returns 201 with an instance_id and
	// the supervisor begins driving the node. CreateInstance asserts the
	// 201 status and waits for the root dispatch row to materialize, so
	// reaching past it proves the create path is wired end-to-end.
	iid := h.CreateInstance(tid, "ck-instance-lifecycle", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iid, "create must return a non-zero instance id")

	w := h.FindNode(iid, "worker")
	require.NotNil(t, w, "worker node must materialize on create")

	// @deliberate: The supervisor drives the first dispatch through the REAL claim
	// path; reaching fresh proves create wired the instance into the
	// dispatch queue, not just persisted a row.
	require.True(t, h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh on first run — create did not actually drive a dispatch")

	// @constraint: (2) LIST: GET /v1/instances?template_hash=... must include this instance.
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

	// @constraint: (3) GET: GET /v1/instances/{id} returns the row with paused=false
	// initially.
	getBody := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, iid.String(), getBody["id"])
	require.Equal(t, false, getBody["paused"], "fresh instance must report paused=false")

	// @constraint: (4) DELETE non-terminal: must be refused with 409. Spec falsifier:
	// "delete succeeds non-terminal".
	delNonTerminalResp := doDelete(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, http.StatusConflict, delNonTerminalResp.status,
		"DELETE on a non-terminal instance must return 409 (terminal guard): %s",
		string(delNonTerminalResp.raw))

	// @deliberate: (5) PAUSE — the load-bearing leg.
	//
	// Snapshot the count of terminal/success events for this node
	// PRIOR to pause + invalidate. Any new dispatch that the supervisor
	// claims and runs through the stub will emit one more.
	preSuccessCount := countEvents(t, h.ControlBase, iid, w.ID, "terminal/success")

	pauseResp := doPost(t, h.ControlBase+"/v1/instances/"+iid.String()+"/pause", nil)
	require.Equal(t, http.StatusOK, pauseResp.status,
		"pause must return 200: %s", string(pauseResp.raw))

	// @constraint: Verify paused=true on the read surface.
	getAfterPause := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, true, getAfterPause["paused"], "paused must be true after /pause")

	// @deliberate: Drive the supervisor's claim layer: INVALIDATE the worker. The
	// frame engine flips the node to stale and enqueues a fresh pending
	// dispatch row. On an UNPAUSED instance the supervisor's 100ms
	// claim-poll would pick it up within ~1s and emit a new
	// terminal/success. Under pause, the postgres queue's
	// `i.paused = false` predicate (lib/foundation/persistence/postgres/
	// queue.go) filters the row out and it sits in `pending`.
	//
	// The retired operator route `POST /v1/nodes/{id}/invalidate` is
	// replaced by the same runtime-synthetic envelope it used to seed
	// internally (`node/invalidate` + `wake_node_ids`), driven via the
	// scenario harness helper. The frame-engine path is identical.
	h.InvalidateNode(iid, w.ID)

	// @deliberate: Pause window: wait 2s and assert no new terminal/success event
	// appears on `GET /v1/events?instance_id=...&kind=terminal/success`.
	// This is the spec's required acceptance shape — observing the
	// real event-log surface, not just probing the `paused` column.
	time.Sleep(2 * time.Second)
	midSuccessCount := countEvents(t, h.ControlBase, iid, w.ID, "terminal/success")
	require.Equal(t, preSuccessCount, midSuccessCount,
		"while paused, no new terminal/success event must appear on /v1/events — "+
			"the supervisor must stop claiming new dispatches for the paused instance "+
			"(spec falsifier: pause is recorded but the supervisor keeps dispatching)")

	// @deliberate: The node row itself must be `stale` (the invalidate took effect at
	// the frame layer) and a pending dispatch must be sitting unclaimed.
	// Together these prove the supervisor's claim, not just the
	// invalidate, is what's gated by pause.
	var pendingCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND phase = 'pending' AND claimed_by IS NULL`,
		[]any{w.ID}, &pendingCount)
	require.GreaterOrEqual(t, pendingCount, 1,
		"under pause there must be at least one unclaimed pending dispatch row "+
			"for the invalidated worker (the supervisor refused to claim it)")

	// @deliberate: (6) RESUME: POST /v1/instances/{id}/resume → 200, then the
	// supervisor's next claim-poll picks up the pending dispatch and a
	// new terminal/success event arrives.
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

	// @deliberate: (7) Drive the instance terminal via POST /v1/instances/{id}/terminate
	// so DELETE can succeed. This is the same terminate handler the
	// force-terminate sibling test exercises — here we use it as the
	// reaper-precondition step, not as the proof itself.
	termResp := doPost(t, h.ControlBase+"/v1/instances/"+iid.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, termResp.status,
		"terminate must return 200: %s", string(termResp.raw))
	require.True(t, waitForInstanceTerminatedInst(t, h, iid, 15*time.Second),
		"terminate must set terminated_at on the instance")

	// @constraint: (8) DELETE terminal: now passes the terminal guard, returns
	// 200 {"deleted":true}.
	delResp := doDelete(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Equal(t, http.StatusOK, delResp.status,
		"DELETE on a terminated instance must succeed: %s", string(delResp.raw))
	require.Contains(t, string(delResp.raw), `"deleted":true`,
		"DELETE response must report deleted:true")

	// @deliberate: (9) Confirm the row is actually gone — a follow-up GET returns 404.
	getGone, err := http.Get(h.ControlBase + "/v1/instances/" + iid.String())
	require.NoError(t, err)
	defer getGone.Body.Close()
	require.Equal(t, http.StatusNotFound, getGone.StatusCode,
		"GET after DELETE must return 404 (row removed)")
}

// countEvents queries /v1/events?instance_id=...&node_id=...&kind=... and
// returns the number of rows in the response. The spec specifically asks
// the pause assertion to read from the events surface, not the underlying
// rimsky_events table directly, so the test exercises the real read
// surface a debugger would use.
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

// getJSONMapInst is a local copy of the getJSONMap helper used by
// sibling scenarios. Lives here to avoid a cross-file dependency cycle
// (the helper file in this package uses the same name as a helper in
// the observability_latest_attribute_fullstack_test.go file).
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

// waitForInstanceTerminatedInst polls until terminated_at is set on the
// instance row. Mirrors the waitForInstanceTerminatedLifecycle helper in
// template_lifecycle_e2e_test.go; lives here to avoid coupling tests
// across files.
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
