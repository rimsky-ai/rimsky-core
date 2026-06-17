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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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

	// @constraint: post a message targeting `root`. Idempotency-Key is mandatory on
	// every emit, so a successful 201 path must carry one.
	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":    "system/invalidate",
		"payload": map[string]any{"reason": "manual"},
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID)

	// @constraint: list with type filter (kind → type rename under the
	// message-schema-layer redesign).
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

	// @constraint: verify persisted row via direct read.
	mid, err := uuid.Parse(msgID)
	require.NoError(t, err)
	row, err := h.persist.Messages().Get(ctx, shared.UUID(mid))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "system/invalidate", row.Type)
	require.Equal(t, "operator", row.SenderKind)
}

// TestMessages_ListByFrameID pins the ?frame_id= filter on GET
// /instances/{id}/messages — the "what landed in frame X" forensic query
// for fan-out debugging. It returns exactly the messages
// delivered into the named frame, excludes other frames and still-pending
// messages, and 400s on a malformed frame id. The driver-level frame_id
// predicate is conformance-tested on both engines; this pins that the HTTP
// handler threads the query param into MessageListFilter.FrameID.
func TestMessages_ListByFrameID(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "frame-filter")

	post := func() string {
		// @constraint: each emit needs a distinct Idempotency-Key (mandatory header)
		// so the two posts are independent inserts, not a dedup replay.
		resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
			"type": "system/invalidate",
		}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
		require.Equal(t, http.StatusCreated, resp.status, resp.body)
		id, _ := resp.body["message_id"].(string)
		require.NotEmpty(t, id)
		return id
	}
	deliveredID := post()
	// @constraint: left pending (frame_id NULL) — must never match a
	// frame filter.
	_ = post()

	// @constraint: deliver the first message into a real frame row. Post
	// migration 010 the rimsky_messages.frame_id column carries a FK to
	// rimsky_frames(frame_id) ON DELETE SET NULL on BOTH backends (the
	// sqlite migration's column declaration mirrors the postgres
	// ADD CONSTRAINT), so the frame id MUST resolve to a real row or
	// MarkDelivered fails the FK check. Reuse the in-flight message id
	// as the frame's triggering_message_id — InsertFrame requires a
	// real message reference, and the just-posted `deliveredID`
	// satisfies it.
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

	// @constraint: ?frame_id=<frame> → exactly the delivered message.
	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?frame_id=%s", instID, frameID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1, "frame filter must narrow to the one delivered message")
	got := msgs[0].(map[string]any)
	require.Equal(t, deliveredID, got["id"])
	require.Equal(t, frameID.String(), got["frame_id"])

	// @constraint: ?frame_id=<other> → nothing (cross-frame exclusion; pending excluded).
	status, out = h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?frame_id=%s", instID, uuid.NewString()), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ = out["messages"].([]any)
	require.Empty(t, msgs, "a frame with no delivered message returns zero")

	// @constraint: malformed frame id → 400.
	status, _ = h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?frame_id=not-a-uuid", instID), nil)
	require.Equal(t, http.StatusBadRequest, status)
}

// TestMessages_PostRejectsMissingIdempotencyKey verifies that a POST
// without the mandatory Idempotency-Key header is refused at the
// boundary so an operator sees an explicit error rather than the
// request silently being treated as un-deduped. The message-type
// registry-gate rejection (the "undeclared type" path) belongs to a
// later pass that lands the `messages:` registry; this test deliberately
// targets only the idempotency-key precondition, because without it the
// handler short-circuits before any registry lookup would run.
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

	// @constraint: no Idempotency-Key header → 400, regardless of `type`.
	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type": "some-other-kind",
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

	// @constraint: mark instance terminated directly.
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Instances().MarkTerminated(ctx, shared.UUID(instUUID), tx)
	}))

	// @constraint: the 409 fires inside the tx (instance terminated), AFTER the
	// request-level Idempotency-Key guard — so carry a key, otherwise the
	// guard would pre-empt with a 400 and mask the intended conflict.
	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type": "system/invalidate",
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusConflict, resp.status)
}

// newInstanceForMessages — creates a template + instance and returns instance id.
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
			MessageType:    "system/invalidate",
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

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subID,
		// @constraint: body sender is overridden by the publisher
		// capability trust path.
		"sender": "ignored-by-trust",
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

// TestCreateMessage_SenderKindPublisherStoppedSubscriptionForbidden —
// a publisher-side message against a stopped subscription is rejected
// with 403.
func TestCreateMessage_SenderKindPublisherStoppedSubscriptionForbidden(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "stopped")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateStopped)

	// @constraint: 403 fires inside the tx (capability check), after the request-level
	// Idempotency-Key guard — carry a key so the guard doesn't pre-empt.
	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subID,
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
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
	// @constraint: 403 fires inside the tx (capability check), after the request-level
	// Idempotency-Key guard — carry a key so the guard doesn't pre-empt.
	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type":                      "system/invalidate",
		"sender_kind":               "publisher",
		"publisher_subscription_id": uuid.NewString(),
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
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

	// @constraint: 403 fires inside the tx (capability check binds the sub to instance
	// A, not B), after the request-level Idempotency-Key guard — carry a
	// key so the guard doesn't pre-empt.
	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instB), map[string]any{
		"type":                      "system/invalidate",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subForA,
	}, map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusForbidden, resp.status)
}

// TestCreateMessage_SenderKindPublisherMissingSubscriptionIDBadRequest
// — a publisher-side message without a subscription id is rejected
// with 400 (capability check is mandatory).
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

// TestCreateMessage_SenderKindInvalidBadRequest — unknown sender_kind
// values are rejected with 400.
func TestCreateMessage_SenderKindInvalidBadRequest(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "invalid-kind")
	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type": "system/invalidate",
		// @constraint: "sensor" is legacy / unsupported sender_kind.
		"sender_kind": "sensor",
	})
	require.Equal(t, http.StatusBadRequest, status)
}

// TestCreateMessage_MissingIdempotencyKeyRejected — a keyless emit
// (no Idempotency-Key header) is rejected 400 with a header-naming
// diagnostic and persists NOTHING: no message envelope (verified via
// GET /instances/{id}/messages showing zero messages) and, by the
// same-tx gating, no idempotency dedup row. Mandatory replay-dedup
// means a missing key can never silently bypass it.
//
// RED until S-control-api-mcp-idempotency-key-required lands: the
// handler today reads the header but gates dedup behind
// `if idempotencyKey != ""` and returns 201 with no required-header
// guard, so a keyless POST succeeds and silently loses replay-dedup.
func TestCreateMessage_MissingIdempotencyKeyRejected(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "no-idem-key")

	// @constraint: httpJSON sets no Idempotency-Key header.
	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID), map[string]any{
		"type": "system/invalidate",
	})
	require.Equal(t, http.StatusBadRequest, status, out)
	errMsg, _ := out["error"].(string)
	require.Contains(t, strings.ToLower(errMsg), "idempotency-key",
		"the rejection diagnostic must name the required header")

	// @constraint: no envelope persisted by the rejected POST. The envelope
	// insert is gated in the same tx as the (would-be) idempotency-row
	// insert. Post-spec instance creation is idle (no synthetic envelope
	// at create), so the message ledger filtered by the rejected type
	// stays empty.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages?type=system/invalidate", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Empty(t, msgs, "a rejected keyless emit must persist no invalidate envelope")
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
		"type": "system/invalidate",
	}
	first := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		body, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, first.status, first.body)
	firstID, _ := first.body["message_id"].(string)
	require.NotEmpty(t, firstID)
	// @constraint: second send (replay) — must dedup.
	second := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		body, map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusOK, second.status, "replay returns 200 OK")
	secondID, _ := second.body["message_id"].(string)
	require.Equal(t, firstID, secondID, "replay returns the original message_id")
}

// TestCreateMessage_DeclaredTypeAccepted exercises the happy path of
// the Pass 5 receipt-time registry gate: a POST whose `type:` matches
// a declared entry in the instance's template's `messages:` registry is
// accepted (201) and persists an envelope. `validTemplateBody` declares
// `invalidate` in its registry, so this test confirms the gate lets the
// declared type through end-to-end.
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

// TestCreateMessage_UndeclaredTypeRefused pins the load-bearing leg of
// the Pass 5 receipt-time registry gate: a POST whose `type:` is not in
// the instance's template's `messages:` registry is rejected with HTTP
// 400, the response body names both the rejected type and the declared
// set, AND no envelope is persisted. This is the message-schema story's
// falsifier — refusing loudly at receipt is what the cheaper "accept
// and silently dead-letter" shape fails to do.
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
	// @constraint: the fixture template declares exactly `system/invalidate`
	// in its registry.
	require.ElementsMatch(t, []any{"system/invalidate"}, declared)

	// @constraint: no envelope persisted — the rejection runs in the same
	// tx as the envelope insert, so a 400 leaves zero `ping/recheck` rows.
	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/instances/%s/messages?type=ping/recheck", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Empty(t, msgs, "rejected receipt must persist no envelope")
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

	// @constraint: operator-side send.
	first := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": "system/invalidate"},
		map[string]string{"Idempotency-Key": idemKey})
	require.Equal(t, http.StatusCreated, first.status)
	firstID, _ := first.body["message_id"].(string)

	// @constraint: publisher-side send with the SAME idempotency key but a
	// different sender (the subscription's publisher_name).
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

// templateBodyWithMessageSchema builds a wrapped template body whose
// `messages:` registry declares a `ping/recheck` type with a
// strict body_schema. Used by the payload-validation tests to exercise
// the receipt-time body shape check.
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

// newInstanceWithMessageSchema seeds a template carrying the
// body_schema fixture, deploys it, and creates one instance. Returns
// instance id.
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

// TestCreateMessage_AdmitsPayloadFailingBodySchema pins the spec's
// "body bytes are read only at the sanctioned substitution leaf and the
// persistence-layer fetch" rule (`@blessed-invariant: 21` + `concept:
// message-schema`'s "the body remains inert at receipt"): a payload that
// does NOT satisfy the declared body_schema is admitted at receipt with
// HTTP 201. Receivers reading `{{messages.<type>.<field>}}` would fail
// substitution at dispatch via the existing attribute-validation gate;
// the receipt path stays inert.
func TestCreateMessage_AdmitsPayloadFailingBodySchema(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceWithMessageSchema(t, h, "schema-fail")

	// @constraint: pong_status is required by the fixture body_schema; an
	// empty payload omits it. Receipt admits the envelope; dispatch-time
	// substitution is where missing required fields surface.
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

// TestCreateMessage_AcceptsPayloadMatchingBodySchema is the happy path
// complement: a payload satisfying the body_schema is accepted and the
// envelope is persisted intact.
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

// TestCreateMessage_EmptyTypeAdmittedAsImplicitEntry pins the load-
// bearing new affordance of the empty-message-wake-trigger spec: every
// template's declared-types set carries an implicit `""` entry seeded
// at registration, so a POST with `"type":""` and an Idempotency-Key is
// admitted 201, persists one envelope (with type=""), and opens a
// frame whose triggering_message_id points at it. Pins both the admit-
// path branch (`if body.Type == "" { matched = true }`) and the
// instance-create-is-idle baseline (ledger empty before the emit).
//
//	@story: empty-message-wakes-roots
//	@decision: empty-message-as-root-trigger
func TestCreateMessage_EmptyTypeAdmittedAsImplicitEntry(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "empty-admit")

	// @constraint: post-spec instance-create is idle — no synthetic
	// envelope, no frame. The ledger is empty before the test's emit.
	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	beforeMsgs, _ := out["messages"].([]any)
	require.Empty(t, beforeMsgs, "instance creation is idle; ledger must be empty before the empty-type emit")

	// @constraint: POST `{"type": ""}` with an Idempotency-Key — admitted
	// 201 via the implicit empty-entry seeded into every template's
	// declared-types set.
	resp := h.httpJSONWithHeaders(t, "POST",
		fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": ""},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID, "201 must carry a message_id")

	// @constraint: the envelope is persisted intact (type="").
	mid, err := uuid.Parse(msgID)
	require.NoError(t, err)
	row, err := h.persist.Messages().Get(ctx, shared.UUID(mid))
	require.NoError(t, err)
	require.NotNil(t, row, "the empty-typed envelope must be persisted")
	require.Equal(t, "", row.Type, "the row's type must be exactly the empty string")

	// @constraint: GET surfaces exactly one row, with type="".
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/messages", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1, "the empty-typed emit must persist exactly one envelope")
	first := msgs[0].(map[string]any)
	require.Equal(t, "", first["type"], "the GET projection must echo type=\"\"")

	// @constraint: a frame opens with this envelope as triggering_message_id.
	// Pins the empty-message wake driving the frame engine through the
	// same path any other typed message does.
	status, framesOut := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/frames", instID), nil)
	require.Equal(t, http.StatusOK, status, framesOut)
	frames, _ := framesOut["frames"].([]any)
	require.GreaterOrEqual(t, len(frames), 1, "the empty-typed emit must open at least one frame")
	frame := frames[0].(map[string]any)
	require.Equal(t, msgID, frame["triggering_message_id"],
		"the frame's triggering_message_id must point at the empty-typed envelope")
}

// TestCreateMessage_UndeclaredTypeRefused_SurfacesImplicitTypes pins
// that the 400-body response for an undeclared type carries the
// `implicit_types: [""]` sibling field. A future change that drops
// the field would mask the empty-message wake affordance from
// operators who typo'd a message type and are inspecting the response
// for the admissible set.
//
//	@story: empty-message-wakes-roots
//	@decision: empty-message-as-root-trigger
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
