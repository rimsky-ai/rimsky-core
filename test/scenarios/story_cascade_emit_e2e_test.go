// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-cascade-emit acceptance proof.
//
// As a template author, I can declare a node-type whose dispatch is to
// emit a message of a given type, so that cross-frame coupling is
// explicit as a graph object I can point at.
//
// Acceptance shape (per spec):
//  1. A node with `emits_message: <type>` and matching attribute
//     schema dispatches when its subscriptions fire.
//  2. The runtime constructs a message envelope with the resolved
//     attributes as the body and inserts it into the message ledger.
//  3. The next frame opens carrying the new envelope's
//     triggering_message_id.
//  4. A template whose emit-node attribute schema declares fields the
//     destination type's body_schema doesn't is rejected at
//     registration.
//
// The proof boots the real rimsky stack and drives the entire chain
// through the supervisor's dispatch path. No stubs of the
// value-delivering component: the emit-node's terminal-resolution goes
// through `applyTerminalComplete` → `emitCascadeMessageInTx` →
// `frame.EnqueueFrame` → the next-tick delivery sweep.
//
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

	// The producer node (`pong`) runs through the stub executor and
	// emits an attribute. Its terminal/success drives the emit-node's
	// subscription, and the emit-node's attribute pulls from
	// {{nodes.pong.attribute.status}} so the resolved body field
	// reflects the upstream's value.
	h.Stub.WhenType("pong").Success(map[string]any{"status": "needs_work"}, true, "produced status")

	// One more receiver subscribed to the cascade-emit message, so the
	// "the next frame opens carrying that message" leg has an observable
	// downstream effect (terminal/success on `tail`).
	h.Stub.WhenType("tail").Success(map[string]any{"observed": "ok"}, true, "saw cascade-emit")

	// Initial trigger uses an "initial/wakeup" message type so the
	// producer node's frame opens cleanly. (The pong node must be
	// stale-marked somehow to wake the cascade; subscribing it to a
	// declared message type is the cheapest, most realistic shape.)
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
					// Pong subscribes to the initial wakeup message so a
					// single POST drives the whole cascade end-to-end.
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
						// The emit-node body field IS the attribute. The
						// attribute pulls from the producer; the runtime
						// JSON-marshals the resolved set into the wire
						// payload.
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

	// Drive the wakeup message that fires pong.
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

	// The emit-node has no executor; it cannot be looked up via the
	// stub. Wait for tail's terminal/success — that's the proof the
	// entire chain ran:
	//   wakeup → pong frame → pong runs → emit-node fires
	//   → cascade-emit message lands → next frame opens
	//   → tail subscribed to ping/recheck stale-marks → tail runs.
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

	// Persistence-layer assertion: a ping/recheck envelope landed in the
	// ledger with the resolved body. The body's pong_status reflects the
	// pong node's attribute via substitution. The body bytes are inert,
	// so the test reads them through the persistence layer rather than
	// re-serializing on the wire.
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

	// The next-frame property: a frame exists whose triggering_message_id
	// is the cascade-emitted envelope. This pins the spec acceptance
	// "the next frame opens carrying that message" — the load-bearing
	// link between the emit-node's dispatch and the receiver's wake.
	frames := getFrames(t, h.ControlBase, iid, emittedMsgID)
	require.NotEmpty(t, frames,
		"no frame carries triggering_message_id = %s (the cascade-emit envelope)",
		emittedMsgID)
	require.Equal(t, "ping/recheck", frames[0].MessageType,
		"the cascade-opened frame must carry the emit-message-type on its join")
}

// TestStoryCascadeEmit_SchemaMismatchRejectsAtRegistration covers the
// spec falsifier:
//
//	"the emit-node's attribute schema can declare fields the destination
//	 message type's body_schema doesn't, without registration-time error"
//
// Drives the rejection through the real `POST /v1/templates` registration
// surface — same handler authors deploying templates touch.
func TestStoryCascadeEmit_SchemaMismatchRejectsAtRegistration(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Emit-node attribute schema declares an extra field the destination
	// `ping/recheck` body_schema does NOT carry. Per concept:message-
	// emitter-node "hidden state is not allowed because the attribute set
	// IS the body," this MUST reject at registration.
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
						"pong_status": map[string]any{"type": "string"},
						// Extra field the destination body doesn't have.
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
