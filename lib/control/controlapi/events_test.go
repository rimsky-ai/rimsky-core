// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func seedEvent(t *testing.T, h *harness, in persistence.EventAppendInput) {
	t.Helper()
	require.NoError(t, h.persist.Transaction(context.Background(),
		func(ctx context.Context, tx persistence.Tx) error {
			return h.persist.Events().Append(ctx, in, tx)
		}))
}

func seedOneEvent(t *testing.T, h *harness, k events.Kind) {
	t.Helper()
	seedEvent(t, h, persistence.EventAppendInput{Kind: k})
}

func TestEventsRoute_NoKindReturnsUnfiltered(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	seedOneEvent(t, h, events.KindWorkStarted())
	status, body := h.httpJSON(t, http.MethodGet, "/v1/events", nil)
	require.Equal(t, http.StatusOK, status)
	evs, ok := body["events"].([]any)
	require.True(t, ok, "events array missing in response")
	require.GreaterOrEqual(t, len(evs), 1)
}

func TestEventsRoute_KnownOperationalKindSucceeds(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	seedOneEvent(t, h, events.KindWorkStarted())
	status, body := h.httpJSON(t, http.MethodGet,
		"/v1/events?kind=work_started", nil)
	require.Equal(t, http.StatusOK, status)
	evs, ok := body["events"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(evs), 1)
	row := evs[0].(map[string]any)
	require.Equal(t, "work_started", row["kind"])
}

func TestEventsRoute_KnownSignalKindSucceeds(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	seedOneEvent(t, h, events.SignalKind("terminal/success"))
	status, body := h.httpJSON(t, http.MethodGet,
		"/v1/events?kind=terminal/success", nil)
	require.Equal(t, http.StatusOK, status)
	evs, ok := body["events"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(evs), 1)
	row := evs[0].(map[string]any)
	require.Equal(t, "terminal/success", row["kind"])
}

func TestEventsRoute_UnknownKindReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/events?kind=totally_made_up_kind", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestEventsRoute_InstanceIDFilterNarrows(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instA := seedInstance(t, h, "events-inst-a-"+uuid.NewString())
	instB := seedInstance(t, h, "events-inst-b-"+uuid.NewString())
	seedEvent(t, h, persistence.EventAppendInput{
		InstanceID: &instA.ID,
		Kind:       events.KindWorkStarted(),
	})
	seedEvent(t, h, persistence.EventAppendInput{
		InstanceID: &instB.ID,
		Kind:       events.KindWorkStarted(),
	})

	status, body := h.httpJSON(t, http.MethodGet,
		"/v1/events?instance_id="+instA.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, body)
	evs, ok := body["events"].([]any)
	require.True(t, ok)
	require.Len(t, evs, 1, "instance_id filter must narrow to only the matching instance's events")
	row := evs[0].(map[string]any)
	require.Equal(t, instA.ID.String(), row["instance_id"])
}

func TestEventsRoute_InstanceIDInvalidReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	status, _ := h.httpJSON(t, http.MethodGet, "/v1/events?instance_id=not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestEventsRoute_NodeIDFilterNarrows(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "events-node-"+uuid.NewString())
	var nodes []persistence.NodeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Nodes().ListByInstance(ctx, inst.ID, tx)
		nodes = r
		return err
	}))
	require.GreaterOrEqual(t, len(nodes), 2, "template must seed at least 2 nodes for this test")

	seedEvent(t, h, persistence.EventAppendInput{
		InstanceID: &inst.ID,
		NodeID:     &nodes[0].ID,
		Kind:       events.KindWorkStarted(),
	})
	seedEvent(t, h, persistence.EventAppendInput{
		InstanceID: &inst.ID,
		NodeID:     &nodes[1].ID,
		Kind:       events.KindWorkStarted(),
	})

	status, body := h.httpJSON(t, http.MethodGet,
		"/v1/events?node_id="+nodes[0].ID.String(), nil)
	require.Equal(t, http.StatusOK, status, body)
	evs, ok := body["events"].([]any)
	require.True(t, ok)
	require.Len(t, evs, 1, "node_id filter must narrow to only the matching node's events")
	row := evs[0].(map[string]any)
	require.Equal(t, nodes[0].ID.String(), row["node_id"])
}

func TestEventsRoute_NodeIDInvalidReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	status, _ := h.httpJSON(t, http.MethodGet, "/v1/events?node_id=not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestEventsRoute_SinceUntilFilterNarrows(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := base.Add(-time.Hour)
	inWindow := base
	after := base.Add(time.Hour)

	seedEvent(t, h, persistence.EventAppendInput{Kind: events.KindWorkStarted(), OccurredAt: &before})
	seedEvent(t, h, persistence.EventAppendInput{Kind: events.KindWorkStarted(), OccurredAt: &inWindow})
	seedEvent(t, h, persistence.EventAppendInput{Kind: events.KindWorkStarted(), OccurredAt: &after})

	sinceStr := base.Add(-time.Minute).Format(time.RFC3339)
	untilStr := base.Add(time.Minute).Format(time.RFC3339)
	status, body := h.httpJSON(t, http.MethodGet,
		fmt.Sprintf("/v1/events?kind=work_started&since=%s&until=%s", sinceStr, untilStr), nil)
	require.Equal(t, http.StatusOK, status, body)
	evs, ok := body["events"].([]any)
	require.True(t, ok)
	require.Len(t, evs, 1, "since/until must narrow to only the in-window event")
	row := evs[0].(map[string]any)
	occurredAt, err := time.Parse(time.RFC3339, row["occurred_at"].(string))
	require.NoError(t, err)
	require.True(t, occurredAt.Equal(inWindow), "expected %v, got %v", inWindow, occurredAt)
}

func TestEventsRoute_SinceInvalidReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	status, _ := h.httpJSON(t, http.MethodGet, "/v1/events?since=not-a-date", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestEventsRoute_UntilInvalidReturns400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	status, _ := h.httpJSON(t, http.MethodGet, "/v1/events?until=not-a-date", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestEventsRoute_CursorPaginationWalksAllPages(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	const total = 3
	for i := 0; i < total; i++ {
		occurredAt := base.Add(time.Duration(i) * time.Second)
		seedEvent(t, h, persistence.EventAppendInput{
			Kind:       events.SignalKind("terminal/success"),
			Payload:    map[string]any{"seq": i},
			OccurredAt: &occurredAt,
		})
	}

	seen := map[float64]bool{}
	cursor := ""
	for page := 0; page < total+2; page++ {
		path := "/v1/events?kind=terminal/success&limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		status, body := h.httpJSON(t, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, status, body)
		evs, ok := body["events"].([]any)
		require.True(t, ok)
		nextCursor, _ := body["next_cursor"].(string)

		if len(evs) == 0 {
			require.Empty(t, nextCursor, "an empty page must not carry a further cursor")
			require.Len(t, seen, total, "must have walked every seeded event before the trailing empty page")
			return
		}
		require.Len(t, evs, 1, "page %d must return exactly one event with limit=1", page)
		row := evs[0].(map[string]any)
		payload := row["payload"].(map[string]any)
		seq := payload["seq"].(float64)
		require.False(t, seen[seq], "seq %v returned twice across pages", seq)
		seen[seq] = true

		if nextCursor == "" {
			require.Len(t, seen, total, "must have walked every seeded event by the last page")
			return
		}
		cursor = nextCursor
	}
	t.Fatalf("cursor pagination did not terminate within %d pages", total+2)
}
