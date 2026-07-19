// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
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
			if _, err := h.persist.Frames().MarkFrameEnded(ctx, *priorRunning, tx); err != nil {
				return err
			}
		}
		fid, err := h.persist.Frames().InsertRunningFrame(ctx, instanceID, msgID, rootScope, tx)
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

func endFrameForTest(t *testing.T, ctx context.Context, h *harness, frameID shared.UUID) {
	t.Helper()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		transitioned, err := h.persist.Frames().MarkFrameEnded(ctx, frameID, tx)
		require.True(t, transitioned)
		return err
	}))
}

func TestFrames_ListFilteredByStateRunning(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "frames-state")
	instUUID := mustParseUUID(t, instID)
	endedFrame, _ := seedFrameForTest(t, ctx, h, instUUID, "test/ended")
	endFrameForTest(t, ctx, h, endedFrame)
	runningFrame, _ := seedFrameForTest(t, ctx, h, instUUID, "test/running")

	url := fmt.Sprintf("/v1/instances/%s/frames?state=running", instID)
	status, out := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusOK, status, out)
	frames, _ := out["frames"].([]any)
	require.Len(t, frames, 1, "state=running must exclude the ended frame")
	m, _ := frames[0].(map[string]any)
	require.Equal(t, runningFrame.String(), m["frame_id"])
	require.Equal(t, "running", m["state"])
}

func TestFrames_ListUnfilteredIncludesEndedFrames(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "frames-state-all")
	instUUID := mustParseUUID(t, instID)
	endedFrame, _ := seedFrameForTest(t, ctx, h, instUUID, "test/ended")
	endFrameForTest(t, ctx, h, endedFrame)
	runningFrame, _ := seedFrameForTest(t, ctx, h, instUUID, "test/running")

	url := fmt.Sprintf("/v1/instances/%s/frames", instID)
	status, out := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusOK, status, out)
	frames, _ := out["frames"].([]any)
	require.Len(t, frames, 2)

	states := map[string]string{}
	for _, f := range frames {
		m, _ := f.(map[string]any)
		states[m["frame_id"].(string)] = m["state"].(string)
	}
	require.Equal(t, "completed", states[endedFrame.String()])
	require.Equal(t, "running", states[runningFrame.String()])
}

func TestFrames_List_UnknownInstanceReturns404(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	url := fmt.Sprintf("/v1/instances/%s/frames", uuid.NewString())
	status, _ := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestFrames_List_InvalidInstanceIDReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, _ := h.httpJSON(t, "GET", "/v1/instances/not-a-uuid/frames", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestFrames_List_InvalidTriggeringMessageIDReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "frames-bad-trigger")
	url := fmt.Sprintf("/v1/instances/%s/frames?triggering_message_id=not-a-uuid", instID)
	status, _ := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestFrames_Get_UnknownInstanceReturns404(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	url := fmt.Sprintf("/v1/instances/%s/frames/%s", uuid.NewString(), uuid.NewString())
	status, _ := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestFrames_Get_InvalidInstanceIDReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	url := fmt.Sprintf("/v1/instances/not-a-uuid/frames/%s", uuid.NewString())
	status, _ := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestFrames_Get_InvalidFrameIDReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "frames-bad-frame-id")
	url := fmt.Sprintf("/v1/instances/%s/frames/not-a-uuid", instID)
	status, _ := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestFrames_Get_UnknownFrameIDReturns404(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "frames-unknown-frame")
	url := fmt.Sprintf("/v1/instances/%s/frames/%s", instID, uuid.NewString())
	status, _ := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestFrames_Get_CrossInstanceFrameReturns404(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instAID := newInstanceForMessages(t, h, "frames-tenancy-a")
	instAUUID := mustParseUUID(t, instAID)
	frameA, _ := seedFrameForTest(t, ctx, h, instAUUID, "test/tenancy-a")

	instBID := newInstanceForMessages(t, h, "frames-tenancy-b")

	url := fmt.Sprintf("/v1/instances/%s/frames/%s", instBID, frameA.String())
	status, out := h.httpJSON(t, "GET", url, nil)
	require.Equal(t, http.StatusNotFound, status, out)
}

func TestFrames_List_CursorPaginationWalksAllPages(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "frames-cursor")
	instUUID := mustParseUUID(t, instID)
	const total = 3
	want := map[string]bool{}
	for i := 0; i < total; i++ {
		fid, _ := seedFrameForTest(t, ctx, h, instUUID, fmt.Sprintf("test/cursor-%d", i))
		want[fid.String()] = true
		endFrameForTest(t, ctx, h, fid)
	}

	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < total+2; page++ {
		url := fmt.Sprintf("/v1/instances/%s/frames?limit=1", instID)
		if cursor != "" {
			url += "&cursor=" + neturl.QueryEscape(cursor)
		}
		status, out := h.httpJSON(t, "GET", url, nil)
		require.Equal(t, http.StatusOK, status, out)
		frames, _ := out["frames"].([]any)
		nextCursor, _ := out["next_cursor"].(string)

		if len(frames) == 0 {
			require.Empty(t, nextCursor, "an empty page must not carry a further cursor")
			require.Equal(t, want, seen, "must have walked every seeded frame before the trailing empty page")
			return
		}
		require.Len(t, frames, 1, "page %d must return exactly one frame with limit=1", page)
		m, _ := frames[0].(map[string]any)
		fid, _ := m["frame_id"].(string)
		require.False(t, seen[fid], "frame %s returned twice across pages", fid)
		seen[fid] = true

		if nextCursor == "" {
			require.Equal(t, want, seen, "must have walked every seeded frame by the last page")
			return
		}
		cursor = nextCursor
	}
	t.Fatalf("cursor pagination did not terminate within %d pages", total+2)
}
