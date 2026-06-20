// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestIdempotencyMatrix(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	messageCount := func(t *testing.T, instID string) int {
		t.Helper()
		status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages?type=system/invalidate", instID), nil)
		require.Equal(t, http.StatusOK, status, out)
		msgs, _ := out["messages"].([]any)
		return len(msgs)
	}

	t.Run("first_insert_201", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "first-insert")
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{"type": "system/invalidate"},
			map[string]string{"Idempotency-Key": "first-" + uuid.NewString()})
		require.Equal(t, http.StatusCreated, resp.status, resp.body)
		msgID, _ := resp.body["message_id"].(string)
		require.NotEmpty(t, msgID, "first insert must return a message_id")
		require.Equal(t, 1, messageCount(t, instID), "first insert persists exactly one envelope")
	})

	t.Run("replay_200_same_id", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "replay")
		key := "replay-" + uuid.NewString()
		body := map[string]any{"type": "system/invalidate"}

		first := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID), body,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, first.status, first.body)
		firstID, _ := first.body["message_id"].(string)
		require.NotEmpty(t, firstID)

		second := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID), body,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusOK, second.status, "replay returns 200 OK")
		secondID, _ := second.body["message_id"].(string)
		require.Equal(t, firstID, secondID, "replay returns the original message_id")

		require.Equal(t, 1, messageCount(t, instID),
			"replay must leave a single envelope (no duplicate insert)")
	})

	t.Run("missing_key_400", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "missing-key")
		status, out := h.httpJSON(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{"type": "system/invalidate"})
		require.Equal(t, http.StatusBadRequest, status, out)
		errMsg, _ := out["error"].(string)
		require.Contains(t, strings.ToLower(errMsg), "idempotency-key",
			"the rejection diagnostic must name the required header")
		require.Equal(t, 0, messageCount(t, instID),
			"a rejected keyless emit must persist no envelope")
	})

	t.Run("distinct_sender_no_collision", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "distinct-sender")
		subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)
		key := "shared-" + uuid.NewString()

		operator := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{"type": "system/invalidate"},
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, operator.status, operator.body)
		operatorID, _ := operator.body["message_id"].(string)

		publisher := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"type":                      "system/invalidate",
				"publisher_subscription_id": subID,
			},
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, publisher.status,
			"distinct sender → no dedup collision")
		publisherID, _ := publisher.body["message_id"].(string)
		require.NotEqual(t, operatorID, publisherID, "distinct sender → distinct message ids")

		require.Equal(t, 2, messageCount(t, instID),
			"distinct-sender same-key yields two envelopes")
	})

	t.Run("publisher_named_operator_no_collision", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "pub-op")
		subID := insertPublisherSubscription(t, h, instID, "operator", persistence.PublisherSubscriptionStateActive)
		key := "pub-op-" + uuid.NewString()

		operator := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{"type": "system/invalidate"},
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, operator.status, operator.body)
		operatorID, _ := operator.body["message_id"].(string)

		publisher := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"type":                      "system/invalidate",
				"publisher_subscription_id": subID,
			},
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, publisher.status,
			"publisher named \"operator\" + same key must NOT replay the operator emit — the persistence-layer sender_kind discriminator must hold across the two auth paths")
		publisherID, _ := publisher.body["message_id"].(string)
		require.NotEqual(t, operatorID, publisherID,
			"distinct sender_kind → distinct message ids; publisher named \"operator\" must not inherit the operator's dedup row")

		require.Equal(t, 2, messageCount(t, instID),
			"publisher-named-operator + same key as operator yields two envelopes (no false-replay collision)")
	})

	t.Run("active_sub_success", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "active-sub")
		subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"type":                      "system/invalidate",
				"publisher_subscription_id": subID,
			},
			map[string]string{"Idempotency-Key": "active-" + uuid.NewString()})
		require.Equal(t, http.StatusCreated, resp.status, resp.body)
		require.Equal(t, 1, messageCount(t, instID), "active-sub emit persists one envelope")
	})

	t.Run("stopped_sub_403", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "stopped-sub")
		subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateStopped)
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"type":                      "system/invalidate",
				"publisher_subscription_id": subID,
			},
			map[string]string{"Idempotency-Key": "stopped-" + uuid.NewString()})
		require.Equal(t, http.StatusForbidden, resp.status, resp.body)
		require.Equal(t, 0, messageCount(t, instID), "a rejected emit persists no envelope")
	})

	t.Run("unknown_sub_403", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "unknown-sub")
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"type":                      "system/invalidate",
				"publisher_subscription_id": uuid.NewString(),
			},
			map[string]string{"Idempotency-Key": "unknown-" + uuid.NewString()})
		require.Equal(t, http.StatusForbidden, resp.status, resp.body)
		require.Equal(t, 0, messageCount(t, instID), "a rejected emit persists no envelope")
	})

	t.Run("wrong_instance_403", func(t *testing.T) {
		instA := newInstanceForMessages(t, h, "wrong-a")
		instB := newInstanceForMessages(t, h, "wrong-b")
		subForA := insertPublisherSubscription(t, h, instA, "sensor-http", persistence.PublisherSubscriptionStateActive)
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instB),
			map[string]any{
				"type":                      "system/invalidate",
				"publisher_subscription_id": subForA,
			},
			map[string]string{"Idempotency-Key": "wrong-" + uuid.NewString()})
		require.Equal(t, http.StatusForbidden, resp.status, resp.body)
		require.Equal(t, 0, messageCount(t, instB), "a rejected emit persists no envelope in instance B")
	})

}
