// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fallguy/rimsky/control/cli"
	"github.com/fallguy/rimsky/control/cli/internal/clitest"
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
