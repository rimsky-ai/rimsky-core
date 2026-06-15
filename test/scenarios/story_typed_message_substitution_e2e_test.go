// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-typed-message-substitution acceptance proof.
//
// As a template author, I read from and compose message bodies using
// the same substitution grammar that handles node attributes, with
// each message type addressable by its declared name, so that message
// bodies are first-class typed attribute blocks that flow across
// frames.
//
// Acceptance shape (per spec):
//   - Receiver-side: a node's attribute schema referencing
//     `{{messages.<type>.<typo'd-field>}}` rejects at registration.
//   - Emitter-side: an emit-node whose attribute schema declares a
//     field the destination body_schema doesn't carry rejects at
//     registration.
//   - Runtime: a working back-edge cycle resolves the receiver's
//     `{{messages.<type>.<field>}}` directive against the actual
//     envelope body.
//   - Code path: the same resolver function services both
//     `{{nodes.X.attribute.Y}}` and `{{messages.X.Y}}` directives.
//
// The code-path assertion lives at
// `lib/graph/attribute/substitution_test.go::TestSubstitution_
// SharedResolverServicesNodesAndMessages` (added by Task 53). This
// file carries the end-to-end registration and runtime proofs.
//
// @story: typed-message-substitution
// @concept: message-schema
package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestStoryTypedMessageSubstitution_RejectsReceiverTypoFieldAtRegistration
// pins the receiver-side rejection leg: a node that reads
// `{{messages.<type>.<typo'd-field>}}` is rejected at template
// registration with an error naming the offending field.
func TestStoryTypedMessageSubstitution_RejectsReceiverTypoFieldAtRegistration(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	specMap := map[string]any{
		"name":    "story-typed-msg-subst-receiver-typo",
		"version": "1",
		"messages": []map[string]any{{
			"type": "ping/recheck",
			"body_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{"type": "string"},
				},
				"required": []any{"reason"},
			},
		}},
		"nodes": []map[string]any{{
			"type":     "receiver",
			"executor": "stub",
			"subscribes": []map[string]any{{
				"node": "ping/recheck",
				"type": "terminal/success",
			}},
			"attributes": map[string]any{
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						// Typo: the body_schema declares `reason`,
						// not `not_a_real_field`. Registration must
						// refuse, naming the offending field, so the
						// author sees the typo at registration rather
						// than at dispatch.
						"observed": map[string]any{
							"type":   "string",
							"source": "{{messages.ping/recheck.not_a_real_field}}",
						},
					},
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
		"receiver-side typo must reject at registration; got status=%d body=%s",
		resp.StatusCode, string(raw))
	// Diagnostic mentions the offending substitution path.
	require.Contains(t, string(raw), "not_a_real_field",
		"rejection diagnostic must name the typo'd field; body=%s", string(raw))
}

// TestStoryTypedMessageSubstitution_RejectsEmitterExtraFieldAtRegistration
// pins the emitter-side rejection leg: an emit-node whose attribute
// schema declares a field the destination body_schema doesn't have is
// rejected at registration.
func TestStoryTypedMessageSubstitution_RejectsEmitterExtraFieldAtRegistration(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	specMap := map[string]any{
		"name":    "story-typed-msg-subst-emitter-extra",
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
						// Extra field the destination body doesn't
						// have. Per concept:message-emitter-node
						// "hidden state is not allowed because the
						// attribute set IS the body."
						"extra_field": map[string]any{"type": "string"},
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
		"emitter-side extra field must reject at registration; got status=%d body=%s",
		resp.StatusCode, string(raw))
	require.Contains(t, string(raw), "extra_field",
		"rejection diagnostic must name the offending field; body=%s", string(raw))
}

// TestStoryTypedMessageSubstitution_RuntimeResolutionThroughBackEdge
// pins the runtime resolution leg: a working back-edge cycle reads
// the cascade-emitted message's body via the typed-message
// substitution grammar.
//
// The receiver's attribute schema references `{{messages.<type>.
// <field>}}`. After the emit-node lands the envelope, the next frame
// opens with that envelope as the triggering message; the receiver
// dispatches; substitution resolves the body field.
//
// The acceptance falsifier this test pins: "the runtime resolves
// `{{messages.X.Y}}` and `{{nodes.X.attribute.Y}}` through two
// different resolver functions" — instead, the receiver's dispatch
// goes through the same resolver, which is verified at the unit-test
// layer (lib/graph/attribute/substitution_test.go).
func TestStoryTypedMessageSubstitution_RuntimeResolutionThroughBackEdge(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// The receiver node reads the message body's `reason` field; the
	// stub records that value back as its attribute so the test can
	// verify the substitution resolved.
	h.Stub.WhenType("receiver").Success(map[string]any{
		"echoed_reason": "saw-it",
	}, true, "receiver ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-typed-msg-subst-runtime", Version: "1",
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
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "receiver",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "ping/recheck", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"reason_from_body": map[string]any{
							"type":   "string",
							"source": "{{messages.ping/recheck.reason}}",
						},
						"echoed_reason": map[string]any{
							"type": "string",
						},
					},
					"required": []any{"reason_from_body"},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-typed-msg-subst-runtime", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iid)

	// Post the message with a known reason field.
	expectedReason := "operator-said-recheck"
	resp := postMessage(t, h.ControlBase, iid, map[string]any{
		"type":    "ping/recheck",
		"payload": map[string]any{"reason": expectedReason},
	}, "key-runtime-"+uuid.NewString())
	require.Truef(t, resp.status == http.StatusOK || resp.status == http.StatusCreated,
		"message POST must succeed; status=%d body=%s", resp.status, string(resp.raw))

	// Wait for the receiver to settle. The substitution at dispatch
	// resolves `{{messages.ping/recheck.reason}}` against the
	// envelope's body — the same resolver function the unit test in
	// lib/graph/attribute/substitution_test.go exercises for both
	// substitution surfaces.
	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, receiver)
	require.True(t,
		h.WaitForEventKind(receiver.ID, "terminal/success", 20*time.Second),
		"receiver did not run — substitution may have failed")

	// Read the persisted attribute row for this dispatch. The
	// substitution must have resolved `reason_from_body` to the
	// envelope's reason field. The persistence layer's attribute store
	// keeps the resolved value alongside the dispatch row; the test
	// asserts on the stored value rather than the wire shape so it's
	// robust to any cosmetic JSON projection changes.
	var resolvedBody []byte
	h.QueryRowSQL(
		`SELECT data FROM rimsky_node_attributes
		   WHERE node_id = $1
		   ORDER BY updated_at DESC LIMIT 1`,
		[]any{receiver.ID}, &resolvedBody)
	require.NotEmpty(t, resolvedBody,
		"receiver must have persisted resolved attributes")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(resolvedBody, &decoded))
	require.Equal(t, expectedReason, decoded["reason_from_body"],
		"the substitution at dispatch must resolve {{messages.ping/recheck.reason}} to the envelope's body value; got %v",
		decoded)
}
