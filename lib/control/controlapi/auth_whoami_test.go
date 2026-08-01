// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func whoamiTestServer(t *testing.T, h authTestHarness) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Get("/v1/auth/whoami", handleWhoAmI())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func getWhoami(t *testing.T, url, bearer string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url+"/v1/auth/whoami", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, body
}

func TestWhoAmI_ValidKeyReportsKeyID(t *testing.T) {
	h := newAuthTestHarness(t)
	srv := whoamiTestServer(t, h)

	status, body := getWhoami(t, srv.URL, h.plaintext)
	if status != http.StatusOK {
		t.Fatalf("status: got %d want 200 (body %v)", status, body)
	}
	if body["kind"] != "api_key" {
		t.Errorf("kind: got %v want api_key", body["kind"])
	}
	if body["key_id"] != (shared.UUID{9, 9, 9}).String() {
		t.Errorf("key_id: got %v want %s", body["key_id"], shared.UUID{9, 9, 9})
	}
}

func TestWhoAmI_InvalidOrMissingKeyIsUnauthorizedInAuthenticatedMode(t *testing.T) {
	h := newAuthTestHarness(t)
	srv := whoamiTestServer(t, h)

	if status, _ := getWhoami(t, srv.URL, "rk_not-a-real-key"); status != http.StatusUnauthorized {
		t.Fatalf("invalid key status: got %d want 401", status)
	}
	if status, _ := getWhoami(t, srv.URL, (shared.UUID{9, 9, 9}).String()); status != http.StatusUnauthorized {
		t.Fatalf("key-id-as-token status: got %d want 401", status)
	}
	if status, _ := getWhoami(t, srv.URL, ""); status != http.StatusUnauthorized {
		t.Fatalf("no-token status: got %d want 401", status)
	}
}

func TestWhoAmI_BadBearerRejectedEvenInAnonymousMode(t *testing.T) {
	h := newUnseededAuthTestHarness(t)
	srv := whoamiTestServer(t, h)

	anonStatus, anonBody := getWhoami(t, srv.URL, "")
	if anonStatus != http.StatusOK || anonBody["kind"] != "anonymous" {
		t.Fatalf("harness precondition: got status=%d body=%v, want an anonymous-mode 200", anonStatus, anonBody)
	}

	if status, body := getWhoami(t, srv.URL, "rk_not-a-real-key"); status != http.StatusUnauthorized {
		t.Fatalf("bad bearer in anonymous mode: got %d (body %v) want 401; "+
			"a presented-but-invalid token must fail closed, never fall through to the anonymous admin identity", status, body)
	}
}

func TestWhoAmI_AnonymousModeReportsAnonymous(t *testing.T) {
	h := newUnseededAuthTestHarness(t)
	srv := whoamiTestServer(t, h)

	status, body := getWhoami(t, srv.URL, "")
	if status != http.StatusOK {
		t.Fatalf("status: got %d want 200 (body %v)", status, body)
	}
	if body["kind"] != "anonymous" {
		t.Errorf("kind: got %v want anonymous", body["kind"])
	}
	if _, present := body["key_id"]; present {
		t.Errorf("anonymous identity must not carry a key_id, got %v", body["key_id"])
	}
}
