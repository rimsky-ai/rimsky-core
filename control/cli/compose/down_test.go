// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package compose_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/control/cli/compose"
)

func TestRunComposeDown_Terminal(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	insts := srv.State.ListInstances("", "")
	if len(insts) != 1 {
		t.Fatalf("got %+v", insts)
	}
	now := time.Now()
	srv.State.SetInstanceTerminated(insts[0].ID, &now)
	if got := compose.RunComposeDown(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Errorf("exit %d", got)
	}
	if len(srv.State.ListInstances("", "")) != 0 {
		t.Errorf("instances remain")
	}
}

func TestRunComposeDown_NonTerminalRefuses(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	_ = srv
	if got := compose.RunComposeDown(context.Background(), []string{"-f", mf, "--yes"}); got != 1 {
		t.Errorf("exit %d, want 1", got)
	}
}

func TestRunComposeDown_InfraFlag(t *testing.T) {
	srv := setupServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.yml"), []byte(planSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "probe.flag")
	body := `project: p
infra:
  down:
    command: ["sh", "-c", "touch ` + probe + `"]
templates:
  - path: spec.yml
    tag: a@1.0
`
	mf := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	_ = srv
	if got := compose.RunComposeDown(context.Background(), []string{"-f", mf, "--yes", "--infra"}); got != 0 {
		t.Errorf("exit %d", got)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Errorf("probe file not created: %v", err)
	}
}

func TestRunComposeDown_InfraFlagNotRunByDefault(t *testing.T) {
	srv := setupServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.yml"), []byte(planSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "probe.flag")
	body := `project: p
infra:
  down:
    command: ["sh", "-c", "touch ` + probe + `"]
templates:
  - path: spec.yml
    tag: a@1.0
`
	mf := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	_ = srv
	if got := compose.RunComposeDown(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Errorf("exit %d", got)
	}
	if _, err := os.Stat(probe); err == nil {
		t.Errorf("probe file should not exist")
	}
}
