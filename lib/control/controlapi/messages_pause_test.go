// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: message
// @concept: instance

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestPausedInstance_MessageAccumulatesUndeliveredThenDrainsOnResume(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "pause-drain")

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/pause", instID), map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["paused"])

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{"type": "system/invalidate"},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body,
		"pause must not block message acceptance; messages accumulate while paused")
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID)

	h.tickFrameEngine(t)

	status, out = h.httpJSON(t, "GET", "/v1/messages/"+msgID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Nil(t, out["delivered_at"],
		"a message posted to a paused instance must not be delivered while paused")

	status, framesOut := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/frames", instID), nil)
	require.Equal(t, http.StatusOK, status, framesOut)
	frames, _ := framesOut["frames"].([]any)
	require.Empty(t, frames, "no frame may open for a message posted to a paused instance")

	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/resume", instID), map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["resumed"])

	h.tickFrameEngine(t)
	require.NoError(t, runtime.SweepDeliverTriggeringMessagesForRunningFrames(context.Background(), h.persist, shared.SilentLogger{}, time.Now()))

	status, out = h.httpJSON(t, "GET", "/v1/messages/"+msgID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.NotNil(t, out["delivered_at"],
		"the accumulated message must drain (be delivered) once the instance resumes")
}

func TestPausedInstance_PublisherMessageAccumulatesUndeliveredThenDrainsOnResume(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "pause-drain-publisher")
	subID := insertPublisherSubscription(t, h, instID, "sensor-http", persistence.PublisherSubscriptionStateActive)

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/pause", instID), map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["paused"])

	resp := h.httpJSONWithHeaders(t, "POST", fmt.Sprintf("/v1/instances/%s/messages", instID),
		map[string]any{
			"type":                      "system/invalidate",
			"publisher_subscription_id": subID,
		},
		map[string]string{"Idempotency-Key": "key-" + uuid.NewString()})
	require.Equal(t, http.StatusCreated, resp.status, resp.body,
		"pause must not block publisher message acceptance; messages accumulate while paused")
	msgID, _ := resp.body["message_id"].(string)
	require.NotEmpty(t, msgID)

	h.tickFrameEngine(t)

	status, out = h.httpJSON(t, "GET", "/v1/messages/"+msgID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "publisher", out["sender_kind"])
	require.Nil(t, out["delivered_at"],
		"a publisher message posted to a paused instance must not be delivered while paused")

	status, framesOut := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/frames", instID), nil)
	require.Equal(t, http.StatusOK, status, framesOut)
	frames, _ := framesOut["frames"].([]any)
	require.Empty(t, frames, "no frame may open for a publisher message posted to a paused instance")

	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/resume", instID), map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["resumed"])

	h.tickFrameEngine(t)
	require.NoError(t, runtime.SweepDeliverTriggeringMessagesForRunningFrames(context.Background(), h.persist, shared.SilentLogger{}, time.Now()))

	status, out = h.httpJSON(t, "GET", "/v1/messages/"+msgID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.NotNil(t, out["delivered_at"],
		"the accumulated publisher message must drain (be delivered) once the instance resumes")
}
