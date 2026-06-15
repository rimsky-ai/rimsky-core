// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// idempotency_matrix_test.go — the consolidated per-status acceptance
// matrix for POST /instances/{id}/messages (story
// S-control-api-mcp-idempotency-status-matrix).
//
// Every idempotency and publisher-capability outcome is pinned to its
// exact HTTP status by its own named sub-test, each driving a REAL
// request through the REAL controlapi handler (httptest server + real
// Postgres via pgtest — the value-delivering component, not a fake).
// The matrix is the regression lock the 2026-05-17 plan (Tasks 12 & 32)
// demanded: flipping any single status in handleCreateMessage reddens
// exactly its sub-test.
//
// Each sub-test is self-contained (its own instance, where state would
// otherwise bleed) so a single flipped status cannot ripple across
// sibling sub-cases — the spec's "exactly its test red" property.

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

// TestIdempotencyMatrix pins every idempotency / publisher-capability
// HTTP status to its own named sub-case against the real handler.
//
// Status map under test:
//   - first-insert            → 201
//   - same-key replay         → 200, identical message_id, no 2nd envelope
//   - missing Idempotency-Key → 400 (regression anchor for CLICTRL-2)
//   - distinct-sender same key→ both 201, distinct ids, two envelopes
//   - active publisher sub    → 201
//   - stopped publisher sub   → 403
//   - unknown publisher sub   → 403
//   - wrong-instance sub      → 403
//   - missing publisher_subscription_id → 400
func TestIdempotencyMatrix(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	// @constraint: messageCount reads the live envelope count for an instance off the
	// real GET /instances/{id}/messages projection — the persisted-side
	// surface the spec names ("replay leaves a single envelope",
	// "distinct-sender yields two envelopes").
	messageCount := func(t *testing.T, instID string) int {
		t.Helper()
		status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages", instID), nil)
		require.Equal(t, http.StatusOK, status, out)
		msgs, _ := out["messages"].([]any)
		return len(msgs)
	}

	// @constraint: first-insert → 201. A keyed operator emit creates exactly one
	// envelope; capture the message_id for the replay sub-case below.
	t.Run("first_insert_201", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "first-insert")
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{"kind": "invalidate", "target": "root"},
			map[string]string{"Idempotency-Key": "first-" + uuid.NewString()})
		require.Equal(t, http.StatusCreated, resp.status, resp.body)
		msgID, _ := resp.body["message_id"].(string)
		require.NotEmpty(t, msgID, "first insert must return a message_id")
		require.Equal(t, 1, messageCount(t, instID), "first insert persists exactly one envelope")
	})

	// @constraint: same-key replay → 200 with the identical message_id and NO second
	// envelope (replay leaves a single envelope).
	t.Run("replay_200_same_id", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "replay")
		key := "replay-" + uuid.NewString()
		body := map[string]any{"kind": "invalidate", "target": "root"}

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

	// @constraint: missing Idempotency-Key → 400. This is the regression anchor: it
	// fails (returns 201) against the pre-CLICTRL-2 handler that gated
	// dedup behind `if idempotencyKey != ""` with no required-header
	// guard. The diagnostic must name the header. Nothing persists.
	t.Run("missing_key_400", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "missing-key")
		status, out := h.httpJSON(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{"kind": "invalidate", "target": "root"})
		require.Equal(t, http.StatusBadRequest, status, out)
		errMsg, _ := out["error"].(string)
		require.Contains(t, strings.ToLower(errMsg), "idempotency-key",
			"the rejection diagnostic must name the required header")
		require.Equal(t, 0, messageCount(t, instID),
			"a rejected keyless emit must persist no envelope")
	})

	// @constraint: distinct-sender same key → both 201, distinct ids. The dedup tuple
	// is (instance, sender, key); operator vs publisher resolve to
	// different senders, so the shared key cannot collide. Two envelopes
	// result.
	t.Run("distinct_sender_no_collision", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "distinct-sender")
		subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)
		key := "shared-" + uuid.NewString()

		operator := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{"kind": "invalidate", "target": "root"},
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, operator.status, operator.body)
		operatorID, _ := operator.body["message_id"].(string)

		publisher := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"kind":                      "invalidate",
				"target":                    "root",
				"sender_kind":               "publisher",
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

	// @constraint: publisher-named-"operator" same key → no collision with the
	// operator-side emit. The dedup tuple now carries a structural
	// sender_kind discriminator ("operator" vs "publisher") so a
	// publisher whose operator-chosen publisher_name happens to be the
	// literal `"operator"` cannot share a dedup tuple with operator-side
	// emits — the `sender` column alone is no longer load-bearing for
	// cross-source isolation.
	t.Run("publisher_named_operator_no_collision", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "pub-op")
		// @constraint: operator-chosen publisher_name == "operator" — the exact
		// collision shape sender_kind exists to prevent.
		subID := insertPublisherSubscription(t, h, instID, "operator", persistence.PublisherSubscriptionStateActive)
		key := "pub-op-" + uuid.NewString()

		operator := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{"kind": "invalidate", "target": "root"},
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, operator.status, operator.body)
		operatorID, _ := operator.body["message_id"].(string)

		publisher := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"kind":                      "invalidate",
				"target":                    "root",
				"sender_kind":               "publisher",
				"publisher_subscription_id": subID,
			},
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusCreated, publisher.status,
			"publisher named \"operator\" + same key must NOT replay the operator emit — sender_kind discriminator must hold")
		publisherID, _ := publisher.body["message_id"].(string)
		require.NotEqual(t, operatorID, publisherID,
			"distinct sender_kind → distinct message ids; publisher named \"operator\" must not inherit the operator's dedup row")

		require.Equal(t, 2, messageCount(t, instID),
			"publisher-named-operator + same key as operator yields two envelopes (no false-replay collision)")
	})

	// @constraint: active publisher subscription → 201. The capability check passes
	// when the sub is active and bound to this instance.
	t.Run("active_sub_success", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "active-sub")
		subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"kind":                      "invalidate",
				"target":                    "root",
				"sender_kind":               "publisher",
				"publisher_subscription_id": subID,
			},
			map[string]string{"Idempotency-Key": "active-" + uuid.NewString()})
		require.Equal(t, http.StatusCreated, resp.status, resp.body)
		require.Equal(t, 1, messageCount(t, instID), "active-sub emit persists one envelope")
	})

	// @constraint: stopped publisher subscription → 403. Carries a key so the 403 is
	// the capability reason, not the header guard.
	t.Run("stopped_sub_403", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "stopped-sub")
		subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateStopped)
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"kind":                      "invalidate",
				"target":                    "root",
				"sender_kind":               "publisher",
				"publisher_subscription_id": subID,
			},
			map[string]string{"Idempotency-Key": "stopped-" + uuid.NewString()})
		require.Equal(t, http.StatusForbidden, resp.status, resp.body)
		require.Equal(t, 0, messageCount(t, instID), "a rejected emit persists no envelope")
	})

	// @constraint: unknown publisher subscription → 403 (not 404 — avoid leaking
	// subscription-id existence). Carries a key.
	t.Run("unknown_sub_403", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "unknown-sub")
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"kind":                      "invalidate",
				"target":                    "root",
				"sender_kind":               "publisher",
				"publisher_subscription_id": uuid.NewString(),
			},
			map[string]string{"Idempotency-Key": "unknown-" + uuid.NewString()})
		require.Equal(t, http.StatusForbidden, resp.status, resp.body)
		require.Equal(t, 0, messageCount(t, instID), "a rejected emit persists no envelope")
	})

	// @constraint: wrong-instance subscription → 403. A sub bound to instance A cannot
	// emit into instance B. Carries a key.
	t.Run("wrong_instance_403", func(t *testing.T) {
		instA := newInstanceForMessages(t, h, "wrong-a")
		instB := newInstanceForMessages(t, h, "wrong-b")
		subForA := insertPublisherSubscription(t, h, instA, "sensor-http", persistence.PublisherSubscriptionStateActive)
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instB),
			map[string]any{
				"kind":                      "invalidate",
				"target":                    "root",
				"sender_kind":               "publisher",
				"publisher_subscription_id": subForA,
			},
			map[string]string{"Idempotency-Key": "wrong-" + uuid.NewString()})
		require.Equal(t, http.StatusForbidden, resp.status, resp.body)
		require.Equal(t, 0, messageCount(t, instB), "a rejected emit persists no envelope in instance B")
	})

	// @constraint: missing publisher_subscription_id → 400. A publisher-kind sender
	// without a subscription id is rejected at body validation (before
	// the tx). Carries a key so the 400 is the missing-sub-id reason, not
	// the header guard.
	t.Run("missing_sub_id_400", func(t *testing.T) {
		instID := newInstanceForMessages(t, h, "missing-sub-id")
		resp := h.httpJSONWithHeaders(t, "POST",
			fmt.Sprintf("/v1/instances/%s/messages", instID),
			map[string]any{
				"kind":        "invalidate",
				"target":      "root",
				"sender_kind": "publisher",
			},
			map[string]string{"Idempotency-Key": "missing-sub-" + uuid.NewString()})
		require.Equal(t, http.StatusBadRequest, resp.status, resp.body)
		errMsg, _ := resp.body["error"].(string)
		require.Contains(t, strings.ToLower(errMsg), "publisher_subscription_id",
			"the rejection diagnostic must name the required field")
		require.Equal(t, 0, messageCount(t, instID), "a rejected emit persists no envelope")
	})
}
