// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func seedAuditEvents(t *testing.T, h *harness) {
	t.Helper()
	rows := []events.Kind{
		events.KindAuthAccessAttempted(),
		events.KindAuthAccessDenied(),
		events.KindAuthKeyCreated(),
		events.KindAuthKeyRevoked(),
		events.KindAuthKeyRotated(),
	}
	require.NoError(t, h.persist.Transaction(context.Background(),
		func(ctx context.Context, tx persistence.Tx) error {
			for _, k := range rows {
				if err := h.persist.Events().Append(ctx,
					persistence.EventAppendInput{Kind: k},
					tx); err != nil {
					return err
				}
			}
			return nil
		}))
}

func TestAuditRoute_NoKindReturnsAllAuthRows(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedAuditEvents(t, h)
	status, body := h.httpJSON(t, http.MethodGet, "/v1/audit", nil)
	require.Equal(t, http.StatusOK, status)
	rows, ok := body["audit"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(rows), 5)
}

func TestAuditRoute_AuthPrefixKindNarrows(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedAuditEvents(t, h)
	status, body := h.httpJSON(t, http.MethodGet,
		"/v1/audit?kind="+auth.EventKeyRevoked, nil)
	require.Equal(t, http.StatusOK, status)
	rows, ok := body["audit"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 1)
	r := rows[0].(map[string]any)
	require.Equal(t, auth.EventKeyRevoked, r["kind"])
}

func TestAuditRoute_NoKindExcludesNonAuthOperationalEvents(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedAuditEvents(t, h)
	require.NoError(t, h.persist.Transaction(context.Background(),
		func(ctx context.Context, tx persistence.Tx) error {
			return h.persist.Events().Append(ctx,
				persistence.EventAppendInput{Kind: events.KindStateTransition()},
				tx)
		}))
	status, body := h.httpJSON(t, http.MethodGet, "/v1/audit", nil)
	require.Equal(t, http.StatusOK, status)
	rows, ok := body["audit"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 5)
	for _, row := range rows {
		r := row.(map[string]any)
		require.True(t, strings.HasPrefix(r["kind"].(string), "auth."),
			"unfiltered /v1/audit returned a non-auth kind %q", r["kind"])
	}
}

func TestAuditRoute_NonAuthOperationalKindReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedAuditEvents(t, h)
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/audit?kind=state_transition", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestAuditRoute_AuthPrefixedNonAllowlistedKindReturns400NotEmptyList(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedAuditEvents(t, h)
	status, body := h.httpJSON(t, http.MethodGet,
		"/v1/audit?kind=auth.foo/bar", nil)
	require.Equal(t, http.StatusBadRequest, status, body,
		"a kind that merely starts with \"auth.\" but is not one of the five allowlisted audit kinds "+
			"must 400, not silently pass the gate and return a zero-row page")
}

func TestAuditRoute_UnknownKindReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/audit?kind=totally_made_up_kind", nil)
	require.Equal(t, http.StatusBadRequest, status)
}
