// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instance_lifecycle_fullstack_test.go — exhibits the operator-driven
// STORY-instance-lifecycle end-to-end: create (observe post-create
// idle: empty frames + empty message ledger before any wake), post
// the empty-message wake as a separate operator action and observe
// the supervisor begin dispatching, pause, resume,
// force-terminate-via-/terminate, delete. The sibling
// lifecycle_force_terminate_fullstack_test.go covers the
// force-terminate-from-wedge leg end-to-end; this file completes the
// story.
//
// Load-bearing properties the test protects:
//
//  1. STORY-instance-lifecycle (post-spec): instance-create is idle.
//     `POST /v1/instances` materializes the instance row and the
//     per-instance node rows but enqueues no frame and lands no
//     message in the ledger; the supervisor does not dispatch
//     against the instance until a sender posts a message. The
//     proof issues the create via the raw HTTP surface (the harness's
//     `CreateInstance` helper now wakes after the create POST per
//     decision:test-harness-create-instance-wakes-roots-after-create
//     and would confound the observation).
//
//  2. STORY-instance-lifecycle (wake-as-separate-action): the empty-
//     message wake is the legitimate trigger that flips an idle
//     instance into running per
//     decision:empty-message-as-root-trigger and
//     story:empty-message-wakes-roots. The test posts the empty
//     message via the public POST /v1/instances/{id}/messages route
//     as an explicit operator action layered on top of the create,
//     then asserts the structural-root worker reaches the fresh
//     state — the supervisor-observable proxy for "began dispatching
//     against the instance."
//
//  3. STORY-instance-lifecycle (pause falsifier): "pause is recorded
//     but the supervisor keeps dispatching against the instance". We
//     don't just assert the `paused` column was flipped; we drive
//     the supervisor's claim layer by emitting an empty-message wake
//     while the instance is paused, then assert that no new
//     `terminal/success` event for the node appears on
//     `GET /v1/events?instance_id=...&kind=terminal/success` during a
//     fixed pause window — i.e. the supervisor stops CLAIMING new
//     dispatches for the paused instance even when there is queued
//     work waiting to run. After /resume the same dispatch is picked
//     up and a fresh terminal/success event arrives. The cheaper
//     "the pause flag was written" shape is NOT the acceptance.
//
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

	// @deliberate: Worker emits a small attribute delta on each dispatch. The
	// terminal/success event count for this node is the supervisor-
	// observable proxy for "a new dispatch was claimed and run" — the
	// load-bearing signal under pause.
	h.Stub.WhenType("worker").Success(map[string]any{"value": 1}, true, "ran")

	// @deliberate: worker declares NO subscribes block and reads no
	// upstream typed-message fields. Per
	// decision:structural-root-edge-injection-at-registration and
	// story:empty-message-wakes-roots, that makes worker a structural
	// root: the registration-time edge builder injects a sender="",
	// SenderBoundToEmpty=true edge for it, and an empty-message wake
	// posted to the instance fires worker. The empty-message wake is
	// then the supervisor-observable wake-as-separate-operator-action
	// step the spec mandates for STORY-instance-lifecycle.
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

	// @constraint: (0) IDLE-ON-CREATE: STORY-instance-lifecycle's
	// post-spec acceptance — POST /v1/instances must NOT enqueue any
	// frame, land any message in the ledger, or dispatch any node-run
	// until a sender posts a message. The proof issues the create via
	// the raw HTTP surface (not via h.CreateInstance, which now emits
	// an internal empty-message wake after the create POST per
	// decision:test-harness-create-instance-wakes-roots-after-create
	// and would confound the observation). After a small bounded
	// settle window, the instance's frame collection AND its message
	// ledger must both be empty. The same instance is then driven
	// through the wake / pause / resume / terminate / delete legs so
	// the empty-message wake is exercised against the same idle
	// instance whose idle state was just observed.
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
	// @constraint: while idle (no wake posted yet), the supervisor
	// must NOT have dispatched against the worker — the load-bearing
	// proxy is the absence of any terminal/success event on the
	// /v1/events surface. A freshly-created node's row carries
	// state='fresh' by default (the implicit "no run row → fresh"
	// rule in lib/foundation/persistence/postgres/nodes.go::Create);
	// that's a static default, not evidence that a dispatch occurred.
	// The event-log row is the supervisor-observable signal that a
	// dispatch was actually claimed and run.
	preWakeSuccessCount := countEvents(t, h.ControlBase, iid, w.ID, "terminal/success")
	require.Equal(t, 0, preWakeSuccessCount,
		"STORY-instance-create-is-idle falsifier: a terminal/success "+
			"event appeared for the worker before any wake message was "+
			"posted — the supervisor must not dispatch against an idle "+
			"instance")

	// @deliberate: (1) WAKE-AS-SEPARATE-OPERATOR-ACTION: post the
	// empty-message wake as an explicit operator action against the
	// idle instance. Per
	// decision:empty-message-as-root-trigger and
	// story:empty-message-wakes-roots, the supervisor begins
	// dispatching against the instance after this — the worker
	// (structural root, no upstream subscriptions) is stale-marked by
	// the cascade walker via the runtime-injected
	// SenderBoundToEmpty=true edge under sender="" and the supervisor
	// claims the resulting pending dispatch. The supervisor-observable
	// proxy for "began dispatching" is the appearance of the first
	// terminal/success event for the worker on /v1/events.
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
	// @deliberate: with the dispatch landed, the worker's row reaches
	// the fresh state. The pause/resume legs below count terminal/success
	// events from this baseline.
	require.True(t, h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh after the empty-message wake")

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

	// @deliberate: Drive the supervisor's claim layer by emitting an
	// empty-message wake. The cascade walker stale-marks the
	// structural-root worker via its SenderBoundToEmpty=true edge and
	// enqueues a fresh pending dispatch row. On an UNPAUSED instance
	// the supervisor's 100ms claim-poll would pick it up within ~1s
	// and emit a new terminal/success. Under pause, the postgres
	// queue's `i.paused = false` predicate
	// (lib/foundation/persistence/postgres/queue.go) filters the row
	// out and it sits in `pending`.
	h.PostInstanceMessage(iid, "", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

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

// postCreateInstanceIdle POSTs /v1/instances directly (bypassing the
// harness's CreateInstance helper, which now emits an internal wake
// message after the create POST per
// decision:test-harness-create-instance-wakes-roots-after-create).
// Returns the new instance_id. Used by the idle-on-create assertion
// to observe the un-woken state directly.
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

// getInstanceFramesInst GETs /v1/instances/{id}/frames and returns
// the `frames` array. The empty-on-idle invariant is asserted by the
// caller.
func getInstanceFramesInst(t *testing.T, controlBase string, instanceID shared.UUID) []any {
	t.Helper()
	body := getJSONMapInst(t, controlBase+"/v1/instances/"+instanceID.String()+"/frames")
	frames, _ := body["frames"].([]any)
	return frames
}

// getInstanceMessagesInst GETs /v1/instances/{id}/messages and
// returns the `messages` array.
func getInstanceMessagesInst(t *testing.T, controlBase string, instanceID shared.UUID) []any {
	t.Helper()
	body := getJSONMapInst(t, controlBase+"/v1/instances/"+instanceID.String()+"/messages")
	messages, _ := body["messages"].([]any)
	return messages
}
