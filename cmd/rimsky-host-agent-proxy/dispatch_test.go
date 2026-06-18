// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlAPIFetcherHitsV1InstancesRoute(t *testing.T) {
	const (
		instanceID    = "inst-abc"
		token         = "test-token"
		bindingName   = "verifier"
		ownerAPIKeyID = "key-1"
	)

	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/instances/"+instanceID {
			http.NotFound(w, r)
			return
		}
		body := instanceJSON{
			ServiceBindings: map[string]bindingSpec{
				bindingName: {Path: "/usr/local/bin/verifier"},
			},
			OwnerAPIKeyID: ownerAPIKeyID,
			Params:        json.RawMessage(`{"cwd":"/work"}`),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	fetch := newControlAPIFetcher(srv.Client(), srv.URL, token)

	entry, found, err := fetch(context.Background(), instanceID)
	if err != nil {
		t.Fatalf("fetcher returned error: %v", err)
	}
	if !found {
		t.Fatalf("fetcher reported not-found for instance %q (path hit: %q) — the cache-miss fallback would silently dead-end here", instanceID, gotPath)
	}
	if gotPath != "/v1/instances/"+instanceID {
		t.Fatalf("fetcher hit wrong path: got %q, want %q", gotPath, "/v1/instances/"+instanceID)
	}
	if gotAuth != "Bearer "+token {
		t.Fatalf("fetcher sent wrong Authorization header: got %q, want %q", gotAuth, "Bearer "+token)
	}
	if entry == nil {
		t.Fatalf("fetcher returned nil entry despite found=true")
	}
	if entry.ownerAPIKeyID != ownerAPIKeyID {
		t.Fatalf("entry.ownerAPIKeyID = %q, want %q", entry.ownerAPIKeyID, ownerAPIKeyID)
	}
	if _, ok := entry.serviceBindings[bindingName]; !ok {
		t.Fatalf("entry.serviceBindings missing %q binding; got %v", bindingName, entry.serviceBindings)
	}
	if got, _ := entry.params["cwd"].(string); got != "/work" {
		t.Fatalf("entry.params[cwd] = %q, want %q", got, "/work")
	}
}

func TestControlAPIFetcherFoldsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	fetch := newControlAPIFetcher(srv.Client(), srv.URL, "")
	entry, found, err := fetch(context.Background(), "inst-missing")
	if err != nil {
		t.Fatalf("404 must not surface as an error: %v", err)
	}
	if found || entry != nil {
		t.Fatalf("404 must yield (nil, false, nil); got entry=%v found=%v", entry, found)
	}
}

func TestControlAPIFetcherSurfacesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	fetch := newControlAPIFetcher(srv.Client(), srv.URL, "")
	_, _, err := fetch(context.Background(), "inst-x")
	if err == nil {
		t.Fatalf("500 must surface as an error, got nil")
	}
}

func TestControlAPIFetcherEmptyBaseURL(t *testing.T) {
	fetch := newControlAPIFetcher(http.DefaultClient, "", "")
	entry, found, err := fetch(context.Background(), "inst-x")
	if err != nil {
		t.Fatalf("empty baseURL must not error: %v", err)
	}
	if found || entry != nil {
		t.Fatalf("empty baseURL must yield (nil, false, nil); got entry=%v found=%v", entry, found)
	}
}
