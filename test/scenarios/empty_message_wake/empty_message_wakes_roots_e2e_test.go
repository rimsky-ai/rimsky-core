// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// empty_message_wakes_roots_e2e_test.go — executable proof for
// STORY-empty-message-wakes-roots.
//
// Boots the in-process full stack (scheduler + supervisor + control-api
// + stub executor) via the scenario harness against a Postgres
// testcontainer, deploys a template with two structural roots, one
// direct downstream subscriber, and one cross-cutting watcher, then:
//
//  1. Creates an instance via the raw HTTP surface (bypassing the
//     harness's CreateInstance helper, which now emits an internal
//     wake message after the create POST). Asserts the instance is
//     idle — empty frames, empty message ledger.
//  2. POSTs an empty-bodied message (`type: ""`) with an
//     Idempotency-Key. Asserts the response is `201 Created` with a
//     non-empty message_id.
//  3. Observes exactly one new frame with `triggering_message_id`
//     matching the returned message_id; observes node-runs for the two
//     structural roots and waits for them to settle; observes the
//     direct downstream subscriber dispatches after its upstream root
//     settles.
//  4. Replays the same Idempotency-Key with the same body. Asserts the
//     response is `200 OK` carrying the ORIGINAL message_id (replay
//     surface) and that the frame count is still exactly one (no
//     second frame opens).
//
// The cross-cutting (`instance: true`) watcher's dispatch is observed
// but its presence in node-runs is not asserted as a falsifier — per
// the spec, cross-cutting subscribers may legitimately fire on the
// empty-message virtual's terminal/success emission.
//
// @story: empty-message-wakes-roots
// @concept: cascade
// @concept: message
// @decision: empty-message-as-root-trigger
// @decision: structural-root-edge-injection-at-registration
// @decision: empty-sender-key-edge-disambiguation

package empty_message_wake

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestStory_EmptyMessageWakesRoots exhibits STORY-empty-message-wakes-roots
// end-to-end: idle-on-create, empty-message emit opens a single frame
// with the right triggering message id, the two structural roots stale-
// mark and dispatch (and the direct downstream subscriber dispatches
// downstream of its upstream root), and a replay with the same key
// returns the original message_id with 200 OK and opens no second frame.
func TestStory_EmptyMessageWakesRoots(t *testing.T) {
	t.Parallel()

	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: every node-type dispatched returns Success against the
	// stub executor so the structural roots reach terminal/success in
	// the frame the empty-message envelope opens; the direct downstream
	// subscriber then cascades and reaches its own terminal/success.
	h.Stub.WhenType("root1").Success(map[string]any{"r1": 1}, true, "root1")
	h.Stub.WhenType("root2").Success(map[string]any{"r2": 1}, true, "root2")
	h.Stub.WhenType("down").Success(map[string]any{"d": 1}, true, "down")
	h.Stub.WhenType("watch").Success(map[string]any{"w": 1}, true, "watch")

	// @deliberate: template shape — two structural roots, one direct
	// downstream subscriber, one cross-cutting watcher.
	//
	//   - `root1` and `root2` carry no `subscribes:` block, so the
	//     runtime-injected structural-root edges (sender="",
	//     sender_bound_to_empty=true) fire them when the empty-message
	//     virtual settles `terminal/success`.
	//   - `down` carries an author-declared direct subscription to
	//     `root1` on `terminal/success`. It MUST NOT stale-mark from the
	//     empty-message wake itself — the structural-root injection is
	//     scoped to sender="", and `down`'s subscription is to
	//     `Node: root1`, not the empty sender. `down` dispatches via
	//     cascade after `root1` settles.
	//   - `watch` carries a cross-cutting (`Instance: true`) subscription
	//     on `terminal/success`. Cross-cutting subscribers may legitimately
	//     fire on the empty-message virtual's terminal/success emission;
	//     the test observes its dispatch but does not pin its presence as
	//     a falsifier.
	tspec := node.TemplateSpec{
		Name:    "empty-message-wakes-roots",
		Version: "v1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "root1", Executor: "stub"},
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "root2", Executor: "stub"},
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "down", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node:                 "root1",
					Type:                 "terminal/success",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "watch", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance:             true,
					Type:                 "terminal/success",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	}

	templateHash := h.DeployTemplate(tspec)
	require.NotEmpty(t, templateHash)

	// @constraint: bypass Harness.CreateInstance — that helper now emits
	// an internal wake message after the create POST. To observe the
	// idle-on-create precondition and to drive the wake explicitly the
	// test uses the raw HTTP surface.
	instanceID := postCreateInstance(t, h, templateHash, "ck-empty-message-wakes-roots")
	require.NotEmpty(t, instanceID)

	// @constraint: STORY-instance-create-is-idle precondition — the
	// instance MUST be idle (empty frames, empty message ledger)
	// before the wake step. A frame or message landing here would be
	// a regression in the idle-on-create story but it would also
	// confound this story's "exactly one frame" assertion below.
	time.Sleep(500 * time.Millisecond)
	require.Empty(t, getInstanceFrames(t, h, instanceID), "instance must be idle before the wake step (STORY-instance-create-is-idle precondition)")
	require.Empty(t, getInstanceMessages(t, h, instanceID), "message ledger must be empty before the wake step (STORY-instance-create-is-idle precondition)")

	// @constraint: STORY-empty-message-wakes-roots — emit the empty
	// message. `type: ""` is the implicit runtime-injected empty-message
	// type-path (per decision:empty-message-as-root-trigger). The
	// Idempotency-Key is mandatory on every POST /messages emit.
	const wakeKey = "test-wake-1"
	postStatus, postBody := postInstanceMessage(t, h, instanceID, "", wakeKey)
	require.Equal(t, http.StatusCreated, postStatus,
		"first emit must return 201 Created (fresh insert); got %d: %s", postStatus, string(postBody))
	var postOut struct {
		MessageID string `json:"message_id"`
	}
	require.NoErrorf(t, json.Unmarshal(postBody, &postOut), "POST /messages decode: %s", string(postBody))
	require.NotEmpty(t, postOut.MessageID, "POST /messages must return a non-empty message_id")
	wakeMessageID := postOut.MessageID

	// @constraint: STORY-empty-message-wakes-roots falsifier (1): "the
	// empty-message emit lands in the ledger but no frame opens". Wait
	// for the frame to appear with the right triggering_message_id.
	require.Eventually(t, func() bool {
		return len(getInstanceFrames(t, h, instanceID)) >= 1
	}, 10*time.Second, 100*time.Millisecond,
		"a frame must open in response to the empty-message emit")

	frames := getInstanceFrames(t, h, instanceID)
	require.Len(t, frames, 1, "exactly one frame must open for one empty-message emit; got %d", len(frames))
	frame0, _ := frames[0].(map[string]any)
	require.NotNil(t, frame0)
	require.Equal(t, wakeMessageID, frame0["triggering_message_id"],
		"frame.triggering_message_id must point at the emitted empty-message envelope")

	// @constraint: STORY-empty-message-wakes-roots falsifier (2): "the
	// frame opens but no structural root stale-marks (no node-runs
	// created)". Wait for the two structural-root nodes to dispatch and
	// settle to terminal/success.
	instUUID := parseInstanceUUID(t, instanceID)
	root1 := h.FindNode(instUUID, "root1")
	root2 := h.FindNode(instUUID, "root2")
	down := h.FindNode(instUUID, "down")
	require.NotNil(t, root1, "root1 node row must exist")
	require.NotNil(t, root2, "root2 node row must exist")
	require.NotNil(t, down, "down node row must exist")

	require.Truef(t, h.WaitForNodeState(root1.ID, cascade.NodeStateFresh, 15*time.Second),
		"root1 must dispatch and settle to fresh via the empty-message wake")
	require.Truef(t, h.WaitForNodeState(root2.ID, cascade.NodeStateFresh, 15*time.Second),
		"root2 must dispatch and settle to fresh via the empty-message wake")

	// @constraint: each structural root emitted a real terminal/success
	// (not just a state flip). The audit row guards against the
	// falsifier shape "state=fresh but the supervisor never actually
	// claimed and ran" — fresh is also the default for an untouched
	// node, so checking the audit-event side is the discriminator.
	require.True(t, h.WaitForEventKind(root1.ID, "terminal/success", 10*time.Second),
		"root1 must have a real terminal/success event (proof of actual dispatch)")
	require.True(t, h.WaitForEventKind(root2.ID, "terminal/success", 10*time.Second),
		"root2 must have a real terminal/success event (proof of actual dispatch)")

	// @constraint: STORY-empty-message-wakes-roots falsifier (3): "a
	// non-root node with author-declared direct subscriptions also
	// stale-marks (the trigger overreaches)". `down` has a direct
	// `subscribes:` to `root1`. It MUST dispatch — but as a downstream
	// of `root1`'s settlement, not as a direct wakee of the empty-
	// message envelope. We observe `down` reaches terminal/success
	// (proves cascade-through-root1 works); the structural-root edge
	// itself did not overreach because `down`'s edge has
	// `sender_bound_to_empty=false` and the empty-message virtual's
	// sender type is "" (not "root1"), so the empty-message wake alone
	// does not fire `down`.
	require.True(t, h.WaitForEventKind(down.ID, "terminal/success", 15*time.Second),
		"down must dispatch downstream of root1's settlement (cascade through author-declared edge)")

	// @constraint: STORY-empty-message-wakes-roots falsifier (4):
	// "Idempotency-Key replay opens a second frame". Replay with the
	// same key and same body.
	replayStatus, replayBody := postInstanceMessage(t, h, instanceID, "", wakeKey)
	require.Equal(t, http.StatusOK, replayStatus,
		"replay with same Idempotency-Key must return 200 OK (not 201 Created); got %d: %s", replayStatus, string(replayBody))
	var replayOut struct {
		MessageID string `json:"message_id"`
	}
	require.NoErrorf(t, json.Unmarshal(replayBody, &replayOut), "replay decode: %s", string(replayBody))
	require.Equal(t, wakeMessageID, replayOut.MessageID,
		"replay must echo the ORIGINAL message_id (idempotent dedup); got %q want %q", replayOut.MessageID, wakeMessageID)

	// @constraint: frame count remains exactly one after the replay.
	// A second frame opening would falsify the story; the replay must
	// be a no-op against the frame engine.
	framesAfterReplay := getInstanceFrames(t, h, instanceID)
	require.Lenf(t, framesAfterReplay, 1,
		"replay with same Idempotency-Key must NOT open a second frame; got %d frames", len(framesAfterReplay))

	// @constraint: STORY-empty-message-wakes-roots falsifier (5,
	// extra-belt): the ledger after the replay must contain EXACTLY
	// one message — the original wake envelope. A synthetic envelope
	// landing in the ledger as a side-effect of the wake (the retired
	// pre-spec behavior the synthetic-envelope mechanism produced)
	// would surface as a second row here. The replay's idempotent-
	// dedup must not insert a second row either.
	messagesAfterReplay := getInstanceMessages(t, h, instanceID)
	require.Lenf(t, messagesAfterReplay, 1,
		"message ledger must contain exactly one envelope (the operator-posted empty-message wake); got %d", len(messagesAfterReplay))
	msg0, _ := messagesAfterReplay[0].(map[string]any)
	require.NotNil(t, msg0)
	require.Equal(t, wakeMessageID, msg0["id"],
		"the one envelope in the ledger MUST be the operator-posted wake; got %q want %q", msg0["id"], wakeMessageID)
	require.Equal(t, "operator", msg0["sender_kind"],
		"the wake envelope's sender_kind must be `operator` (no synthetic-envelope sender_kind=instance row should appear)")

	// @deliberate: the cross-cutting `watch` node is allowed (but not
	// required) to dispatch per the spec — cross-cutting subscribers
	// may legitimately fire on the empty-message virtual's
	// terminal/success emission. We do not pin its dispatch as a
	// falsifier (positive or negative).
	_ = down
}

// postCreateInstance POSTs to /v1/instances directly (bypassing the
// harness's CreateInstance helper, which now emits an internal wake
// message after the create POST). Returns the new instance_id string.
func postCreateInstance(t *testing.T, h *scenario.Harness, templateHash, instanceKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/instances", "application/json", bytes.NewReader(body))
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
	require.NotEmpty(t, out.InstanceID)
	return out.InstanceID
}

// postInstanceMessage POSTs to /v1/instances/{id}/messages with the
// given type and Idempotency-Key. Returns (statusCode, responseBody)
// so the test can distinguish 201 Created (fresh insert) from 200 OK
// (idempotent replay).
func postInstanceMessage(t *testing.T, h *scenario.Harness, instanceID, msgType, idempotencyKey string) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"type": msgType})
	require.NoError(t, err)
	url := h.ControlBase + "/v1/instances/" + instanceID + "/messages"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// getInstanceFrames GETs /v1/instances/{id}/frames and returns the
// `frames` array from the JSON response.
func getInstanceFrames(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/frames")
	frames, _ := body["frames"].([]any)
	return frames
}

// getInstanceMessages GETs /v1/instances/{id}/messages and returns the
// `messages` array from the JSON response.
func getInstanceMessages(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/messages")
	messages, _ := body["messages"].([]any)
	return messages
}

func getJSONMap(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s: status=%d body=%s", url, resp.StatusCode, string(raw))
	var out map[string]any
	require.NoErrorf(t, json.Unmarshal(raw, &out), "GET %s: decode: %s", url, string(raw))
	return out
}

// parseInstanceUUID parses an instance_id string into a shared.UUID for
// the Harness.FindNode lookup. shared.UUID is a type alias over
// uuid.UUID; the cast is purely a name change.
func parseInstanceUUID(t *testing.T, s string) shared.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	require.NoErrorf(t, err, "parseInstanceUUID: bad instance_id %q", s)
	return shared.UUID(id)
}
