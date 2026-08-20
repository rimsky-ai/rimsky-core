// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
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
	t.Setenv("RIMSKY_CONTROL_API_URL", srv.URL)
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
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "p:other@1", "")
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:orphan"
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

func TestApplyPlan_AppliedCountExcludesSkippedSteps(t *testing.T) {
	srv := setupServer(t)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "p:existing@1", "")

	c := cli.NewClient(srv.URL)
	plan := &compose.Plan{
		Project: "p",
		Steps: []compose.Step{
			{Action: compose.ActionTagCreate, Kind: compose.KindTag, Tag: "p:existing@1", TemplateHash: hash},
			{Action: compose.ActionTagCreate, Kind: compose.KindTag, Tag: "p:fresh@1", TemplateHash: hash},
		},
	}

	_, applied, err := compose.ApplyPlan(context.Background(), c, plan, compose.ApplyOpts{})
	if err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1 (one step should have been skipped as already-exists, not counted as applied)", applied)
	}
}

func TestRunComposeUp_RejectsStrayPositionalArgs(t *testing.T) {
	_ = setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes", "bogus"}); got != 2 {
		t.Fatalf("exit %d, want 2 for a stray positional argument", got)
	}
}

func TestRunComposePlan_RejectsStrayPositionalArgs(t *testing.T) {
	_ = setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposePlan(context.Background(), []string{"-f", mf, "bogus"}); got != 2 {
		t.Fatalf("exit %d, want 2 for a stray positional argument", got)
	}
}

func TestRunComposeStatus_RejectsStrayPositionalArgs(t *testing.T) {
	_ = setupServer(t)
	mf := writeFullManifest(t)
	if got := compose.RunComposeStatus(context.Background(), []string{"-f", mf, "bogus"}); got != 2 {
		t.Fatalf("exit %d, want 2 for a stray positional argument", got)
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

func TestApplyPlan_RefusesWhenTheDeploymentHashesTheSpecDifferently(t *testing.T) {
	srv := setupServer(t)
	c := cli.NewClient(srv.URL)
	spec := node.TemplateSpec{Name: "sugar", Version: "1.0"}
	deploymentHash, err := canonical.CanonicalSpecHash(spec)
	if err != nil {
		t.Fatalf("canonical hash of the spec the deployment will store: %v", err)
	}
	const plannedHash = "sha256-000000000000000000000000000000000000000000000000000000000000dead"
	if deploymentHash == plannedHash {
		t.Fatalf("the planned hash and the deployment's hash are the same value, so this test cannot tell "+
			"a refusal from an acceptance: %s", deploymentHash)
	}
	plan := &compose.Plan{
		Project: "p",
		Steps: []compose.Step{{
			Action:       compose.ActionRegister,
			Kind:         compose.KindTemplate,
			TemplateHash: plannedHash,
			FromPath:     "spec.yml",
			SpecBody:     &spec,
		}},
	}
	_, _, err = compose.ApplyPlan(context.Background(), c, plan, compose.ApplyOpts{})
	if err == nil {
		t.Fatalf("ApplyPlan applied a register whose deployment-side hash differs from the planned one. " +
			"Every later tag, deploy and instance step then names a template the deployment does not hold")
	}
	for _, want := range []string{plannedHash, deploymentHash, "spec.yml"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q, so it cannot tell the author which hash the deployment "+
				"assigned and which the manifest planned: %v", want, err)
		}
	}
}

func TestComposeUpUnderJSONWritesExactlyOneRecordToStdout(t *testing.T) {
	_ = setupServer(t)
	mf := writeFullManifest(t)

	out, code := captureComposeStdout(t, func() int {
		return compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes", "-o", "json"})
	})
	if code != 0 {
		t.Fatalf("compose up -o json exited %d:\n%s", code, out)
	}
	rec := soleJSONDocument(t, "compose up", out)
	if rec["project"] != "p" {
		t.Errorf("compose up -o json reported project %v, want p", rec["project"])
	}
	applied, ok := rec["applied"].(float64)
	if !ok || applied == 0 {
		t.Fatalf("compose up -o json reported applied=%v; a first apply changes the world its manifest "+
			"describes", rec["applied"])
	}
	instances, ok := rec["instances"].([]any)
	if !ok || len(instances) == 0 {
		t.Fatalf("compose up -o json reported instances=%v; the manifest creates one", rec["instances"])
	}
	created, ok := instances[0].(map[string]any)
	if !ok || created["instance_key"] == "" || created["instance_id"] == "" {
		t.Errorf("the created-instance record is %v; an operator reads the key and the id back from it",
			instances[0])
	}
}

func TestComposeDownUnderJSONWritesExactlyOneRecordWithAnEmptyInstanceArray(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	seeded, code := captureComposeStdout(t, func() int {
		return compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes", "-o", "json"})
	})
	if code != 0 {
		t.Fatalf("compose up (seeding the project) exited %d:\n%s", code, seeded)
	}
	terminated := time.Now()
	for _, inst := range soleJSONDocument(t, "compose up", seeded)["instances"].([]any) {
		srv.State.SetInstanceTerminated(inst.(map[string]any)["instance_id"].(string), &terminated)
	}

	out, code := captureComposeStdout(t, func() int {
		return compose.RunComposeDown(context.Background(), []string{"-f", mf, "--yes", "-o", "json"})
	})
	if code != 0 {
		t.Fatalf("compose down -o json exited %d:\n%s", code, out)
	}
	rec := soleJSONDocument(t, "compose down", out)
	if rec["project"] != "p" {
		t.Errorf("compose down -o json reported project %v, want p", rec["project"])
	}
	if _, ok := rec["applied"].(float64); !ok {
		t.Errorf("compose down -o json reported applied=%v, want a number", rec["applied"])
	}
	if instances, ok := rec["instances"].([]any); !ok || len(instances) != 0 {
		t.Errorf("compose down -o json reported instances=%v. A teardown creates none, and the field is an "+
			"empty array rather than null, so a consumer can range over it", rec["instances"])
	}
}

func soleJSONDocument(t *testing.T, verb, out string) map[string]any {
	t.Helper()
	reader := strings.NewReader(out)
	dec := json.NewDecoder(reader)
	var rec map[string]any
	if err := dec.Decode(&rec); err != nil {
		t.Fatalf("%s -o json did not write a JSON document to stdout: %v\nstdout:\n%s", verb, err, out)
	}
	rest, err := io.ReadAll(io.MultiReader(dec.Buffered(), reader))
	if err != nil {
		t.Fatalf("read what %s -o json wrote after its record: %v", verb, err)
	}
	if strings.TrimSpace(string(rest)) != "" {
		t.Fatalf("%s -o json wrote %q after its record. Stdout carries the record and nothing else, and the "+
			"per-step narration belongs on stderr\nstdout:\n%s", verb, string(rest), out)
	}
	return rec
}
