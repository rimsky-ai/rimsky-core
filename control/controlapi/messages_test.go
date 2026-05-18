// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// messages_test.go — F1 + F2 integration tests against the pgtest
// harness (httptest server + real postgres). Each test boots a
// throwaway instance, exercises the messages endpoints, and asserts
// the persisted state matches the expected envelope shape.

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// TestMessages_PostListGet drives the full enqueue → list → detail
// flow on the messages surface. Asserts that the enqueued row carries
// sender=operator + sender_kind=operator + the supplied target/kind.
func TestMessages_PostListGet(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("msg-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	// Post a message targeting `root`.
	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instID), map[string]any{
		"kind":    "invalidate",
		"target":  "root",
		"payload": map[string]any{"reason": "manual"},
	})
	require.Equal(t, http.StatusCreated, status, out)
	msgID, _ := out["message_id"].(string)
	require.NotEmpty(t, msgID)

	// List with kind filter.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/instances/%s/messages?kind=invalidate", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.GreaterOrEqual(t, len(msgs), 1)
	first := msgs[0].(map[string]any)
	require.Equal(t, "invalidate", first["kind"])
	require.Equal(t, "operator", first["sender"])
	require.Equal(t, "operator", first["sender_kind"])

	// Detail.
	status, out = h.httpJSON(t, "GET", "/messages/"+msgID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, msgID, out["id"])
	require.Equal(t, instID, out["instance_id"])

	// Verify persisted row via direct read.
	mid, err := uuid.Parse(msgID)
	require.NoError(t, err)
	row, err := h.persist.Messages().Get(ctx, shared.UUID(mid))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "invalidate", row.Kind)
	require.Equal(t, "operator", row.SenderKind)
	require.Nil(t, row.BackfillOperationID)
}

// TestMessages_PostInvalidKind rejects non-invalidate kinds at the
// boundary so operators see an explicit error instead of a silent
// dead-letter.
func TestMessages_PostInvalidKind(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("msg-bad-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-bad-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)

	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instID), map[string]any{
		"kind": "some-other-kind",
	})
	require.Equal(t, http.StatusBadRequest, status)
}

// TestMessages_TargetTerminatedInstanceConflict — sending a message to
// a terminated instance returns 409.
func TestMessages_TargetTerminatedInstanceConflict(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("msg-term-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-term-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	// Mark instance terminated directly.
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Instances().MarkTerminated(ctx, shared.UUID(instUUID), tx)
	}))

	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instID), map[string]any{
		"kind": "invalidate",
	})
	require.Equal(t, http.StatusConflict, status)
}

// helper — creates a template + instance and returns instance id.
func newInstanceForMessages(t *testing.T, h *harness, tag string) string {
	t.Helper()
	tplBody := validTemplateBody("msg-pub-" + tag + "-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "msg-pub-ck-" + tag + "-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	id, _ := out["instance_id"].(string)
	require.NotEmpty(t, id)
	return id
}

// insertPublisherSubscription seeds a row in
// rimsky_publisher_subscriptions so the capability-check path has
// something to bind to. Returns the new subscription id.
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
			MessageKind:    "invalidate",
			State:          state,
		})
	}))
	return subID.String()
}

// TestCreateMessage_SenderKindPublisherActiveSubscriptionSucceeds — a
// publisher-side message with a valid active subscription is accepted
// and the persisted row carries sender_kind="publisher" plus
// sender=publisher_name (derived from the subscription row, not the
// request body).
func TestCreateMessage_SenderKindPublisherActiveSubscriptionSucceeds(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "active")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instID), map[string]any{
		"kind":                      "invalidate",
		"target":                    "root",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subID,
		"sender":                    "ignored-by-trust", // body sender is overridden
	})
	require.Equal(t, http.StatusCreated, status, out)
	msgID, _ := out["message_id"].(string)
	require.NotEmpty(t, msgID)

	mid, err := uuid.Parse(msgID)
	require.NoError(t, err)
	row, err := h.persist.Messages().Get(ctx, shared.UUID(mid))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "publisher", row.SenderKind)
	require.Equal(t, "sensor-http", row.Sender, "sender must be derived from publisher_name, not body")
}

// TestCreateMessage_SenderKindPublisherStoppedSubscriptionForbidden —
// a publisher-side message against a stopped subscription is rejected
// with 403.
func TestCreateMessage_SenderKindPublisherStoppedSubscriptionForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "stopped")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateStopped)

	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instID), map[string]any{
		"kind":                      "invalidate",
		"target":                    "root",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subID,
	})
	require.Equal(t, http.StatusForbidden, status)
}

// TestCreateMessage_SenderKindPublisherUnknownSubscriptionForbidden —
// a publisher-side message with a non-existent subscription id is
// rejected with 403 (not 404, to avoid leaking subscription-id
// existence to callers without an active row).
func TestCreateMessage_SenderKindPublisherUnknownSubscriptionForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "unknown")
	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instID), map[string]any{
		"kind":                      "invalidate",
		"target":                    "root",
		"sender_kind":               "publisher",
		"publisher_subscription_id": uuid.NewString(),
	})
	require.Equal(t, http.StatusForbidden, status)
}

// TestCreateMessage_SenderKindPublisherWrongInstanceForbidden — a
// subscription tied to instance A cannot be used to send a message to
// instance B.
func TestCreateMessage_SenderKindPublisherWrongInstanceForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instA := newInstanceForMessages(t, h, "wrong-a")
	instB := newInstanceForMessages(t, h, "wrong-b")
	subForA := insertPublisherSubscription(t, h, instA, "sensor-http", persistence.PublisherSubscriptionStateActive)

	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instB), map[string]any{
		"kind":                      "invalidate",
		"target":                    "root",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subForA,
	})
	require.Equal(t, http.StatusForbidden, status)
}

// TestCreateMessage_SenderKindPublisherMissingSubscriptionIDBadRequest
// — a publisher-side message without a subscription id is rejected
// with 400 (capability check is mandatory).
func TestCreateMessage_SenderKindPublisherMissingSubscriptionIDBadRequest(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "missing-sub")
	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instID), map[string]any{
		"kind":        "invalidate",
		"target":      "root",
		"sender_kind": "publisher",
	})
	require.Equal(t, http.StatusBadRequest, status)
}

// TestCreateMessage_SenderKindInvalidBadRequest — unknown sender_kind
// values are rejected with 400.
func TestCreateMessage_SenderKindInvalidBadRequest(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "invalid-kind")
	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/messages", instID), map[string]any{
		"kind":        "invalidate",
		"target":      "root",
		"sender_kind": "sensor", // legacy / unsupported
	})
	require.Equal(t, http.StatusBadRequest, status)
}

// TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting — second
// POST with the same Idempotency-Key returns the original message_id
// with 200 OK and does NOT insert a second envelope.
func TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "idem-dup")
	idemKey := "idem-key-" + uuid.NewString()
	body := map[string]any{
		"kind":   "invalidate",
		"target": "root",
	}
	// First send.
	first := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/instances/%s/messages", instID),
		body, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, first.status, first.body)
	firstID, _ := first.body["message_id"].(string)
	require.NotEmpty(t, firstID)
	// Second send (replay) — must dedup.
	second := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/instances/%s/messages", instID),
		body, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusOK, second.status, "replay returns 200 OK")
	secondID, _ := second.body["message_id"].(string)
	require.Equal(t, firstID, secondID, "replay returns the original message_id")
}

// TestCreateMessage_IdempotencyKeyDistinctSendersDoNotCollide — same
// idempotency-key from operator vs publisher are independent (the
// dedup tuple is (instance, sender, key) — different senders → no
// collision).
func TestCreateMessage_IdempotencyKeyDistinctSendersDoNotCollide(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "idem-sender")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)
	idemKey := "shared-key-" + uuid.NewString()

	// Operator-side send.
	first := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/instances/%s/messages", instID),
		map[string]any{"kind": "invalidate", "target": "root"},
		map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, first.status)
	firstID, _ := first.body["message_id"].(string)

	// Publisher-side send with the SAME idempotency key but a
	// different sender (the subscription's publisher_name).
	second := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/instances/%s/messages", instID),
		map[string]any{
			"kind":                      "invalidate",
			"target":                    "root",
			"sender_kind":               "publisher",
			"publisher_subscription_id": subID,
		},
		map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, second.status,
		"different sender → no dedup collision")
	secondID, _ := second.body["message_id"].(string)
	require.NotEqual(t, firstID, secondID, "different sender → distinct message ids")
}
