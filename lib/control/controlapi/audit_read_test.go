// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
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

func TestAuditRoute_NonAuthOperationalKindReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedAuditEvents(t, h)
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/audit?kind=state_transition", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestAuditRoute_UnknownKindReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/audit?kind=totally_made_up_kind", nil)
	require.Equal(t, http.StatusBadRequest, status)
}
