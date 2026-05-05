// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package compose_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fallguy/rimsky/modeling/cli/compose"
)

func TestMaterializeRimskyConfig_Inline(t *testing.T) {
	dir := t.TempDir()
	m := &compose.Manifest{
		Project: "p",
		RimskyConfig: &compose.RimskyConfig{
			Inline: map[string]any{"stores": map[string]any{"a": 1}},
		},
	}
	path, err := compose.MaterializeRimskyConfig(m, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "rimsky.yml") {
		t.Errorf("path: %s", path)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "stores:") {
		t.Errorf("content: %s", raw)
	}
	// Re-running overwrites.
	m.RimskyConfig.Inline["stores"] = map[string]any{"b": 2}
	if _, err := compose.MaterializeRimskyConfig(m, dir); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "b:") {
		t.Errorf("did not overwrite: %s", raw)
	}
}

func TestRunInfraUp_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	infra := &compose.Infra{
		Up: &compose.InfraCommand{
			Command: []string{"true"},
			WaitFor: srv.URL,
			Timeout: "5s",
		},
	}
	if err := compose.RunInfraUp(context.Background(), infra, t.TempDir()); err != nil {
		t.Errorf("err: %v", err)
	}
}

func TestRunInfraUp_CommandFailure(t *testing.T) {
	infra := &compose.Infra{
		Up: &compose.InfraCommand{Command: []string{"false"}},
	}
	if err := compose.RunInfraUp(context.Background(), infra, t.TempDir()); err == nil {
		t.Error("want error")
	}
}

func TestRunInfraUp_WaitForTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	infra := &compose.Infra{
		Up: &compose.InfraCommand{
			Command: []string{"true"},
			WaitFor: srv.URL,
			Timeout: "100ms",
		},
	}
	if err := compose.RunInfraUp(context.Background(), infra, t.TempDir()); err == nil {
		t.Error("want timeout error")
	}
}

func TestRunDevUp_HappyPath(t *testing.T) {
	apiSrv := setupServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.yml"), []byte(planSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	healthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthSrv.Close()
	body := `project: p
infra:
  up:
    command: ["true"]
    wait_for: "` + healthSrv.URL + `"
    timeout: 2s
templates:
  - path: spec.yml
    tag: a@1.0
`
	mf := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := compose.RunDevUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Errorf("exit %d", got)
	}
	if len(apiSrv.State.ListTemplates()) == 0 {
		t.Errorf("template not registered")
	}
}

func TestRunDevUp_InfraCommandFailureBail(t *testing.T) {
	_ = setupServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.yml"), []byte(planSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `project: p
infra:
  up:
    command: ["false"]
templates:
  - path: spec.yml
    tag: a@1.0
`
	mf := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := compose.RunDevUp(context.Background(), []string{"-f", mf, "--yes"}); got != 1 {
		t.Errorf("exit %d", got)
	}
}
