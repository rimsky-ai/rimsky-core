// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func seedOneEvent(t *testing.T, h *harness, k events.Kind) {
	t.Helper()
	require.NoError(t, h.persist.Transaction(context.Background(),
		func(ctx context.Context, tx persistence.Tx) error {
			return h.persist.Events().Append(ctx,
				persistence.EventAppendInput{Kind: k},
				tx)
		}))
}

func TestEventsRoute_NoKindReturnsUnfiltered(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedOneEvent(t, h, events.KindWorkStarted())
	status, body := h.httpJSON(t, http.MethodGet, "/v1/events", nil)
	require.Equal(t, http.StatusOK, status)
	evs, ok := body["events"].([]any)
	require.True(t, ok, "events array missing in response")
	require.GreaterOrEqual(t, len(evs), 1)
}

func TestEventsRoute_KnownOperationalKindSucceeds(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
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
	h, cleanup := newHarness(t)
	defer cleanup()
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
	h, cleanup := newHarness(t)
	defer cleanup()
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/events?kind=totally_made_up_kind", nil)
	require.Equal(t, http.StatusBadRequest, status)
}
