// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
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

	t.Run("concurrent_same_key_dedups_to_one_envelope", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "concurrent-key")
		key := "concurrent-" + uuid.NewString()
		body := map[string]any{"type": "system/invalidate"}

		const concurrency = 8
		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			statuses  []int
			messageID []string
		)
		wg.Add(concurrency)
		for i := 0; i < concurrency; i++ {
			go func() {
				defer wg.Done()
				resp := h.httpJSONWithHeaders(t, "POST",
					fmt.Sprintf("/v1/instances/%s/messages", instID), body,
					map[string]string{"Idempotency-Key": key})
				mu.Lock()
				defer mu.Unlock()
				statuses = append(statuses, resp.status)
				if id, ok := resp.body["message_id"].(string); ok {
					messageID = append(messageID, id)
				}
			}()
		}
		wg.Wait()

		require.Len(t, statuses, concurrency)
		created := 0
		for _, s := range statuses {
			require.Contains(t, []int{http.StatusCreated, http.StatusOK}, s,
				"every concurrent send with the same key must succeed as either the insert or a replay")
			if s == http.StatusCreated {
				created++
			}
		}
		require.Equal(t, 1, created,
			"exactly one concurrent POST may win the InsertOrLookup race; the rest must observe the unique-violation and replay")

		require.Len(t, messageID, concurrency)
		for _, id := range messageID {
			require.Equal(t, messageID[0], id, "every concurrent sender must observe the same winning message_id")
		}
		require.Equal(t, 1, messageCount(t, instID),
			"concurrent same-key sends must dedup to a single persisted envelope")
	})

	t.Run("cross_instance_same_key_no_collision", func(t *testing.T) {
		instA := newInstanceForMessages(t, h, "cross-inst-a")
		instB := newInstanceForMessages(t, h, "cross-inst-b")
		key := "cross-inst-" + uuid.NewString()
		body := map[string]any{"type": "system/invalidate"}

		respA := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instA), body,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, respA.status, respA.body)
		idA, _ := respA.body["message_id"].(string)
		require.NotEmpty(t, idA)

		respB := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instB), body,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, respB.status, respB.body,
			"the same (sender, key) tuple against a different instance must not replay across instances")
		idB, _ := respB.body["message_id"].(string)
		require.NotEmpty(t, idB)

		require.NotEqual(t, idA, idB, "distinct instance -> distinct message ids for the same idempotency key")
		require.Equal(t, 1, messageCount(t, instA), "instance A gets its own envelope")
		require.Equal(t, 1, messageCount(t, instB), "instance B gets its own envelope, not a cross-instance replay")
	})

	t.Run("replay_after_termination_returns_original_id", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "replay-terminated")
		key := "replay-terminated-" + uuid.NewString()
		body := map[string]any{"type": "system/invalidate"}

		first := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID), body,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, first.status, first.body)
		firstID, _ := first.body["message_id"].(string)
		require.NotEmpty(t, firstID)

		termStatus, termOut := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/terminate", instID), map[string]any{})
		require.Equal(t, http.StatusOK, termStatus, termOut)

		replay := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID), body,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusOK, replay.status,
			"a retry of an already-accepted send must replay with the original message_id (200), "+
				"not fail as if it were a fresh send to a terminated instance (409)")
		replayID, _ := replay.body["message_id"].(string)
		require.Equal(t, firstID, replayID, "replay after termination must return the original message_id")

		require.Equal(t, 1, messageCount(t, instID),
			"replay after termination must not create a second envelope")

		fresh := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID), body,
			map[string]string{"Idempotency-Key": "fresh-after-terminate-" + uuid.NewString()})
		require.Equal(t, http.StatusConflict, fresh.status, fresh.body,
			"a genuinely new send (distinct key) to a terminated instance must still be refused")
	})
}
