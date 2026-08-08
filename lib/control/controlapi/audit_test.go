// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func newAuditTestState(t *testing.T) (*AuthState, *harness, func()) {
	t.Helper()
	h, cleanup := newHarness(t)
	s := &AuthState{
		Tables: h.persist,
		Clock:  shared.SystemClock{},
		Logger: h.logger,
	}
	return s, h, cleanup
}

func eventsByKind(t *testing.T, h *harness, kind string) []persistence.EventRow {
	t.Helper()
	var res persistence.EventListResult
	require.NoError(t, h.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Events().List(ctx, persistence.EventListFilter{KindIn: []string{kind}}, persistence.ListPagination{Limit: 100}, tx)
		res = r
		return err
	}))
	return res.Events
}

func TestInsertEvent_HappyPathPersistsRow(t *testing.T) {
	t.Parallel()
	s, h, cleanup := newAuditTestState(t)
	defer cleanup()

	keyID := mustParseUUID(t, "11111111-1111-1111-1111-111111111111")
	s.EmitKeyCreated(context.Background(), auth.KeyCreatedPayload{
		KeyID:   keyID,
		KeyName: "test-key",
	})

	rows := eventsByKind(t, h, auth.EventKeyCreated)
	require.Len(t, rows, 1)
	require.Equal(t, "test-key", rows[0].Payload.Map()["key_name"])
}

func TestInsertEvent_UnknownKindLogsParseErrorAndDoesNotPersist(t *testing.T) {
	t.Parallel()
	s, h, cleanup := newAuditTestState(t)
	defer cleanup()
	h.logger.Clear()

	s.insertEvent("totally_made_up_kind", auth.KeyCreatedProto(auth.KeyCreatedPayload{KeyName: "x"}))

	require.Empty(t, eventsByKind(t, h, "totally_made_up_kind"))
	requireLoggedError(t, h, "audit.kind-parse")
}

func requireLoggedError(t *testing.T, h *harness, msg string) {
	t.Helper()
	for _, rec := range h.logger.Records() {
		if rec.Level == "error" && rec.Msg == msg {
			return
		}
	}
	t.Fatalf("expected an error-level log record %q, got none in %+v", msg, h.logger.Records())
}

func TestEmitAttempted_ExecutedComputation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		isWrite      bool
		mode         auth.Mode
		status       int
		wantExecuted bool
	}{
		{"read succeeds", false, "", http.StatusOK, true},
		{"read fails", false, "", http.StatusInternalServerError, false},
		{"write executed", true, auth.ModeExecute, http.StatusOK, true},
		{"write dry-run never executed even on 200", true, auth.ModeDryRun, http.StatusOK, false},
		{"write executed but denied status", true, auth.ModeExecute, http.StatusForbidden, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, h, cleanup := newAuditTestState(t)
			defer cleanup()

			req := httptest.NewRequest(http.MethodPost, "/v1/instances", nil)
			ident := auth.Identity{KeyName: "k-" + tc.name, Kind: auth.IdentityAPIKey}
			s.emitAttempted(req, s.Clock.Now(), ident, "instance:create", "rest", nil, false, tc.status, tc.mode, tc.isWrite)

			rows := eventsByKind(t, h, auth.EventAccessAttempted)
			var found *persistence.EventRow
			for i := range rows {
				if rows[i].Payload.Map()["key_name"] == ident.KeyName {
					found = &rows[i]
				}
			}
			require.NotNil(t, found, "emitAttempted must persist an access_attempted row")
			require.Equal(t, tc.wantExecuted, found.Payload.Map()["executed"])
		})
	}
}

func TestEmitDenied_OptionalIdentityAndActionFields(t *testing.T) {
	t.Parallel()
	s, h, cleanup := newAuditTestState(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/instances", nil)
	s.emitDenied(req, s.Clock.Now(), auth.Identity{}, "", "rest", nil, false, http.StatusUnauthorized, auth.DenialNoToken, nil)

	rows := eventsByKind(t, h, auth.EventAccessDenied)
	var anonRow *persistence.EventRow
	for i := range rows {
		if rows[i].Payload.Map()["denial_reason"] == string(auth.DenialNoToken) && rows[i].Payload.Map()["key_name"] == nil {
			anonRow = &rows[i]
		}
	}
	require.NotNil(t, anonRow, "an identity-less denial must omit key_name/identity_kind/action rather than emit zero values")
	require.Nil(t, anonRow.Payload.Map()["action"])
	require.Nil(t, anonRow.Payload.Map()["identity_kind"])

	keyID := mustParseUUID(t, "22222222-2222-2222-2222-222222222222")
	ident := auth.Identity{KeyID: &keyID, KeyName: "named-key", Kind: auth.IdentityAPIKey}
	mode := auth.ModeExecute
	s.emitDenied(req, s.Clock.Now(), ident, "instance:create", "rest", nil, false, http.StatusForbidden, auth.DenialPermissionDenied, &mode)

	rows = eventsByKind(t, h, auth.EventAccessDenied)
	var namedRow *persistence.EventRow
	for i := range rows {
		if rows[i].Payload.Map()["key_name"] == "named-key" {
			namedRow = &rows[i]
		}
	}
	require.NotNil(t, namedRow, "a denial with a resolved identity must carry key_name/identity_kind/action")
	require.Equal(t, "instance:create", namedRow.Payload.Map()["action"])
	require.Equal(t, string(auth.IdentityAPIKey), namedRow.Payload.Map()["identity_kind"])
}
