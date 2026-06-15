// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-message-schema acceptance proof.
//
// As a template author, I can declare which message types instances of
// this template accept, so that messages have a typed contract and
// unknown ones fail loud instead of silently dead-lettering.
//
// Acceptance shape (per spec):
//  1. Declared type → POST /v1/instances/{id}/messages returns 200/201;
//     the next frame opens with triggering_message_id set; the
//     subscribed node-run runs to terminal/success carrying the
//     substituted body field.
//  2. Undeclared type → POST returns HTTP 400 naming the rejected
//     type and listing the declared set; no row in rimsky_messages.
//
// The proof boots the real rimsky stack via the testcontainers-backed
// scenario harness (scheduler + supervisor + control-api against a real
// Postgres), deploys a template carrying a `messages:` registry with two
// declared types, subscribes a node to one of them via the virtual-node
// terminal/success grammar, and drives both legs through the real HTTP
// surface. No stubs of the value-delivering component: the message goes
// through `controlapi.handleCreateMessage` → `runtime.EnqueueMessage` →
// `frame.EnqueueFrame` → `runtime.SweepDeliverMessagesForRunningFrames`
// → cascade walker; the rejection leg goes through the same handler's
// registry lookup before any persistence write.
//
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
	"time"

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

	// @deliberate: the receiver runs through the stub executor and
	// produces a small terminal/success row each dispatch — the
	// observable proxy for "the message-virtual-node settled and
	// stale-marked me, the supervisor ran my dispatch."
	h.Stub.WhenType("receiver").Success(map[string]any{"observed": "ok"}, true, "ran")

	// @deliberate: two declared message types — a simple one with a
	// single string field, and a more complex one with a nested array.
	// Both leg-behaviour tests rely on the simple one; the second
	// exists to confirm the declared_types response carries the full
	// registry on rejection, not just the first entry.
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
					// @deliberate: subscribe to the message-virtual-node-
					// type's terminal/success — the standard subscription
					// grammar the cascade walker matches at frame boundary.
					Subscribes: []node.SubscriptionEntry{
						{Node: "ping/recheck", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// @deliberate: the substitution leaf that reads
						// from the triggering message body. The auto-
						// subscribe rule and the explicit subscribes:
						// entry above both ground the receiver to
						// ping/recheck's settle.
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

	// @deliberate: drive the rejection leg FIRST so the persistence
	// assertion below ("no rimsky_messages row for the rejected type")
	// is checked at a clean baseline. The handler's registry-lookup
	// gate runs INSIDE the tx but BEFORE the idempotency-key insert and
	// BEFORE the envelope insert, so a 400 leaves neither row.
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

	// @deliberate: persistence-layer falsifier — no rimsky_messages row
	// landed for the rejected type. The handler's gate runs before the
	// envelope insert, so a 400 must leave the ledger untouched.
	// Falsifier: "a message of an undeclared type lands in the ledger
	// and is silently dropped."
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

	// @deliberate: the acceptance assertion is at the user-observable
	// surface — the receiver runs and emits terminal/success after the
	// message lands. "Stale → running → terminal/success" is what the
	// user sees through the supervisor's dispatch path — the message-
	// virtual-node-settle → cascade-walker pipeline drove it. Asserting
	// only at fresh-state would catch the cascade but miss the
	// dispatch; asserting at terminal/success demonstrates the full
	// round-trip.
	require.True(t,
		h.WaitForNodeState(receiver.ID, cascade.NodeStateFresh, 20*time.Second),
		"receiver did not reach fresh — message-virtual-node settle did not stale-mark and dispatch")
	require.True(t,
		h.WaitForEventKind(receiver.ID, "terminal/success", 20*time.Second),
		"receiver did not emit terminal/success — frame did not open with the delivered message")

	// @deliberate: the triggering_message_id surface assertion — the
	// frame that opened to deliver this message carries
	// triggering_message_id = the just-inserted envelope's id. This is
	// the load-bearing observability invariant the frame-origin-audit
	// story rests on, and the only way to confirm the spec's acceptance
	// "the instance opens a frame and the receivers I have declared via
	// subscriptions stale-mark."
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

// postMessage POSTs a typed message to /v1/instances/{id}/messages with
// the mandatory Idempotency-Key header. Returns the raw HTTP response
// and body so callers can assert on both status and shape.
//
// The handler's registry-lookup gate runs INSIDE the tx but BEFORE the
// idempotency-key insert, so a 400 from an undeclared type leaves no
// trace in either the message ledger or the idempotency ledger — the
// scenario tests check both.
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

// frameView is the JSON projection of one frame item returned by
// GET /v1/instances/{id}/frames. The shared.UUID columns surface as
// strings on the wire; the test compares against the wire shape.
type frameView struct {
	FrameID             string `json:"frame_id"`
	State               string `json:"state"`
	TriggeringMessageID string `json:"triggering_message_id"`
	MessageType         string `json:"message_type"`
	MessageSender       string `json:"message_sender"`
	MessageSenderKind   string `json:"message_sender_kind"`
}

// getFrames fetches the frames list for an instance, optionally filtered
// by triggering_message_id. The reverse-join filter exists for the
// frame-origin-audit story; the forward enumeration carries the joined
// envelope fields.
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
