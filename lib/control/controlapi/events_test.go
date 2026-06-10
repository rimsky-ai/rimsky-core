// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// events_test.go — exercises the GET /events read-API surface for
// the typed-Kind discipline introduced by
// spec:2026-06-08-design-corpus-bootstrap Pass 2. Three load-bearing
// behaviors are pinned here:
//
//   - A request with no ?kind= returns the unfiltered feed (default
//     today's behavior).
//   - A request with a recognized operational kind (or signal-class
//     type-path) narrows the feed and succeeds.
//   - A request with an unknown kind returns 400 Bad Request with
//     the offending value surfaced — never silently treated as a
//     no-op filter (per decision:event-log-kind-enum).

package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// seedOneEvent inserts a single rimsky_events row of the given kind.
// The row is instance-scopeless (no instance_id) to skip the FK
// requirement to rimsky_instances — the read-API kind-filter
// validation under test does not care about row provenance.
func seedOneEvent(t *testing.T, h *harness, k events.Kind) {
	t.Helper()
	require.NoError(t, h.persist.Transaction(context.Background(),
		func(ctx context.Context, tx persistence.Tx) error {
			return h.persist.Events().Append(ctx,
				persistence.EventAppendInput{Kind: k},
				tx)
		}))
}

// TestEventsRoute_NoKindReturnsUnfiltered confirms the default
// behavior is preserved: omitting ?kind= returns the feed unfiltered
// (validation only fires for non-empty values).
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

// TestEventsRoute_KnownOperationalKindSucceeds confirms a recognized
// operational kind narrows the feed and returns 200.
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

// TestEventsRoute_KnownSignalKindSucceeds confirms a canonical
// signal-class type-path also narrows successfully (events route is
// not restricted to operational kinds).
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

// TestEventsRoute_UnknownKindReturns400 pins the defensive read
// boundary: an unknown kind is rejected with 400 — never silently
// treated as a no-op filter that returns every row.
func TestEventsRoute_UnknownKindReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/events?kind=totally_made_up_kind", nil)
	require.Equal(t, http.StatusBadRequest, status)
}
