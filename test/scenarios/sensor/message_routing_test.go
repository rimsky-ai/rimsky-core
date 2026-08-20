// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: message
// @concept: publisher-subscription
package sensor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func insertLiveSubscription(t *testing.T, h *scenario.Harness, instanceID shared.UUID, publisherName, state string) shared.UUID {
	t.Helper()
	subID := shared.UUID(uuid.New())
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.PublisherSubscriptions().Insert(ctx, persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     instanceID,
			PublisherName:  publisherName,
			Kind:           "http",
			ResolvedConfig: json.RawMessage(`{}`),
			MessageType:    "sensor/observation",
			State:          state,
			StartedAt:      time.Now().UTC(),
		}, tx)
	}))
	return subID
}

type envelopePostResult struct {
	Status    int
	MessageID string
	RawBody   string
}

func postPublisherEnvelope(t *testing.T, base string, instanceID, subscriptionID, idempotencyKey, bearer string, payload map[string]any) envelopePostResult {
	t.Helper()
	bodyMap := map[string]any{
		"type":                      "sensor/observation",
		"publisher_subscription_id": subscriptionID,
	}
	if payload != nil {
		bodyMap["payload"] = payload
	}
	body, err := json.Marshal(bodyMap)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost,
		base+"/v1/instances/"+instanceID+"/messages", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var decoded struct {
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &decoded)
	return envelopePostResult{Status: resp.StatusCode, MessageID: decoded.MessageID, RawBody: string(raw)}
}

func routingTemplate(h *scenario.Harness) string {
	return h.DeployTemplate(node.TemplateSpec{
		Name: "sensor-message-routing", Version: "1",
		Messages: []spec.MessageSchema{{Type: "sensor/observation"}},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "hub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "sensor/observation", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
}

func TestMessageRouting_PublisherEnvelopeDeliveredThroughRealPipeline(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	tid := routingTemplate(h)
	iid := h.CreateInstance(tid, "ck-routing-delivery", map[string]any{})
	subID := insertLiveSubscription(t, h, iid, "sensor-cron", persistence.PublisherSubscriptionStateActive)

	hub := h.FindNode(iid, "hub")
	require.NotNil(t, hub)

	idemKey := "scenario-subscription+2026-05-17T12:00:00Z"
	res := postPublisherEnvelope(t, h.ControlBase, iid.String(), subID.String(), idemKey,
		"", map[string]any{"observed_at": "2026-05-17T12:00:00Z"})
	require.Equal(t, http.StatusCreated, res.Status, "body: %s", res.RawBody)
	require.NotEmpty(t, res.MessageID)

	h.WaitForNodeState(hub.ID, cascade.NodeStateFresh)

	msgUUID, err := uuid.Parse(res.MessageID)
	require.NoError(t, err)
	msgID := shared.UUID(msgUUID)

	row := getRoutedMessage(t, h, msgID)
	require.NotNil(t, row)
	require.Equal(t, "publisher", row.SenderKind,
		"a publisher_subscription_id post must be attributed to sender_kind publisher")
	require.Equal(t, "sensor-cron", row.Sender,
		"the sender must be the subscription row's publisher_name, not caller-supplied")
	require.Equal(t, "sensor/observation", row.Type)

	awaited.Until(t, "the routed message to be marked delivered", func() bool {
		row = getRoutedMessage(t, h, msgID)
		return row.DeliveredAt != nil
	})
	require.NotNil(t, row.FrameID,
		"a delivered message must be stamped with the frame that consumed it — mark and deliver commit together")
	require.False(t, row.Cancelled)

	replay := postPublisherEnvelope(t, h.ControlBase, iid.String(), subID.String(), idemKey,
		"", map[string]any{"observed_at": "2026-05-17T12:00:00Z"})
	require.Equal(t, http.StatusOK, replay.Status,
		"an idempotency-key replay must be acknowledged as a replay (200), not re-created")
	require.Equal(t, res.MessageID, replay.MessageID,
		"the replay must return the original message id")

	var typedCount int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_messages
		 WHERE instance_id = $1 AND type = 'sensor/observation'`,
		[]any{iid}, &typedCount)
	require.Equal(t, 1, typedCount,
		"the replay must not enqueue a second message row")
}

func TestMessageRouting_WebhookAuthRejectsNonLiveOrForeignSubscription(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	tid := routingTemplate(h)
	iidA := h.CreateInstance(tid, "ck-routing-auth-a", map[string]any{})
	iidB := h.CreateInstance(tid, "ck-routing-auth-b", map[string]any{})

	stoppedSub := insertLiveSubscription(t, h, iidA, "sensor-cron", persistence.PublisherSubscriptionStateStopped)
	foreignSub := insertLiveSubscription(t, h, iidB, "sensor-cron", persistence.PublisherSubscriptionStateActive)

	res := postPublisherEnvelope(t, h.ControlBase, iidA.String(), uuid.NewString(), "k-unknown", "", nil)
	require.Equal(t, http.StatusForbidden, res.Status,
		"an unknown publisher_subscription_id must be rejected 403; body: %s", res.RawBody)

	res = postPublisherEnvelope(t, h.ControlBase, iidA.String(), stoppedSub.String(), "k-stopped", "", nil)
	require.Equal(t, http.StatusForbidden, res.Status,
		"a stopped subscription must not authenticate a publisher post; body: %s", res.RawBody)

	res = postPublisherEnvelope(t, h.ControlBase, iidA.String(), foreignSub.String(), "k-foreign", "", nil)
	require.Equal(t, http.StatusForbidden, res.Status,
		"a live subscription bound to another instance must not authenticate a post to this instance; body: %s", res.RawBody)

	res = postPublisherEnvelope(t, h.ControlBase, iidA.String(), foreignSub.String(), "", "", nil)
	require.Equal(t, http.StatusBadRequest, res.Status,
		"a missing Idempotency-Key must be rejected 400; body: %s", res.RawBody)

	liveSub := insertLiveSubscription(t, h, iidA, "sensor-cron", persistence.PublisherSubscriptionStateActive)
	res = postPublisherEnvelope(t, h.ControlBase, iidA.String(), liveSub.String(), "k-badtoken", "not-a-real-api-key", nil)
	require.Equal(t, http.StatusUnauthorized, res.Status,
		"an invalid bearer token must 401 even when anonymous mode would otherwise allow the call; body: %s", res.RawBody)

	var accepted int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_messages
		 WHERE instance_id = $1 AND type = 'sensor/observation'`,
		[]any{iidA}, &accepted)
	require.Equal(t, 0, accepted, "no rejected post may leave a message row behind")
}

func TestMessageRouting_MarkDeliveredImpliesReceiverRunInSameFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "sensor-message-mark-deliver", Version: "1",
		Messages: []spec.MessageSchema{{Type: "sensor/observation"}},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "done")
	iid := h.CreateInstance(tid, "ck-routing-mark-deliver", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	msgID := h.PostInstanceMessage(iid, "sensor/observation",
		[]byte(`{"observed_at":"2026-05-17T12:00:00Z"}`), "k-mark-deliver")

	var row *persistence.MessageRow
	awaited.Until(t, "the routed message to be marked delivered", func() bool {
		r := getRoutedMessage(t, h, msgID)
		if r == nil || r.DeliveredAt == nil {
			return false
		}
		row = r
		return true
	})
	require.NotNil(t, row.FrameID,
		"a delivered message must be stamped with the frame that consumed it")

	var receiverRuns int
	h.QueryRowSQL(`
		SELECT COUNT(*) FROM rimsky_node_runs r
		  JOIN rimsky_nodes n ON n.id = r.node_id
		 WHERE n.instance_id = $1
		   AND n.node_type = 'sensor/observation'
		   AND r.creation_reason = 'message_delivery'
		   AND r.frame_id = $2`,
		[]any{iid, *row.FrameID}, &receiverRuns)
	require.Equal(t, 1, receiverRuns,
		"delivered_at set must imply exactly one receiver run committed in the SAME frame — "+
			"mark and deliver are one transaction, so a marked-but-undelivered window cannot exist")

	var pending []persistence.MessageRow
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := h.Persist.Messages().ListPendingForInstance(ctx, iid, tx)
		pending = p
		return err
	}))
	require.Empty(t, pending,
		"the consumed message must not linger as pending")
}

func getRoutedMessage(t *testing.T, h *scenario.Harness, id shared.UUID) *persistence.MessageRow {
	t.Helper()
	var row *persistence.MessageRow
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.Messages().Get(ctx, id, tx)
		row = r
		return err
	}))
	return row
}
