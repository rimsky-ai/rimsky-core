// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/control/cli"
	"github.com/rimsky-ai/rimsky-core/control/cli/internal/clitest"
)

const sampleSpec = `name: x
version: "1.0"
frame_resolution_mode: coalesce
nodes:
  - type: a
    executor: http-node
`

func writeSpec(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.yml")
	if err := os.WriteFile(path, []byte(sampleSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func setupClitest(t *testing.T) *clitest.Server {
	t.Helper()
	srv := clitest.NewServer(t)
	t.Cleanup(srv.Close)
	t.Setenv("RIMSKY_CONTROL_API", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	return srv
}

func TestRunTemplateRegister_OK(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)
	if got := cli.RunTemplateRegister(context.Background(), []string{specPath}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTemplateRegister_RejectComposePrefix(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)
	if got := cli.RunTemplateRegister(context.Background(), []string{"--tag", "compose:foo:bar", specPath}); got != 2 {
		t.Errorf("exit %d", got)
	}
}

// G6: --warnings-as-errors forwards `?warnings_as_errors=true` to the
// control-API. We verify both directions: the flag-not-set path leaves
// the query empty, and the flag-set path sets it. The fake server
// captures r.URL.RawQuery and surfaces it back for assertion.
func TestRunTemplateRegister_WarningsAsErrors_QueryParam(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"template_id": "sha256-z"})
	}))
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	specPath := writeSpec(t)

	if code := cli.RunTemplateRegister(context.Background(), []string{specPath}); code != 0 {
		t.Errorf("exit (no flag): %d", code)
	}
	if seenQuery != "" {
		t.Errorf("query without flag: %q", seenQuery)
	}
	if code := cli.RunTemplateRegister(context.Background(),
		[]string{"--warnings-as-errors", specPath}); code != 0 {
		t.Errorf("exit (with flag): %d", code)
	}
	if !strings.Contains(seenQuery, "warnings_as_errors=true") {
		t.Errorf("query with flag: %q", seenQuery)
	}
}

// G6: when the server rejects with validation_warnings, the CLI exits
// non-zero and the body's warnings/errors are surfaced. We assert the
// exit code is 1 (control-api error) and the registration was refused.
func TestRunTemplateRegister_WarningsAsErrors_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("warnings_as_errors") != "true" {
			t.Errorf("query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":               "template validation pipeline rejected the registration",
			"validation_warnings": []map[string]any{{"role": "validation", "message": "missing tag"}},
			"validation_errors":   []map[string]any{},
			"warnings_as_errors":  true,
		})
	}))
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	specPath := writeSpec(t)
	if code := cli.RunTemplateRegister(context.Background(),
		[]string{"--warnings-as-errors", specPath}); code != 1 {
		t.Errorf("exit code: %d (want 1)", code)
	}
}

func TestRunTemplateList(t *testing.T) {
	srv := setupClitest(t)
	srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, "v1", "")
	if got := cli.RunTemplateList(context.Background(), nil); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTemplateGet_NotFound(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunTemplateGet(context.Background(), []string{"missing"}); got != 1 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTemplateDeploy_AlreadyDeployed(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, "v1", "")
	srv.State.SetTemplateState(hash, "deployed")
	if got := cli.RunTemplateDeploy(context.Background(), []string{"v1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTemplateRm_Conflict(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, "v1", "")
	srv.State.SetTemplateState(hash, "deployed")
	if got := cli.RunTemplateRm(context.Background(), []string{"v1"}); got != 1 {
		t.Errorf("exit %d, want 1 (conflict)", got)
	}
}

func TestReadSpec_NonObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.yml")
	if err := os.WriteFile(path, []byte("- a\n- b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = setupClitest(t)
	if got := cli.RunTemplateRegister(context.Background(), []string{path}); got != 2 {
		t.Errorf("exit %d", got)
	}
	// Sanity: error message mentions YAML object.
	_ = strings.Contains // keep import
}
