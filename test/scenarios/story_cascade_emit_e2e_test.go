// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: cascade-emit
// @concept: message-emitter-node
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

func TestStoryCascadeEmit_EmitsAndOpensNextFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("pong").Success(map[string]any{"status": "needs_work"}, true, "produced status")

	h.Stub.WhenType("tail").Success(map[string]any{"observed": "ok"}, true, "saw cascade-emit")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-cascade-emit", Version: "1",
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
						{Node: "initial/wakeup", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
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
					Type:         "emitter",
					EmitsMessage: "ping/recheck",
					Subscribes: []node.SubscriptionEntry{
						{Node: "pong", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
						{Node: "pong", Type: "attribute/status/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
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
						{Node: "ping/recheck", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
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

	iid := h.CreateInstance(tid, "ck-story-cascade-emit", map[string]any{})
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
	pongNode := h.FindNode(iid, "pong")
	require.NotNil(t, pongNode)
	emitterNode := h.FindNode(iid, "emitter")
	require.NotNil(t, emitterNode)

	require.True(t,
		h.WaitForEventKind(tailNode.ID, "terminal/success", 30*time.Second),
		"tail did not emit terminal/success — cascade-emit pipeline broken (pong → emitter → emit-message → tail)")
	_ = pongNode
	_ = emitterNode

	var emittedMsgID, emittedSender, emittedSenderKind string
	var emittedBody []byte
	h.QueryRowSQL(
		`SELECT id::text, sender, sender_kind, payload
		   FROM rimsky_messages
		  WHERE instance_id = $1
		    AND type = 'ping/recheck'
		  ORDER BY received_at DESC
		  LIMIT 1`,
		[]any{iid}, &emittedMsgID, &emittedSender, &emittedSenderKind, &emittedBody)
	require.NotEmpty(t, emittedMsgID, "no cascade-emit envelope landed in the ledger")
	require.Equal(t, "instance", emittedSenderKind,
		"cascade-emit must carry sender_kind=instance per concept:message-emitter-node")
	require.True(t, strings.HasPrefix(emittedSender, "instance:"),
		"cascade-emit sender must be instance:<id>, got %q", emittedSender)

	var bodyDecoded map[string]any
	require.NoError(t, json.Unmarshal(emittedBody, &bodyDecoded),
		"emit-node body must marshal as JSON object")
	require.Equal(t, "needs_work", bodyDecoded["pong_status"],
		"emit-node body must reflect the substituted upstream attribute value; got %v",
		bodyDecoded)

	frames := getFrames(t, h.ControlBase, iid, emittedMsgID)
	require.NotEmpty(t, frames,
		"no frame carries triggering_message_id = %s (the cascade-emit envelope)",
		emittedMsgID)
	require.Equal(t, "ping/recheck", frames[0].MessageType,
		"the cascade-opened frame must carry the emit-message-type on its join")
}

func TestStoryCascadeEmit_SchemaMismatchRejectsAtRegistration(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @concept: message-emitter-node
	specMap := map[string]any{
		"name":    "story-cascade-emit-mismatch",
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
			"type":          "bad-emitter",
			"emits_message": "ping/recheck",
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
