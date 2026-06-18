// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
			"thing_create": {Action: "thing:create", IsWrite: true, Routes: []mcp.RegistryRoute{{Method: "POST", Path: "/things"}}},
			"thing_list":   {Action: "thing:list", IsWrite: false, Routes: []mcp.RegistryRoute{{Method: "GET", Path: "/things"}}},
		},
	}
	cat := &mcp.Catalog{Registry: reg, Router: r}

	got, mcpErr := cat.Invoke(httptest.NewRequest("POST", "/mcp", nil), "thing_create", json.RawMessage(`{}`))
	if mcpErr != nil {
		t.Fatalf("thing_create: unexpected error %+v", mcpErr)
	}
	if m, ok := got.(map[string]any); ok && m["isError"] == true {
		t.Fatalf("thing_create: got error envelope %+v", m)
	}
	if sawIdempotency == "" {
		t.Fatalf("thing_create: expected synthesized Idempotency-Key header, got none")
	}

	if _, mcpErr := cat.Invoke(httptest.NewRequest("GET", "/mcp", nil), "thing_list", nil); mcpErr != nil {
		t.Fatalf("thing_list: unexpected error %+v", mcpErr)
	}
}
