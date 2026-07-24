// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: empty-message-wakes-roots
// @concept: cascade
// @concept: message
// @decision: empty-message-as-root-trigger
// @decision: structural-root-edge-injection-at-registration

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

func TestStory_EmptyMessageWakesRoots(t *testing.T) {
	t.Parallel()

	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("root1").Success(map[string]any{"r1": 1}, true, "root1")
	h.Stub.WhenType("root2").Success(map[string]any{"r2": 1}, true, "root2")
	h.Stub.WhenType("down").Success(map[string]any{"d": 1}, true, "down")

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
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	}

	templateHash := h.DeployTemplate(tspec)
	require.NotEmpty(t, templateHash)

	instanceID := postCreateInstance(t, h, templateHash, "ck-empty-message-wakes-roots")
	require.NotEmpty(t, instanceID)

	time.Sleep(500 * time.Millisecond)
	require.Empty(t, getInstanceFrames(t, h, instanceID), "instance must be idle before the wake step (STORY-instance-create-is-idle precondition)")
	require.Empty(t, getInstanceMessages(t, h, instanceID), "message ledger must be empty before the wake step (STORY-instance-create-is-idle precondition)")

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

	instUUID := parseInstanceUUID(t, instanceID)
	root1 := h.FindNode(instUUID, "root1")
	root2 := h.FindNode(instUUID, "root2")
	down := h.FindNode(instUUID, "down")
	require.NotNil(t, root1, "root1 node row must exist")
	require.NotNil(t, root2, "root2 node row must exist")
	require.NotNil(t, down, "down node row must exist")

	h.WaitForNodeState(root1.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(root2.ID, cascade.NodeStateFresh)

	h.WaitForEventCount(root1.ID, "terminal/success", 1)
	h.WaitForEventCount(root2.ID, "terminal/success", 1)

	h.WaitForEventCount(down.ID, "terminal/success", 1)

	var emptyTriggerRunID shared.UUID
	h.QueryRowSQL(`
		SELECT r.id FROM rimsky_node_runs r
		  JOIN rimsky_nodes n ON n.id = r.node_id
		 WHERE n.instance_id = $1 AND n.node_type = ''`,
		[]any{instUUID}, &emptyTriggerRunID)

	var root1RunID shared.UUID
	h.QueryRowSQL(`SELECT id FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{root1.ID}, &root1RunID)

	var downSenderRunIDs []shared.UUID
	h.QuerySQL(`
		SELECT w.sender_run_id FROM rimsky_wait_set w
		  JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
		 WHERE r.node_id = $1`,
		[]any{down.ID},
		func(scan func(...any) error) error {
			var sid shared.UUID
			if err := scan(&sid); err != nil {
				return err
			}
			downSenderRunIDs = append(downSenderRunIDs, sid)
			return nil
		})
	require.NotEmpty(t, downSenderRunIDs,
		"down must carry a wait-set row tying its dispatch to an upstream sender")
	for _, sid := range downSenderRunIDs {
		require.NotEqual(t, emptyTriggerRunID, sid,
			"down is a non-root node with an author-declared subscription (subscribes: root1); its "+
				"wait-set must never carry a row sent directly by the empty-message trigger run (%s) — "+
				"that would mean the trigger overreached and stale-marked a non-root subscriber "+
				"directly instead of down legitimately cascading off root1's terminal/success",
			emptyTriggerRunID)
		require.Equal(t, root1RunID, sid,
			"down's only legitimate wait-set sender is root1's node-run (%s); got sender_run_id=%s",
			root1RunID, sid)
	}

	replayStatus, replayBody := postInstanceMessage(t, h, instanceID, "", wakeKey)
	require.Equal(t, http.StatusOK, replayStatus,
		"replay with same Idempotency-Key must return 200 OK (not 201 Created); got %d: %s", replayStatus, string(replayBody))
	var replayOut struct {
		MessageID string `json:"message_id"`
	}
	require.NoErrorf(t, json.Unmarshal(replayBody, &replayOut), "replay decode: %s", string(replayBody))
	require.Equal(t, wakeMessageID, replayOut.MessageID,
		"replay must echo the ORIGINAL message_id (idempotent dedup); got %q want %q", replayOut.MessageID, wakeMessageID)

	framesAfterReplay := getInstanceFrames(t, h, instanceID)
	require.Lenf(t, framesAfterReplay, 1,
		"replay with same Idempotency-Key must NOT open a second frame; got %d frames", len(framesAfterReplay))

	messagesAfterReplay := getInstanceMessages(t, h, instanceID)
	require.Lenf(t, messagesAfterReplay, 1,
		"message ledger must contain exactly one envelope (the operator-posted empty-message wake); got %d", len(messagesAfterReplay))
	msg0, _ := messagesAfterReplay[0].(map[string]any)
	require.NotNil(t, msg0)
	require.Equal(t, wakeMessageID, msg0["id"],
		"the one envelope in the ledger MUST be the operator-posted wake; got %q want %q", msg0["id"], wakeMessageID)
	require.Equal(t, "operator", msg0["sender_kind"],
		"the wake envelope's sender_kind must be `operator` (no synthetic-envelope sender_kind=instance row should appear)")

	var downRunCount int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{down.ID}, &downRunCount)
	require.Equal(t, 1, downRunCount,
		"down must have exactly one node-run (its legitimate cascade off root1); got %d — a second "+
			"run would mean the empty-message trigger also stale-marked the non-root subscriber directly",
		downRunCount)

	var downCreationReason string
	h.QueryRowSQL(`SELECT creation_reason FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{down.ID}, &downCreationReason)
	require.Equal(t, string(cascade.CreationReasonCascade), downCreationReason,
		"down's single node-run must be creation_reason=cascade (woken by root1's terminal/success), "+
			"never message_delivery — message_delivery would mean the empty-message trigger overreached "+
			"and directly stale-marked a non-root node with author-declared subscriptions",
	)
}

func postCreateInstance(t *testing.T, h *scenario.Harness, templateHash, instanceKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"params":       map[string]any{},
		"target_agent": "scenario-default-agent",
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

func getInstanceFrames(t *testing.T, h *scenario.Harness, instanceID string) []any {
	t.Helper()
	body := getJSONMap(t, h.ControlBase+"/v1/instances/"+instanceID+"/frames")
	frames, _ := body["frames"].([]any)
	return frames
}

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

func parseInstanceUUID(t *testing.T, s string) shared.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	require.NoErrorf(t, err, "parseInstanceUUID: bad instance_id %q", s)
	return shared.UUID(id)
}
