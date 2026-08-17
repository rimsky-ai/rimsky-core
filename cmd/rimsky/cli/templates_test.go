// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

const sampleSpec = `name: x
version: "1.0"
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
	t.Setenv("RIMSKY_CONTROL_API_URL", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	return srv
}

func TestRunTemplateRegister_OK(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)
	var got int
	out := captureStdout(t, func() {
		got = cli.RunTemplateRegister(context.Background(), []string{specPath})
	})
	if got != 0 {
		t.Errorf("exit %d", got)
	}
	if !strings.Contains(out, "template_hash:") {
		t.Errorf("template register: stdout must display the template_hash key (CLI vocab), got %q", out)
	}
	if strings.Contains(out, "template_id") {
		t.Errorf("template register: stdout must not use the retired template_id display key, got %q", out)
	}
}

func TestRunTemplateRegister_RejectComposePrefix(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)
	if got := cli.RunTemplateRegister(context.Background(), []string{"--tag", "compose:foo:bar", specPath}); got != 2 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTemplateRegister_WarningsAsErrors_QueryParam(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"template_id": "sha256-z"})
	}))
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API_URL", srv.URL)
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
	t.Setenv("RIMSKY_CONTROL_API_URL", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	specPath := writeSpec(t)
	if code := cli.RunTemplateRegister(context.Background(),
		[]string{"--warnings-as-errors", specPath}); code != 1 {
		t.Errorf("exit code: %d (want 1)", code)
	}
}

const driftSpec = `name: x
version: "1.0"
nodes:
  - type: a
    executor: drift-executor
`

func writeSpecContent(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunTemplateLint_Clean(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)
	if got := cli.RunTemplateLint(context.Background(), []string{specPath}); got != 0 {
		t.Errorf("exit %d, want 0 (clean spec)", got)
	}
}

func TestRunTemplateLint_Findings(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpecContent(t, "drift.yml", driftSpec)
	if got := cli.RunTemplateLint(context.Background(), []string{specPath}); got != 1 {
		t.Errorf("exit %d, want 1 (findings)", got)
	}
}

const warnSpec = `name: x
version: "1.0"
nodes:
  - type: a
    executor: warn-executor
`

func TestRunTemplateLint_WarningsAsErrors(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpecContent(t, "warn.yml", warnSpec)
	if got := cli.RunTemplateLint(context.Background(), []string{specPath}); got != 0 {
		t.Errorf("exit %d, want 0 (warning is non-fatal by default)", got)
	}
	if got := cli.RunTemplateLint(context.Background(),
		[]string{"--warnings-as-errors", specPath}); got != 1 {
		t.Errorf("exit %d, want 1 (--warnings-as-errors folds the warning in)", got)
	}
}

func TestRunTemplateLint_RequiresOneFile(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunTemplateLint(context.Background(), nil); got != 2 {
		t.Errorf("exit %d, want 2 (no file)", got)
	}
}

func TestRunTemplateLint_SourceFileResolution(t *testing.T) {
	_ = setupClitest(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte("hello from a referenced file"), 0o644); err != nil {
		t.Fatal(err)
	}
	specWithRef := `name: x
version: "1.0"
nodes:
  - type: a
    executor: http-node
    description:
      source_file: prompt.txt
`
	specPath := filepath.Join(dir, "spec.yml")
	if err := os.WriteFile(specPath, []byte(specWithRef), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cli.RunTemplateLint(context.Background(), []string{specPath}); got != 0 {
		t.Errorf("exit %d, want 0 (source_file resolved, clean spec)", got)
	}
}

func TestRunTemplateList(t *testing.T) {
	srv := setupClitest(t)
	srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
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
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	srv.State.SetTemplateState(hash, "deployed")
	if got := cli.RunTemplateDeploy(context.Background(), []string{"v1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunTemplateRm_OK(t *testing.T) {
	srv := setupClitest(t)
	srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
	var got int
	out := captureStdout(t, func() {
		got = cli.RunTemplateRm(context.Background(), []string{"v1"})
	})
	if got != 0 {
		t.Errorf("exit %d", got)
	}
	if !strings.Contains(out, "v1 removed") {
		t.Errorf("template rm: stdout must confirm the removed ref, got %q", out)
	}
}

func TestRunTemplateRm_Conflict(t *testing.T) {
	srv := setupClitest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "v1", "")
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
}

// @story: validation-warnings-surfaced
func TestRunTemplateRegister_PrintsAdvisoriesOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"template_id": "sha256-z",
			"validation_warnings": []map[string]string{
				{"path": "nodes[0].executor", "msg": "executor \"warn-executor\" is unreachable in the discovery cache"},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API_URL", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	specPath := writeSpec(t)

	var got int
	errOut := captureStderr(t, func() {
		captureStdout(t, func() {
			got = cli.RunTemplateRegister(context.Background(), []string{specPath})
		})
	})
	if got != 0 {
		t.Fatalf("exit %d", got)
	}
	if !strings.Contains(errOut, "warn-executor") {
		t.Errorf("a successful registration must print the validator's advisories, as the failure path already "+
			"does — the author who succeeds still needs the advice; got %q", errOut)
	}

	var jsonGot int
	jsonOut := captureStdout(t, func() {
		jsonGot = cli.RunTemplateRegister(context.Background(), []string{"--output", "json", specPath})
	})
	if jsonGot != 0 {
		t.Fatalf("json exit %d", jsonGot)
	}
	if !strings.Contains(jsonOut, "validation_warnings") {
		t.Errorf("structured output must carry validation_warnings, got %q", jsonOut)
	}
}
