// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: cascade-send
// @concept: message-sender-node
package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStoryCascadeSend_SendsAndOpensNextFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("pong").Success(map[string]any{"status": "needs_work"}, true, "produced status")

	h.Stub.WhenType("tail").Success(map[string]any{"observed": "ok"}, true, "saw cascade-send")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-cascade-send", Version: "1",
		Messages: []spec.MessageSchema{
			{
				Type: "initial/wakeup",
				BodySchema: []byte(`{
					"type": "object",
					"properties": { "kick": {"type": "string"} }
				}`),
			},
			{
				Type: "ping/recheck",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {
						"pong_status": {"type": "string"}
					},
					"required": ["pong_status"]
				}`),
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "pong",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "initial/wakeup", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status": map[string]any{"type": "string"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:         "sender",
					SendsMessage: "ping/recheck",
					Subscribes: []node.SubscriptionEntry{
						{Node: "pong", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
						{Node: "pong", Type: "attribute/status/changed", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pong_status": map[string]any{
							"type":   "string",
							"source": "{{nodes.pong.attribute.status}}",
						},
					},
					"required": []any{"pong_status"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "tail",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "ping/recheck", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"observed_pong_status": map[string]any{
							"type":   "string",
							"source": "{{messages.ping/recheck.pong_status}}",
						},
					},
					"required": []any{"observed_pong_status"},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-cascade-send", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iid)

	resp := postMessage(t, h.ControlBase, iid, map[string]any{
		"type":    "initial/wakeup",
		"payload": map[string]any{"kick": "go"},
	}, "key-wakeup-"+uuid.NewString())
	require.Truef(t, resp.status == http.StatusOK || resp.status == http.StatusCreated,
		"wakeup POST must succeed; status=%d body=%s", resp.status, string(resp.raw))
	var wakeupBody struct {
		MessageID string `json:"message_id"`
	}
	require.NoError(t, json.Unmarshal(resp.raw, &wakeupBody))

	tailNode := h.FindNode(iid, "tail")
	require.NotNil(t, tailNode)
	require.NotNil(t, h.FindNode(iid, "pong"))
	require.NotNil(t, h.FindNode(iid, "sender"))

	require.True(t,
		h.WaitForEventKind(tailNode.ID, "terminal/success", 30*time.Second),
		"tail did not emit terminal/success — cascade-send pipeline broken (pong → sender → sent message → tail)")

	var sentMsgID, sentSender, sentSenderKind string
	var sentBody []byte
	h.QueryRowSQL(
		`SELECT id::text, sender, sender_kind, payload
		   FROM rimsky_messages
		  WHERE instance_id = $1
		    AND type = 'ping/recheck'
		  ORDER BY received_at DESC
		  LIMIT 1`,
		[]any{iid}, &sentMsgID, &sentSender, &sentSenderKind, &sentBody)
	require.NotEmpty(t, sentMsgID, "no cascade-send envelope landed in the ledger")
	require.Equal(t, "instance", sentSenderKind,
		"cascade-send must carry sender_kind=instance per concept:message-sender-node")
	require.True(t, strings.HasPrefix(sentSender, "instance:"),
		"cascade-send sender must be instance:<id>, got %q", sentSender)

	var bodyDecoded map[string]any
	require.NoError(t, json.Unmarshal(sentBody, &bodyDecoded),
		"send-node body must marshal as JSON object")
	require.Equal(t, "needs_work", bodyDecoded["pong_status"],
		"send-node body must reflect the substituted upstream attribute value; got %v",
		bodyDecoded)

	frames := getFrames(t, h.ControlBase, iid, sentMsgID)
	require.NotEmpty(t, frames,
		"no frame carries triggering_message_id = %s (the cascade-send envelope)",
		sentMsgID)
	require.Equal(t, "ping/recheck", frames[0].MessageType,
		"the cascade-opened frame must carry the sent message type on its join")
}

func TestStoryCascadeSend_SchemaMismatchRejectsAtRegistration(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @concept: message-sender-node
	specMap := map[string]any{
		"name":    "story-cascade-send-mismatch",
		"version": "1",
		"messages": []map[string]any{{
			"type": "ping/recheck",
			"body_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pong_status": map[string]any{"type": "string"},
				},
				"required": []any{"pong_status"},
			},
		}},
		"nodes": []map[string]any{{
			"type":          "bad-sender",
			"sends_message": "ping/recheck",
			"attributes": map[string]any{
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pong_status":  map[string]any{"type": "string"},
						"sneaky_extra": map[string]any{"type": "string"},
					},
					"required": []any{"pong_status"},
				},
			},
		}},
	}
	body, err := json.Marshal(map[string]any{"spec": specMap})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, h.ControlBase+"/v1/templates",
		bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"superset attribute schema must reject at registration; got %d body=%s",
		resp.StatusCode, string(raw))
	require.Contains(t, string(raw), "sneaky_extra",
		"rejection diagnostic must name the offending superset field")
}
