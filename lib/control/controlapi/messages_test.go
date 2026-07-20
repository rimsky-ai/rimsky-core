// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestMessages_PostListGet(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("msg-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":    "system/invalidate",
		"payload": map[string]any{"reason": "manual"},
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID)

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages?type=system/invalidate", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.GreaterOrEqual(t, len(msgs), 1)
	first := msgs[0].(map[string]any)
	require.Equal(t, "system/invalidate", first["type"])
	require.Equal(t, "operator", first["sender"])
	require.Equal(t, "operator", first["sender_kind"])

	status, out = h.httpJSON(t, "GET", "/v1/messages/"+msgID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, msgID, out["id"])
	require.Equal(t, instID, out["instance_id"])

	mid, err := uuid.Parse(msgID)
	require.NoError(t, err)
	row, err := h.persist.Messages().Get(ctx, shared.UUID(mid))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "system/invalidate", row.Type)
	require.Equal(t, "operator", row.SenderKind)
}

func TestMessages_ListByFrameID(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "frame-filter")

	post := func() string {
		resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
			"type": "system/invalidate",
		}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
		require.Equal(t, http.StatusCreated, resp.status, resp.body)
		id, _ := resp.body["message_id"].(string)
		require.NotEmpty(t, id)
		return id
	}
	deliveredID := post()
	_ = post()

	mid, err := uuid.Parse(deliveredID)
	require.NoError(t, err)
	var frameID shared.UUID
	rootScope := mainRunScopeIDForInstance(t, h, shared.UUID(mustParseUUID(t, instID)))
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := h.persist.Frames().InsertRunningFrame(ctx,
			shared.UUID(mustParseUUID(t, instID)), shared.UUID(mid), rootScope, tx)
		if err != nil {
			return err
		}
		frameID = fid
		ok, err := h.persist.Messages().MarkDelivered(ctx, tx, shared.UUID(mid), frameID, time.Now().UTC())
		if err != nil {
			return err
		}
		require.True(t, ok, "MarkDelivered should update exactly one row")
		return nil
	}))

	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?frame_id=%s", instID, frameID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1, "frame filter must narrow to the one delivered message")
	got := msgs[0].(map[string]any)
	require.Equal(t, deliveredID, got["id"])
	require.Equal(t, frameID.String(), got["frame_id"])

	status, out = h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?frame_id=%s", instID, uuid.NewString()), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ = out["messages"].([]any)
	require.Empty(t, msgs, "a frame with no delivered message returns zero")

	status, _ = h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?frame_id=not-a-uuid", instID), nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMessages_PostRejectsMissingIdempotencyKey(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("msg-bad-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-bad-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)

	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type": "some-other-kind",
	})
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMessages_TargetTerminatedInstanceConflict(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("msg-term-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-term-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Instances().MarkTerminated(ctx, shared.UUID(instUUID), tx)
	}))

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type": "system/invalidate",
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusConflict, resp.status)
}

func newInstanceForMessages(t *testing.T, h *harness, tag string) string {
	t.Helper()
	tplBody := validTemplateBody("msg-pub-" + tag + "-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-pub-ck-" + tag + "-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	id, _ := out["instance_id"].(string)
	require.NotEmpty(t, id)
	return id
}

func insertPublisherSubscription(t *testing.T, h *harness, instanceID string, publisherName, state string) string {
	t.Helper()
	instUUID, err := uuid.Parse(instanceID)
	require.NoError(t, err)
	subID := shared.UUID(uuid.New())
	require.NoError(t, h.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.PublisherSubscriptions().Insert(ctx, tx, persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     shared.UUID(instUUID),
			PublisherName:  publisherName,
			Kind:           "http",
			ResolvedConfig: []byte(`{"url":"https://example.invalid"}`),

			MessageType: "system/invalidate",
			State:       state,
		})
	}))
	return subID.String()
}

func TestCreateMessage_PublisherSubscriptionActiveSucceeds(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "active")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": subID,
		"sender":                    "ignored-by-trust",
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID)

	mid, err := uuid.Parse(msgID)
	require.NoError(t, err)
	row, err := h.persist.Messages().Get(ctx, shared.UUID(mid))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "publisher", row.SenderKind)
	require.Equal(t, "sensor-http", row.Sender, "sender must be derived from publisher_name, not body")
}

func TestCreateMessage_PublisherSubscriptionDedupScopedBySubscriptionID(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "pub-dedup-scope")
	subA := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)
	subB := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)

	idemKey := "shared-idem-key-" + uuid.NewString()

	respA := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": subA,
	}, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, respA.status, respA.body)
	msgA, _ := respA.body["message_id"].(string)
	require.NotEmpty(t, msgA)

	respB := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": subB,
	}, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, respB.status, respB.body,
		"a distinct live publisher subscription of the same publisher name, sending under the same "+
			"idempotency key, must not be dropped as a replay of subscription A's send")
	msgB, _ := respB.body["message_id"].(string)
	require.NotEmpty(t, msgB)
	require.NotEqual(t, msgA, msgB,
		"two live subscriptions of the same publisher name must not share a dedup namespace; "+
			"the subscription id must be recorded on the dedup row as sender_subject")

	respAReplay := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": subA,
	}, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusOK, respAReplay.status, respAReplay.body,
		"a genuine retry from the same subscription with the same idempotency key must still replay")
	msgAReplay, _ := respAReplay.body["message_id"].(string)
	require.Equal(t, msgA, msgAReplay, "the replay must return subscription A's original message_id")
}

func TestCreateMessage_PublisherSubscriptionStoppedForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "stopped")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateStopped)

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": subID,
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
}

func TestCreateMessage_PublisherSubscriptionUnknownForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "unknown")
	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": uuid.NewString(),
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
}

func TestCreateMessage_PublisherSubscriptionWrongInstanceForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instA := newInstanceForMessages(t, h, "wrong-a")
	instB := newInstanceForMessages(t, h, "wrong-b")
	subForA := insertPublisherSubscription(t, h, instA, "sensor-http", persistence.PublisherSubscriptionStateActive)

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instB), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": subForA,
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
}

func TestCreateMessage_MissingIdempotencyKeyRejected(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "no-idem-key")

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type": "system/invalidate",
	})
	require.Equal(t, http.StatusBadRequest, status, out)
	errMsg, _ := out["error"].(string)
	require.Contains(t, strings.ToLower(errMsg), "idempotency-key",
		"the rejection diagnostic must name the required header")

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages?type=system/invalidate", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Empty(t, msgs, "a rejected keyless emit must persist no invalidate envelope")
}

func TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "idem-dup")
	idemKey := "idem-key-" + uuid.NewString()
	body := map[string]any{
		"type": "system/invalidate",
	}
	first := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		body, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, first.status, first.body)
	firstID, _ := first.body["message_id"].(string)
	require.NotEmpty(t, firstID)
	second := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		body, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusOK, second.status, "replay returns 200 OK")
	secondID, _ := second.body["message_id"].(string)
	require.Equal(t, firstID, secondID, "replay returns the original message_id")
}

func TestMCPMessageSend_CallerSuppliedIdempotencyKeyReplaysInsteadOfDoubleSending(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)

	status, out := h.http(t, "POST", "/v1/templates", admin, validTemplateBody("mcp-msg-idem-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.http(t, "POST", "/v1/templates/"+tplID+"/deploy", admin, map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out = h.http(t, "POST", "/v1/instances", admin, map[string]any{
		"template":     tplID,
		"instance_key": "mcp-msg-idem-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	idemKey := "mcp-caller-key-" + uuid.NewString()
	callSend := func() map[string]any {
		rpcBody := map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{
				"name": "message_send",
				"arguments": map[string]any{
					"id":              instID,
					"type":            "system/invalidate",
					"idempotency_key": idemKey,
				},
			},
		}
		status, out := h.http(t, "POST", "/v1/mcp", admin, rpcBody)
		require.Equal(t, http.StatusOK, status, out)
		result, ok := out["result"].(map[string]any)
		require.True(t, ok, "expected JSON-RPC result envelope: %v", out)
		content, ok := result["content"].([]any)
		require.True(t, ok && len(content) > 0, "expected result.content: %v", result)
		first, _ := content[0].(map[string]any)
		text, _ := first["text"].(string)
		var toolResult map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &toolResult))
		return toolResult
	}

	first := callSend()
	require.NotEqual(t, true, first["isError"], "first send must succeed: %v", first)
	firstID, _ := first["message_id"].(string)
	require.NotEmpty(t, firstID)

	second := callSend()
	require.NotEqual(t, true, second["isError"], "replay of the same idempotency_key must succeed, not error: %v", second)
	secondID, _ := second["message_id"].(string)
	require.Equal(t, firstID, secondID,
		"an MCP message_send retry carrying the same caller-supplied idempotency_key must replay the "+
			"original send (same message_id), not mint a fresh Idempotency-Key and double-send")

	status, out = h.http(t, "GET", fmt.Sprintf("/v1/instances/%s/messages?type=system/invalidate", instID), admin, nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1, "a replayed send must not enqueue a second message envelope")
}

func TestCreateMessage_DeclaredTypeAccepted(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "declared-ok")
	resp := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": "system/invalidate"},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID)
}

func TestCreateMessage_UndeclaredTypeRefused(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "undeclared")

	resp := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": "ping/recheck"},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, resp.status, resp.body)
	require.Equal(t, "unknown message type", resp.body["error"])
	require.Equal(t, "ping/recheck", resp.body["type"])
	declared, ok := resp.body["declared_types"].([]any)
	require.True(t, ok, "declared_types must be a JSON array, got %+v", resp.body)
	require.ElementsMatch(t, []any{"system/invalidate"}, declared)

	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?type=ping/recheck", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Empty(t, msgs, "rejected receipt must persist no envelope")
}

func TestCreateMessage_IdempotencyKeyDistinctSendersDoNotCollide(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "idem-sender")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)
	idemKey := "shared-key-" + uuid.NewString()

	first := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": "system/invalidate"},
		map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, first.status)
	firstID, _ := first.body["message_id"].(string)

	second := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{
			"type":                      "system/invalidate",
			"publisher_subscription_id": subID,
		},
		map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, second.status,
		"different sender → no dedup collision")
	secondID, _ := second.body["message_id"].(string)
	require.NotEqual(t, firstID, secondID, "different sender → distinct message ids")
}

func templateBodyWithMessageSchema(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
			"messages": []map[string]any{
				{
					"type": "ping/recheck",
					"body_schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"pong_status": map[string]any{"type": "string"},
						},
						"required":             []string{"pong_status"},
						"additionalProperties": false,
					},
				},
			},
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
			},
		},
	}
}

func newInstanceWithMessageSchema(t *testing.T, h *harness, tag string) string {
	t.Helper()
	tplBody := templateBodyWithMessageSchema("msg-schema-" + tag + "-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-schema-ck-" + tag + "-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	id, _ := out["instance_id"].(string)
	require.NotEmpty(t, id)
	return id
}

// @concept: message-schema
func TestCreateMessage_RejectsPayloadFailingBodySchema(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceWithMessageSchema(t, h, "schema-fail")

	resp := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{
			"type":    "ping/recheck",
			"payload": map[string]any{},
		},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, resp.status, resp.body)
	errText := fmt.Sprint(resp.body["error"])
	require.Contains(t, errText, "ping/recheck",
		"rejection must name the offending message type; body: %v", resp.body)
	require.Contains(t, errText, "pong_status",
		"rejection must name the offending field; body: %v", resp.body)

	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	rows, _ := out["messages"].([]any)
	require.Empty(t, rows, "a body-schema-rejected message must not be persisted to the ledger")
}

func TestCreateMessage_AcceptsPayloadMatchingBodySchema(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceWithMessageSchema(t, h, "schema-ok")

	resp := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{
			"type":    "ping/recheck",
			"payload": map[string]any{"pong_status": "needs_work"},
		},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID)
}

// @story: empty-message-wakes-roots
// @decision: empty-message-as-root-trigger
func TestCreateMessage_EmptyTypeAdmittedAsImplicitEntry(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "empty-admit")

	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	beforeMsgs, _ := out["messages"].([]any)
	require.Empty(t, beforeMsgs, "instance creation is idle; ledger must be empty before the empty-type emit")

	resp := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": ""},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID, "201 must carry a message_id")

	mid, err := uuid.Parse(msgID)
	require.NoError(t, err)
	row, err := h.persist.Messages().Get(ctx, shared.UUID(mid))
	require.NoError(t, err)
	require.NotNil(t, row, "the empty-typed envelope must be persisted")
	require.Equal(t, "", row.Type, "the row's type must be exactly the empty string")

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1, "the empty-typed emit must persist exactly one envelope")
	first := msgs[0].(map[string]any)
	require.Equal(t, "", first["type"], "the GET projection must echo type=\"\"")

	h.tickFrameEngine(t)

	status, framesOut := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/frames", instID), nil)
	require.Equal(t, http.StatusOK, status, framesOut)
	frames, _ := framesOut["frames"].([]any)
	require.GreaterOrEqual(t, len(frames), 1, "the empty-typed emit must open at least one frame")
	frame := frames[0].(map[string]any)
	require.Equal(t, msgID, frame["triggering_message_id"],
		"the frame's triggering_message_id must point at the empty-typed envelope")
}

// @story: empty-message-wakes-roots
// @decision: empty-message-as-root-trigger
func TestCreateMessage_UndeclaredTypeRefused_SurfacesImplicitTypes(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "implicit-types")

	resp := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": "ping/recheck"},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, resp.status, resp.body)
	require.Equal(t, "unknown message type", resp.body["error"])
	require.Equal(t, "ping/recheck", resp.body["type"])

	implicit, ok := resp.body["implicit_types"].([]any)
	require.True(t, ok, "implicit_types must be a JSON array, got %+v", resp.body)
	require.ElementsMatch(t, []any{""}, implicit,
		"the 400-body must advertise the runtime-implicit empty-type so an operator inspecting the response sees the admissible empty entry")
}

func TestMessages_ListCursorPaginationPagesPastFirstWindow(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "cursor-page")
	const total = 7
	want := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		want[postMessageForTest(t, h, instID)] = true
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < total*2; pages++ {
		path := fmt.Sprintf("/v1/instances/%s/messages?limit=3", instID)
		if cursor != "" {
			path += "&cursor=" + neturl.QueryEscape(cursor)
		}
		status, out := h.httpJSON(t, "GET", path, nil)
		require.Equal(t, http.StatusOK, status, out)
		msgs, _ := out["messages"].([]any)
		for _, m := range msgs {
			item, _ := m.(map[string]any)
			id, _ := item["id"].(string)
			require.False(t, seen[id], "message %s returned more than once across pages", id)
			seen[id] = true
		}
		nextCursor, _ := out["next_cursor"].(string)
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	require.Len(t, seen, total,
		"GET .../messages?cursor= must actually page past the first window; a client following next_cursor "+
			"must not terminate after one page with rows remaining")
	for id := range want {
		require.True(t, seen[id], "message %s never appeared across any page", id)
	}
}

func postMessageForTest(t *testing.T, h *harness, instID string) string {
	t.Helper()
	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": "system/invalidate"},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	id, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, id)
	return id
}

func deliverMessageForTest(t *testing.T, h *harness, instID, msgID string, deliveredAt time.Time) shared.UUID {
	t.Helper()
	ctx := context.Background()
	mid := mustParseUUID(t, msgID)
	rootScope := mainRunScopeIDForInstance(t, h, mustParseUUID(t, instID))
	var frameID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		instUUID := mustParseUUID(t, instID)
		priorRunning, err := h.persist.Frames().GetRunningFrameID(ctx, instUUID, tx)
		if err != nil {
			return err
		}
		if priorRunning != nil {
			if _, err := h.persist.Frames().MarkFrameEnded(ctx, *priorRunning, tx); err != nil {
				return err
			}
		}
		fid, err := h.persist.Frames().InsertRunningFrame(ctx, instUUID, mid, rootScope, tx)
		if err != nil {
			return err
		}
		frameID = fid
		ok, err := h.persist.Messages().MarkDelivered(ctx, tx, mid, frameID, deliveredAt)
		if err != nil {
			return err
		}
		require.True(t, ok, "MarkDelivered should update exactly one row")
		return nil
	}))
	return frameID
}

func TestMessages_ListFilteredByPendingTrue(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "pending-true")
	pendingID := postMessageForTest(t, h, instID)
	deliveredID := postMessageForTest(t, h, instID)
	deliverMessageForTest(t, h, instID, deliveredID, time.Now().UTC())

	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?pending=true", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1, "pending=true must exclude the delivered message")
	got := msgs[0].(map[string]any)
	require.Equal(t, pendingID, got["id"])
}

func TestMessages_ListFilteredByPendingFalse(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "pending-false")
	_ = postMessageForTest(t, h, instID)
	deliveredID := postMessageForTest(t, h, instID)
	deliverMessageForTest(t, h, instID, deliveredID, time.Now().UTC())

	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?pending=false", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1, "pending=false must exclude the still-pending message")
	got := msgs[0].(map[string]any)
	require.Equal(t, deliveredID, got["id"])
}

func TestMessages_ListPendingInvalidReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "pending-invalid")
	status, _ := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?pending=1", instID), nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMessages_ListFilteredByDeliveredAfterAndBefore(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "delivered-window")
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	earlyID := postMessageForTest(t, h, instID)
	deliverMessageForTest(t, h, instID, earlyID, base.Add(-time.Hour))
	inWindowID := postMessageForTest(t, h, instID)
	deliverMessageForTest(t, h, instID, inWindowID, base)
	lateID := postMessageForTest(t, h, instID)
	deliverMessageForTest(t, h, instID, lateID, base.Add(time.Hour))

	after := base.Add(-time.Minute).Format(time.RFC3339)
	before := base.Add(time.Minute).Format(time.RFC3339)
	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?delivered_after=%s&delivered_before=%s", instID, after, before), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1, "delivered_after/delivered_before must narrow to the in-window message")
	got := msgs[0].(map[string]any)
	require.Equal(t, inWindowID, got["id"])
}

func TestMessages_ListDeliveredAfterInvalidReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "delivered-after-invalid")
	status, _ := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?delivered_after=not-a-date", instID), nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMessages_ListDeliveredBeforeInvalidReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "delivered-before-invalid")
	status, _ := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?delivered_before=not-a-date", instID), nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMessages_ListFilteredBySenderKind(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "sender-kind")
	operatorID := postMessageForTest(t, h, instID)

	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?sender_kind=operator", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1)
	got := msgs[0].(map[string]any)
	require.Equal(t, operatorID, got["id"])

	status, out = h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?sender_kind=publisher", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ = out["messages"].([]any)
	require.Empty(t, msgs, "sender_kind filter must exclude non-matching senders")
}

// @concept: observability
func TestMessages_ListFilteredBySenderName(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "sender-name")
	subA := insertPublisherSubscription(t, h, instID, "publisher-a", persistence.PublisherSubscriptionStateActive)
	subB := insertPublisherSubscription(t, h, instID, "publisher-b", persistence.PublisherSubscriptionStateActive)

	respA := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": subA,
	}, map[string]string{"Idempotency-Key": "key-a-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, respA.status, respA.body)
	msgAID, _ := respA.body["message_id"].(string)

	respB := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"publisher_subscription_id": subB,
	}, map[string]string{"Idempotency-Key": "key-b-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, respB.status, respB.body)

	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?sender=publisher-a", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1,
		"?sender= must narrow to one specific publisher, not the whole sender_kind=publisher class")
	got := msgs[0].(map[string]any)
	require.Equal(t, msgAID, got["id"])
	require.Equal(t, "publisher-a", got["sender"])
	require.NotEmpty(t, got["received_at"])

	status, out = h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?sender=publisher-nonexistent", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ = out["messages"].([]any)
	require.Empty(t, msgs, "an unknown sender name must yield an empty result, not an error")
}

func TestDedupSenderKind_AnonymousBucketDistinctFromOperatorAndPublisher(t *testing.T) {
	anon := dedupSenderKind("operator", auth.AnonymousIdentity())
	require.Equal(t, "anonymous", anon,
		"an anonymous-mode identity must dedup-bucket as 'anonymous', not 'operator'")

	op := dedupSenderKind("operator", auth.Identity{})
	require.Equal(t, "operator", op,
		"a non-anonymous identity must dedup-bucket as 'operator'")

	require.NotEqual(t, anon, op,
		"the anonymous and operator idempotency dedup buckets must never collide")

	pub := dedupSenderKind("publisher", auth.AnonymousIdentity())
	require.Equal(t, "publisher", pub,
		"senderKind=publisher must take priority over identity kind even when the identity is anonymous")
}
