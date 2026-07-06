// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func writeFullManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.yml"), []byte(planSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `project: p
templates:
  - path: spec.yml
    tag: a@1.0
instances:
  - template: a@1.0
    name: hello
`
	mf := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return mf
}

func setupServer(t *testing.T) *clitest.Server {
	t.Helper()
	srv := clitest.NewServer(t)
	t.Cleanup(srv.Close)
	t.Setenv("RIMSKY_CONTROL_API", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	return srv
}

func TestRunComposeUp_FreshAdd(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Errorf("exit %d", got)
	}
	if len(srv.State.ListInstances("", "")) != 1 {
		t.Errorf("instance count: %+v", srv.State.ListInstances("", ""))
	}
}

func TestRunComposePlan_DriftExit3(t *testing.T) {
	_ = setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposePlan(context.Background(), []string{"-f", mf}); got != 3 {
		t.Errorf("exit %d", got)
	}
}

func TestRunComposePlan_NoDriftExit0(t *testing.T) {
	_ = setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	if got := compose.RunComposePlan(context.Background(), []string{"-f", mf}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunComposePlan_ParamsDriftExit3(t *testing.T) {
	_ = setupServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.yml"), []byte(`name: x
version: "1.0"
nodes:
  - type: a
    executor: http-node
`), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `project: p
templates:
  - path: spec.yml
    tag: a@1.0
    state: deployed
instances:
  - template: a@1.0
    name: hello
    params:
      count: 5
`
	mf := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	driftBody := `project: p
templates:
  - path: spec.yml
    tag: a@1.0
    state: deployed
instances:
  - template: a@1.0
    name: hello
    params:
      count: 99
`
	if err := os.WriteFile(mf, []byte(driftBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := compose.RunComposePlan(context.Background(), []string{"-f", mf}); got != 3 {
		t.Errorf("exit %d (want 3 — drift warning even with zero steps)", got)
	}
}

func TestRunComposeStatus(t *testing.T) {
	_ = setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeStatus(context.Background(), []string{"-f", mf}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunComposeUp_NonTerminalOrphanFails(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "compose:p:other@1", "")
	srv.State.SetTemplateState(hash, "deployed")
	key := "compose:p:orphan"
	if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
		t.Fatal(err)
	}
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 1 {
		t.Errorf("exit %d", got)
	}
}

func TestApplyPlan_FailureMidPlan(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	srv.SetFailure("POST", "/v1/tags", clitest.FailureSpec{Status: 500, Body: map[string]any{"error": "boom"}, Times: 5})
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 1 {
		t.Errorf("exit %d", got)
	}
}

func TestRunComposeUp_NonTTYDestructiveRequiresYes(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); got != 0 {
		t.Fatal("up failed")
	}
	insts := srv.State.ListInstances("", "")
	if len(insts) != 1 {
		t.Fatalf("got %+v", insts)
	}
	srv.State.AddNode(insts[0].ID, cli.Node{ID: "n", InstanceID: insts[0].ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FailedCount: 1}})
	now := time.Now()
	srv.State.SetInstanceTerminated(insts[0].ID, &now)
	body := `project: p
templates:
  - path: spec.yml
    tag: a@1.0
instances:
  - template: a@1.0
    name: hello
    restart: on_failure
`
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf}); got != 2 {
		t.Errorf("exit %d", got)
	}
}
