// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// audit_read_test.go — exercises the GET /audit ?kind= parameter
// introduced by spec:2026-06-08-design-corpus-bootstrap Pass 2
// (Task 9). The ?kind= filter on the /audit surface intersects
// with the auth.* allowlist: an auth-prefix kind narrows the read;
// a non-auth-prefix kind (even one valid in the OperationalKind
// proto enum) returns 400; an unknown kind returns 400; an absent
// ?kind= returns the full auth.* feed (today's behavior).

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

// seedAuditEvents inserts one row per auth.* kind so the audit
// reader has a non-trivial feed to filter against. The rows are
// instance-scopeless (auth audit rows aren't bound to an
// instance_id), which mirrors production wiring.
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

// TestAuditRoute_NoKindReturnsAllAuthRows pins today's behavior:
// omitting ?kind= returns the full auth.* feed. The Pass-2 change
// must not alter this default.
func TestAuditRoute_NoKindReturnsAllAuthRows(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedAuditEvents(t, h)
	status, body := h.httpJSON(t, http.MethodGet, "/v1/audit", nil)
	require.Equal(t, http.StatusOK, status)
	rows, ok := body["audit"].([]any)
	require.True(t, ok)
	// All five seeded auth.* rows show up.
	require.GreaterOrEqual(t, len(rows), 5)
}

// TestAuditRoute_AuthPrefixKindNarrows confirms that an auth-prefix
// kind (e.g. auth.key_revoked) narrows the audit feed to that
// single kind.
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

// TestAuditRoute_NonAuthOperationalKindReturns400 pins the
// intersection rule: a kind that is valid in the OperationalKind
// proto enum but NOT in the audit surface's allowlist
// (e.g. state_transition) returns 400 — the /audit surface exists
// to expose auth audit data, not arbitrary operational events.
func TestAuditRoute_NonAuthOperationalKindReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedAuditEvents(t, h)
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/audit?kind=state_transition", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

// TestAuditRoute_UnknownKindReturns400 pins the defensive read
// boundary: an unknown kind is rejected outright.
func TestAuditRoute_UnknownKindReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	status, _ := h.httpJSON(t, http.MethodGet,
		"/v1/audit?kind=totally_made_up_kind", nil)
	require.Equal(t, http.StatusBadRequest, status)
}
