// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// @concept: inertness
func TestGate_AuditRowStoresSubCapRequestParamsByteVerbatim(t *testing.T) {
	h := newAuthTestHarness(t)

	probe := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Post("/v1/probe", h.state.gateByAction("instance:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	const sentinel = "audit-byte-verbatim-sentinel-2b7f0e"
	body := []byte(`{"marker":"` + sentinel + `","nested":{"n":42}}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/probe", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.plaintext)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}

	payload := lastAttemptedRow(t, h.tables)
	rp, ok := payload["request_params"].(map[string]any)
	if !ok {
		t.Fatalf("request_params: got %T want map", payload["request_params"])
	}
	if marker, _ := rp["marker"].(string); marker != sentinel {
		t.Fatalf("request_params.marker: got %q want %q — sub-cap body must be stored byte-verbatim, "+
			"not hashed or normalized", marker, sentinel)
	}
	nested, ok := rp["nested"].(map[string]any)
	if !ok {
		t.Fatalf("request_params.nested: got %T want map", rp["nested"])
	}
	if n, ok := nested["n"].(float64); !ok || n != 42 {
		t.Fatalf("request_params.nested.n: got %v want 42", nested["n"])
	}
}

// @concept: inertness
func TestGate_AuditRowNeverCarriesAPIKeyPlaintext(t *testing.T) {
	h := newAuthTestHarness(t)

	probe := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Post("/v1/probe", h.state.gateByAction("instance:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/probe", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.plaintext)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}

	payload := lastAttemptedRow(t, h.tables)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(payload): %v", err)
	}
	if strings.Contains(string(raw), h.plaintext) {
		t.Fatalf("auth.access_attempted payload must never carry the API key plaintext; found it in: %s", raw)
	}
}
