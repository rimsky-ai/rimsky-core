// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: typed-message-substitution
// @concept: message-schema
package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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
	require.Contains(t, string(raw), "not_a_real_field",
		"rejection diagnostic must name the typo'd field; body=%s", string(raw))
}

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
			"sends_message": "ping/recheck",
			"attributes": map[string]any{
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pong_status": map[string]any{"type": "string"},
						// @concept: message-sender-node
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

func TestStoryTypedMessageSubstitution_RuntimeResolutionThroughBackEdge(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

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
						{Node: "ping/recheck", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
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

	expectedReason := "operator-said-recheck"
	resp := postMessage(t, h.ControlBase, iid, map[string]any{
		"type":    "ping/recheck",
		"payload": map[string]any{"reason": expectedReason},
	}, "key-runtime-"+uuid.NewString())
	require.Truef(t, resp.status == http.StatusOK || resp.status == http.StatusCreated,
		"message POST must succeed; status=%d body=%s", resp.status, string(resp.raw))

	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, receiver)
	h.WaitForEventCount(receiver.ID, "terminal/success", 1)

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
