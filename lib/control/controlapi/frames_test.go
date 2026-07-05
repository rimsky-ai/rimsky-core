// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func mainRunScopeIDForInstance(t *testing.T, h *harness, instanceID shared.UUID) shared.UUID {
	t.Helper()
	scopeID := shared.UUID(uuid.New())
	require.NoError(t, h.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         scopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		})
	}))
	return scopeID
}

func seedFrameForTest(
	t *testing.T, ctx context.Context, h *harness,
	instanceID shared.UUID, msgType string,
) (shared.UUID, shared.UUID) {
	t.Helper()
	msgID := shared.UUID(uuid.New())
	var frameID shared.UUID
	rootScope := mainRunScopeIDForInstance(t, h, instanceID)
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       msgType,
			Sender:     "test-frame-seed",
			SenderKind: "operator",
			ReceivedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		priorRunning, err := h.persist.Frames().GetRunningFrameID(ctx, instanceID, tx)
		if err != nil {
			return err
		}
		if priorRunning != nil {
			if _, err := h.persist.Frames().MarkRunningFrameTerminal(ctx, *priorRunning, persistence.FrameStateCompleted, tx); err != nil {
				return err
			}
		}
		fid, err := h.persist.Frames().InsertRunningFrame(ctx, instanceID, msgID, rootScope, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	return frameID, msgID
}

func TestFrames_ListReturnsAllForInstance(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "frames-list")
	instUUID := mustParseUUID(t, instID)
	want := map[string]string{}
	for i := 0; i < 3; i++ {
		fid, mid := seedFrameForTest(t, ctx, h, instUUID, fmt.Sprintf("test/seed-%d", i))
		want[fid.String()] = mid.String()
	}

	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/frames", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	frames, _ := out["frames"].([]any)
	require.GreaterOrEqual(t, len(frames), 3, "expected at least the 3 seeded frames")

	got := map[string]string{}
	for _, f := range frames {
		m, _ := f.(map[string]any)
		fid, _ := m["frame_id"].(string)
		tmid, _ := m["triggering_message_id"].(string)
		require.NotEmpty(t, fid)
		require.NotEmpty(t, tmid, "every frame line must carry triggering_message_id (frame-origin-audit)")
		got[fid] = tmid
	}
	for fid, mid := range want {
		require.Equal(t, mid, got[fid], "frame %s triggering_message_id mismatch", fid)
	}
}

func TestFrames_ListFilteredByTriggeringMessageID(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "frames-filter")
	instUUID := mustParseUUID(t, instID)
	targetFrame, targetMsg := seedFrameForTest(t, ctx, h, instUUID, "test/target")
	_, _ = seedFrameForTest(t, ctx, h, instUUID, "test/other")

	url := fmt.Sprintf("/v1/instances/%s/frames?triggering_message_id=%s", instID, targetMsg.String())
	status, out := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusOK, status, out)
	frames, _ := out["frames"].([]any)
	require.Len(t, frames, 1, "filter must narrow to the one matching frame")
	m, _ := frames[0].(map[string]any)
	require.Equal(t, targetFrame.String(), m["frame_id"])
	require.Equal(t, targetMsg.String(), m["triggering_message_id"])
}

func TestFrames_GetSingleFrameJoinsMessageEnvelope(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "frames-get")
	instUUID := mustParseUUID(t, instID)
	frameID, msgID := seedFrameForTest(t, ctx, h, instUUID, "test/get")

	url := fmt.Sprintf("/v1/instances/%s/frames/%s", instID, frameID.String())
	status, out := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, frameID.String(), out["frame_id"])
	require.Equal(t, msgID.String(), out["triggering_message_id"])
	require.Equal(t, "test/get", out["message_type"])
	require.Equal(t, "test-frame-seed", out["message_sender"])
	require.Equal(t, "operator", out["message_sender_kind"])
}

func mustParseUUID(t *testing.T, s string) shared.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	require.NoError(t, err)
	return shared.UUID(id)
}
