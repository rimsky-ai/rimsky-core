// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi/mcp"
)

func TestCatalogInvoke_BodylessRequestDoesNotPanic(t *testing.T) {
	r := chi.NewRouter()
	var sawIdempotency string
	r.Post("/things", func(w http.ResponseWriter, req *http.Request) {
		defer func() { _ = req.Body.Close() }()
		body, _ := io.ReadAll(req.Body)
		sawIdempotency = req.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"len":` + strconv.Itoa(len(body)) + `}`))
	})
	r.Get("/things", func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.ReadAll(req.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	reg := &fakeRegistry{
		tools: []string{"thing_create", "thing_list"},
		entries: map[string]mcp.RegistryEntry{
			"thing_create": {Action: "thing:create", Routes: []mcp.RegistryRoute{{Method: "POST", Path: "/things"}}},
			"thing_list":   {Action: "thing:list", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	got, gotIsError, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_create", json.RawMessage(`{}`))
	if mcpErr != nil {
		t.Fatalf("thing_create: unexpected error %+v", mcpErr)
	}
	if gotIsError {
		t.Fatalf("thing_create: expected isError=false for a successful response")
	}
	if m, ok := got.(map[string]any); ok && m["isError"] == true {
		t.Fatalf("thing_create: got error envelope %+v", m)
	}
	if sawIdempotency != "" {
		t.Fatalf("thing_create: expected no Idempotency-Key header for a caller that supplied none, got %q", sawIdempotency)
	}

	if _, _, mcpErr := cat.Invoke(httptest.NewRequest("GET", "/mcp", nil), "thing_list", nil); mcpErr != nil {
		t.Fatalf("thing_list: unexpected error %+v", mcpErr)
	}
}

// @decision: idempotency-key-header-universal
func TestCatalogInvoke_OmittedIdempotencyKeyMintsNoKeyForTheCaller(t *testing.T) {
	r := chi.NewRouter()
	seen := []string{}
	r.Post("/things", func(w http.ResponseWriter, req *http.Request) {
		defer func() { _ = req.Body.Close() }()
		_, _ = io.ReadAll(req.Body)
		seen = append(seen, req.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_create"},
		entries: map[string]mcp.RegistryEntry{
			"thing_create": {Action: "thing:create", Routes: []mcp.RegistryRoute{{Method: "POST", Path: "/things"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	for range 2 {
		if _, _, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_create", json.RawMessage(`{"name":"widget"}`)); mcpErr != nil {
			t.Fatalf("unexpected error %+v", mcpErr)
		}
	}
	for i, key := range seen {
		if key != "" {
			t.Fatalf("call %d: expected no Idempotency-Key header, got %q", i, key)
		}
	}
}

func TestCatalogInvoke_CallerSuppliedIdempotencyKeyThreadsToHeaderAndDropsFromBody(t *testing.T) {
	r := chi.NewRouter()
	var sawIdempotency string
	var sawBody map[string]any
	r.Post("/things", func(w http.ResponseWriter, req *http.Request) {
		defer func() { _ = req.Body.Close() }()
		sawIdempotency = req.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(req.Body).Decode(&sawBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_create"},
		entries: map[string]mcp.RegistryEntry{
			"thing_create": {Action: "thing:create", Routes: []mcp.RegistryRoute{{Method: "POST", Path: "/things"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	args := json.RawMessage(`{"name":"widget","idempotency_key":"caller-supplied-key-1"}`)
	if _, _, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_create", args); mcpErr != nil {
		t.Fatalf("unexpected error %+v", mcpErr)
	}
	if sawIdempotency != "caller-supplied-key-1" {
		t.Fatalf("expected the caller-supplied idempotency_key to reach the Idempotency-Key header verbatim; got %q", sawIdempotency)
	}
	if _, ok := sawBody["idempotency_key"]; ok {
		t.Fatalf("idempotency_key must be consumed into the header, not leaked into the request body: %+v", sawBody)
	}
	if sawBody["name"] != "widget" {
		t.Fatalf("other body fields must still reach the request body: %+v", sawBody)
	}
}

func TestCatalogInvoke_ErrorEnvelopeFor4xxResponses(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/things/{id}", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_get"},
		entries: map[string]mcp.RegistryEntry{
			"thing_get": {Action: "thing:read", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things/{id}"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	got, gotIsError, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_get", json.RawMessage(`{"id":"x"}`))
	if mcpErr != nil {
		t.Fatalf("unexpected protocol-level error %+v", mcpErr)
	}
	if !gotIsError {
		t.Fatalf("expected isError=true for a >=400 response")
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map result for a >=400 response, got %T: %v", got, got)
	}
	if m["isError"] != true {
		t.Fatalf("expected isError:true for a 404 response, got %+v", m)
	}
	if status, _ := m["status"].(int); status != http.StatusNotFound {
		t.Fatalf("expected status %d in envelope, got %+v", http.StatusNotFound, m["status"])
	}
	body, ok := m["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected decoded body map, got %T: %v", m["body"], m["body"])
	}
	if body["error"] != "not found" {
		t.Fatalf("expected inner error body preserved, got %+v", body)
	}
}

func TestCatalogInvoke_ForwardsAuthorizationHeaderToInnerRequest(t *testing.T) {
	r := chi.NewRouter()
	var sawAuth string
	r.Get("/things/{id}", func(w http.ResponseWriter, req *http.Request) {
		sawAuth = req.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_get"},
		entries: map[string]mcp.RegistryEntry{
			"thing_get": {Action: "thing:read", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things/{id}"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	outer := httptest.NewRequest("POST", "/mcp", nil)
	outer.Header.Set("Authorization", "Bearer outer-caller-token")
	if _, _, mcpErr := cat.Invoke(outer, "thing_get", json.RawMessage(`{"id":"x"}`)); mcpErr != nil {
		t.Fatalf("unexpected error %+v", mcpErr)
	}
	if sawAuth != "Bearer outer-caller-token" {
		t.Fatalf("inner request must re-authenticate as the outer caller; got Authorization=%q", sawAuth)
	}
}

type skinCtxKey struct{}

func TestCatalogInvoke_AppliesProtocolSkin(t *testing.T) {
	r := chi.NewRouter()
	var sawSkin string
	r.Get("/things/{id}", func(w http.ResponseWriter, req *http.Request) {
		sawSkin, _ = req.Context().Value(skinCtxKey{}).(string)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_get"},
		entries: map[string]mcp.RegistryEntry{
			"thing_get": {Action: "thing:read", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things/{id}"}}},
		},
	}
	cat := &mcp.Catalog{
		Registry: reg,
		Router:   r,
		WithProtocolSkin: func(ctx context.Context, skin string) context.Context {
			return context.WithValue(ctx, skinCtxKey{}, skin)
		},
	}

	if _, _, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_get", json.RawMessage(`{"id":"x"}`)); mcpErr != nil {
		t.Fatalf("unexpected error %+v", mcpErr)
	}
	if sawSkin != "mcp" {
		t.Fatalf("inner request must carry the mcp protocol skin so the audit row records it; got %q", sawSkin)
	}
}

func TestCatalogInvoke_GETMapsScalarArgsToQuery(t *testing.T) {
	r := chi.NewRouter()
	var sawQuery url.Values
	r.Get("/things", func(w http.ResponseWriter, req *http.Request) {
		sawQuery = req.URL.Query()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_list"},
		entries: map[string]mcp.RegistryEntry{
			"thing_list": {Action: "thing:list", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	args := json.RawMessage(`{"type":"widget"}`)
	if _, _, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_list", args); mcpErr != nil {
		t.Fatalf("unexpected error %+v", mcpErr)
	}
	if got := sawQuery.Get("type"); got != "widget" {
		t.Fatalf("scalar arg must map to a query param; got type=%q", got)
	}
}

func TestCatalogInvoke_GETRejectsObjectAndArrayArgsInsteadOfSilentlyDropping(t *testing.T) {
	r := chi.NewRouter()
	called := false
	r.Get("/things", func(w http.ResponseWriter, req *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_list"},
		entries: map[string]mcp.RegistryEntry{
			"thing_list": {Action: "thing:list", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	args := json.RawMessage(`{"type":"widget","obj":{"a":1},"arr":[1,2]}`)
	_, _, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_list", args)
	if mcpErr == nil {
		t.Fatalf("expected an error naming the unencodable object/array arguments, got none")
	}
	if mcpErr.Code != mcp.CodeInvalidParams {
		t.Fatalf("expected CodeInvalidParams, got %+v", mcpErr)
	}
	if !strings.Contains(mcpErr.Message, "arr") || !strings.Contains(mcpErr.Message, "obj") {
		t.Fatalf("expected the error to name the unencodable keys, got %q", mcpErr.Message)
	}
	if called {
		t.Fatalf("router must not be reached when a query arg cannot be encoded")
	}
}

func TestCatalogInvoke_DryRunArgThreadsToQueryParamForWriteMethods(t *testing.T) {
	r := chi.NewRouter()
	var sawQuery url.Values
	var sawBody map[string]any
	r.Post("/things", func(w http.ResponseWriter, req *http.Request) {
		defer func() { _ = req.Body.Close() }()
		sawQuery = req.URL.Query()
		_ = json.NewDecoder(req.Body).Decode(&sawBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_create"},
		entries: map[string]mcp.RegistryEntry{
			"thing_create": {Action: "thing:create", Routes: []mcp.RegistryRoute{{Method: "POST", Path: "/things"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	args := json.RawMessage(`{"name":"widget","dry_run":true}`)
	if _, _, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_create", args); mcpErr != nil {
		t.Fatalf("unexpected error %+v", mcpErr)
	}
	if got := sawQuery.Get("dry_run"); got != "true" {
		t.Fatalf("dry_run tool arg must thread to the ?dry_run query param the auth middleware reads; got %q (query=%v)", got, sawQuery)
	}
	if _, ok := sawBody["dry_run"]; ok {
		t.Fatalf("dry_run must be consumed into the query string, not leaked into the request body: %+v", sawBody)
	}
	if sawBody["name"] != "widget" {
		t.Fatalf("other body fields must still reach the request body: %+v", sawBody)
	}
}

func TestCatalogInvoke_PathEscapesSubstitutedParams(t *testing.T) {
	r := chi.NewRouter()
	var sawID string
	r.Get("/things/{id}", func(w http.ResponseWriter, req *http.Request) {
		sawID = chi.URLParam(req, "id")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_get"},
		entries: map[string]mcp.RegistryEntry{
			"thing_get": {Action: "thing:read", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things/{id}"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	raw := "a/b c"
	argsBytes, err := json.Marshal(map[string]string{"id": raw})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if _, _, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_get", argsBytes); mcpErr != nil {
		t.Fatalf("unexpected error %+v", mcpErr)
	}
	if sawID == "" {
		t.Fatalf("router did not match /things/{id}; an unescaped '/' in the id would split the route into extra segments")
	}
	unescaped, err := url.PathUnescape(sawID)
	if err != nil {
		t.Fatalf("captured id segment %q is not validly percent-escaped: %v", sawID, err)
	}
	if unescaped != raw {
		t.Fatalf("the escaped id must round-trip to the original value; got %q, want %q", unescaped, raw)
	}
}

func TestCatalogInvoke_MissingPathParamReturnsInvalidParams(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/things/{id}", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	reg := &fakeRegistry{
		tools: []string{"thing_get"},
		entries: map[string]mcp.RegistryEntry{
			"thing_get": {Action: "thing:read", Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things/{id}"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	_, _, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_get", json.RawMessage(`{}`))
	if mcpErr == nil {
		t.Fatalf("expected an error for a missing path param")
	}
	if mcpErr.Code != mcp.CodeInvalidParams {
		t.Fatalf("expected CodeInvalidParams, got %+v", mcpErr)
	}
}
