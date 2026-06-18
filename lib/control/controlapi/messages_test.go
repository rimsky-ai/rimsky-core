// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := h.persist.Frames().InsertFrame(ctx,
			shared.UUID(mustParseUUID(t, instID)), shared.UUID(mid), 600000, tx)
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
			TargetNode:     "root",
			MessageType:    "system/invalidate",
			State:          state,
		})
	}))
	return subID.String()
}

func TestCreateMessage_SenderKindPublisherActiveSubscriptionSucceeds(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "active")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"sender_kind":               "publisher",
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

func TestCreateMessage_SenderKindPublisherStoppedSubscriptionForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "stopped")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateStopped)

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subID,
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
}

func TestCreateMessage_SenderKindPublisherUnknownSubscriptionForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "unknown")
	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"sender_kind":               "publisher",
		"publisher_subscription_id": uuid.NewString(),
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
}

func TestCreateMessage_SenderKindPublisherWrongInstanceForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instA := newInstanceForMessages(t, h, "wrong-a")
	instB := newInstanceForMessages(t, h, "wrong-b")
	subForA := insertPublisherSubscription(t, h, instA, "sensor-http", persistence.PublisherSubscriptionStateActive)

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instB), map[string]any{
		"type":                      "system/invalidate",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subForA,
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
}

func TestCreateMessage_SenderKindPublisherMissingSubscriptionIDBadRequest(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "missing-sub")
	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":        "system/invalidate",
		"sender_kind": "publisher",
	})
	require.Equal(t, http.StatusBadRequest, status)
}

func TestCreateMessage_SenderKindInvalidBadRequest(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "invalid-kind")
	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":        "system/invalidate",
		"sender_kind": "sensor",
	})
	require.Equal(t, http.StatusBadRequest, status)
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
			"sender_kind":               "publisher",
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

func TestCreateMessage_AdmitsPayloadFailingBodySchema(t *testing.T) {
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
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID)
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
