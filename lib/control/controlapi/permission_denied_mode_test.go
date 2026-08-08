// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func newUnpermittedAuthTestHarness(t *testing.T) authTestHarness {
	t.Helper()
	h := newUnseededAuthTestHarness(t)
	ctx := context.Background()
	plaintext, hash, err := auth.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := h.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.tables.APIKeys().Insert(ctx, persistence.APIKey{
			ID:          shared.UUID{7, 7, 7},
			Name:        "unpermitted",
			KeyHash:     hash[:],
			Permissions: []byte(`[{"action":"nothing:matches"}]`),
			CreatedAt:   h.state.Clock.Now(),
		}, tx)
	}); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	h.plaintext = plaintext
	return h
}

func lastDeniedRow(t *testing.T, tables persistence.Tables) map[string]any {
	t.Helper()
	var res persistence.EventListResult
	err := tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		res, lerr = tables.Events().List(ctx, persistence.EventListFilter{
			KindIn: []string{auth.EventAccessDenied},
		}, persistence.ListPagination{Limit: 1}, tx)
		return lerr
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(res.Events) == 0 {
		t.Fatalf("no auth.access_denied row present after request")
	}
	return res.Events[0].Payload.Map()
}

func TestGate_PermissionDeniedRowCarriesRequestedMode(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		wantMode auth.Mode
	}{
		{"enforce denial", "", auth.ModeExecute},
		{"dry-run denial", "?dry_run=true", auth.ModeDryRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newUnpermittedAuthTestHarness(t)

			r := chi.NewRouter()
			r.Use(h.state.IdentityResolver())
			r.Get("/v1/instances", h.state.gateByAction("instance:read", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/instances"+tc.query, nil)
			req.Header.Set("Authorization", "Bearer "+h.plaintext)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status: got %d want 403", resp.StatusCode)
			}

			payload := lastDeniedRow(t, h.tables)
			if payload["denial_reason"] != string(auth.DenialPermissionDenied) {
				t.Fatalf("denial_reason: got %v want %q", payload["denial_reason"], auth.DenialPermissionDenied)
			}
			if payload["mode"] != string(tc.wantMode) {
				t.Fatalf("mode: got %v want %q", payload["mode"], tc.wantMode)
			}
		})
	}
}

func TestGate_IdentityDenialRowCarriesNoMode(t *testing.T) {
	h := newAuthTestHarness(t)

	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Get("/v1/instances", h.state.gateByAction("instance:read", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp, err := http.DefaultClient.Get(srv.URL + "/v1/instances")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", resp.StatusCode)
	}

	payload := lastDeniedRow(t, h.tables)
	if payload["denial_reason"] != string(auth.DenialNoToken) {
		t.Fatalf("denial_reason: got %v want %q", payload["denial_reason"], auth.DenialNoToken)
	}
	if _, present := payload["mode"]; present && payload["mode"] != nil {
		t.Fatalf("mode: got %v want absent/nil for a non-permission_denied denial", payload["mode"])
	}
}
