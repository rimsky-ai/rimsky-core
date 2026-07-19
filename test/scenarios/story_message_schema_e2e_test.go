// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: message-schema
// @concept: message-schema
package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStoryMessageSchema_DeclaredAndUndeclaredTypes(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("receiver").Success(map[string]any{"observed": "ok"}, true, "ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-message-schema", Version: "1",
		Messages: []spec.MessageSchema{
			{
				Type: "ping/recheck",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {
						"reason": {"type": "string"}
					},
					"required": ["reason"]
				}`),
			},
			{
				Type: "flush/cache",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {
						"cache_keys": {"type": "array", "items": {"type": "string"}}
					}
				}`),
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "receiver",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "ping/recheck", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{
							"type":   "string",
							"source": "{{messages.ping/recheck.reason}}",
						},
					},
					"required": []any{"reason"},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-message-schema", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iid)

	respUndeclared := postMessage(t, h.ControlBase, iid, map[string]any{
		"type":    "totally-not-declared",
		"payload": map[string]any{},
	}, "key-undeclared-"+uuid.NewString())
	require.Equal(t, http.StatusBadRequest, respUndeclared.status,
		"undeclared type must refuse with 400; body: %s", string(respUndeclared.raw))
	var undeclaredBody struct {
		Error         string   `json:"error"`
		Type          string   `json:"type"`
		DeclaredTypes []string `json:"declared_types"`
	}
	require.NoError(t, json.Unmarshal(respUndeclared.raw, &undeclaredBody))
	require.Equal(t, "unknown message type", undeclaredBody.Error)
	require.Equal(t, "totally-not-declared", undeclaredBody.Type,
		"response must name the rejected type")
	require.ElementsMatch(t, []string{"flush/cache", "ping/recheck"}, undeclaredBody.DeclaredTypes,
		"response must list the full declared registry, not just the first entry")

	var undeclaredCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND type = $2`,
		[]any{iid, "totally-not-declared"}, &undeclaredCount)
	require.Equal(t, 0, undeclaredCount,
		"undeclared type must not pollute the message ledger")

	respDeclared := postMessage(t, h.ControlBase, iid, map[string]any{
		"type":    "ping/recheck",
		"payload": map[string]any{"reason": "operator-triggered-check"},
	}, "key-declared-"+uuid.NewString())
	require.Truef(t,
		respDeclared.status == http.StatusCreated || respDeclared.status == http.StatusOK,
		"declared type must return 200/201; status=%d body=%s",
		respDeclared.status, string(respDeclared.raw))
	var declaredResp struct {
		MessageID string `json:"message_id"`
	}
	require.NoError(t, json.Unmarshal(respDeclared.raw, &declaredResp))
	require.NotEmpty(t, declaredResp.MessageID)

	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, receiver, "receiver node must exist on the instance")

	h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh)
	h.WaitForEventKind(receiver.ID, "terminal/success")

	frames := getFrames(t, h.ControlBase, iid, "")
	require.NotEmpty(t, frames, "at least one frame must exist for this instance")
	matched := false
	for _, fr := range frames {
		if fr.TriggeringMessageID == declaredResp.MessageID {
			matched = true
			require.Equal(t, "ping/recheck", fr.MessageType,
				"the frame's joined message envelope must carry the declared type")
			break
		}
	}
	require.True(t, matched,
		"no frame carries triggering_message_id = %s; observed: %+v",
		declaredResp.MessageID, frames)
}

func postMessage(t *testing.T, controlBase string, instanceID shared.UUID, body map[string]any, idempotencyKey string) httpResp {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/v1/instances/%s/messages", controlBase, instanceID), bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	require.NotEmpty(t, idempotencyKey, "every POST /messages must carry an Idempotency-Key")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, raw: out}
}

type frameView struct {
	FrameID             string `json:"frame_id"`
	State               string `json:"state"`
	TriggeringMessageID string `json:"triggering_message_id"`
	MessageType         string `json:"message_type"`
	MessageSender       string `json:"message_sender"`
	MessageSenderKind   string `json:"message_sender_kind"`
}

func getFrames(t *testing.T, controlBase string, instanceID shared.UUID, triggeringMessageIDFilter string) []frameView {
	t.Helper()
	u := fmt.Sprintf("%s/v1/instances/%s/frames?limit=100", controlBase, instanceID)
	if triggeringMessageIDFilter != "" {
		u += "&triggering_message_id=" + triggeringMessageIDFilter
	}
	resp, err := http.Get(u)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"GET %s: status=%d body=%s", u, resp.StatusCode, string(raw))
	var out struct {
		Frames []frameView `json:"frames"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out.Frames
}
