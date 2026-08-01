// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func templateServer(t *testing.T, resp map[string]any, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestTemplateHasStructuralRoot_EmptyHash(t *testing.T) {
	got, err := cli.TemplateHasStructuralRoot(context.Background(), cli.NewClient("http://unused.invalid"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatal("empty hash should short-circuit to hasRoot=true")
	}
}

func TestTemplateHasStructuralRoot_GetTemplateErrorPropagates(t *testing.T) {
	srv := templateServer(t, map[string]any{"error": "not found"}, http.StatusNotFound)
	defer srv.Close()
	_, err := cli.TemplateHasStructuralRoot(context.Background(), cli.NewClient(srv.URL), "sha256-x")
	if err == nil {
		t.Fatal("GetTemplate error should propagate, not be swallowed")
	}
}

func TestTemplateHasStructuralRoot_EmptySpecIsRoot(t *testing.T) {
	srv := templateServer(t, map[string]any{"template_id": "sha256-x", "spec": map[string]any{}}, http.StatusOK)
	defer srv.Close()
	got, err := cli.TemplateHasStructuralRoot(context.Background(), cli.NewClient(srv.URL), "sha256-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatal("a template with no spec should be treated as rooted")
	}
}

func TestTemplateHasStructuralRoot_RootedTemplate(t *testing.T) {
	spec := map[string]any{
		"name":    "x",
		"version": "1.0",
		"nodes": []map[string]any{
			{"type": "a", "executor": "http-node"},
		},
	}
	srv := templateServer(t, map[string]any{"template_id": "sha256-x", "spec": spec}, http.StatusOK)
	defer srv.Close()
	got, err := cli.TemplateHasStructuralRoot(context.Background(), cli.NewClient(srv.URL), "sha256-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatal("a node with no subscribes is a structural root; want hasRoot=true")
	}
}

func TestTemplateHasStructuralRoot_NonRootedTemplate(t *testing.T) {
	spec := map[string]any{
		"name":    "x",
		"version": "1.0",
		"nodes": []map[string]any{
			{
				"type":     "b",
				"executor": "http-node",
				"subscribes": []map[string]any{
					{"type": "external/trigger", "force_upstream_refresh": false},
				},
			},
		},
	}
	srv := templateServer(t, map[string]any{"template_id": "sha256-x", "spec": spec}, http.StatusOK)
	defer srv.Close()
	got, err := cli.TemplateHasStructuralRoot(context.Background(), cli.NewClient(srv.URL), "sha256-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("a template whose only node subscribes to an external trigger has no structural root; want hasRoot=false")
	}
}

func TestTemplateHasStructuralRoot_MalformedSpecPropagatesError(t *testing.T) {
	spec := map[string]any{
		"name":    "x",
		"version": 12345,
		"nodes":   []map[string]any{{"type": "a"}},
	}
	srv := templateServer(t, map[string]any{"template_id": "sha256-x", "spec": spec}, http.StatusOK)
	defer srv.Close()
	got, err := cli.TemplateHasStructuralRoot(context.Background(), cli.NewClient(srv.URL), "sha256-x")
	if err == nil {
		t.Fatalf("a spec with a type-mismatched field should fail to unmarshal and propagate an error, got hasRoot=%v with no error", got)
	}
}
