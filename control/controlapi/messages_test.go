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
