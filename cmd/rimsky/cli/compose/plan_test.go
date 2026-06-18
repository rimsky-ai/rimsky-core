// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose_test

import (
	"context"
	"encoding/json"
	"errors"
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
)

func specToMap(t *testing.T, spec node.TemplateSpec) map[string]any {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

const planSpec = `name: x
version: "1.0"
nodes:
  - type: a
    executor: http-node
`

func makeManifest(t *testing.T, body string) (string, *compose.Manifest) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.yml"), []byte(planSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestBody := body
	if manifestBody == "" {
		manifestBody = "project: p\n"
	}
	mfPath := filepath.Join(dir, "rimsky-compose.yml")
	if err := os.WriteFile(mfPath, []byte(manifestBody), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := compose.LoadManifest(mfPath)
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

func TestComputePlan_EmptyManifestEmptyState(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	state, _ := compose.QueryState(context.Background(), c, "p")
	_, m := makeManifest(t, "project: p\n")
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary.Changes != 0 {
		t.Errorf("plan: %+v", plan)
	}
}

func TestComputePlan_FreshAdd(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	dir, m := makeManifest(t, `project: p
templates:
  - path: spec.yml
    tag: a@1.0
instances:
  - template: a@1.0
    name: hello
`)
	state, _ := compose.QueryState(context.Background(), c, m.Project)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	wantActions := []compose.Action{
		compose.ActionRegister, compose.ActionTagCreate, compose.ActionDeploy, compose.ActionInstanceCreate,
	}
	if len(plan.Steps) != len(wantActions) {
		t.Fatalf("steps len: %d want %d (%+v)", len(plan.Steps), len(wantActions), plan.Steps)
	}
	for i, want := range wantActions {
		if plan.Steps[i].Action != want {
			t.Errorf("step %d: got %s want %s", i, plan.Steps[i].Action, want)
		}
	}
}

func TestComputePlan_TagMv(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	oldHash, _ := srv.State.RegisterTemplate(map[string]any{"name": "old", "version": "1.0", "nodes": []any{}}, "compose:p:a@1.0", "")
	srv.State.SetTemplateState(oldHash, "deployed")
	dir, m := makeManifest(t, `project: p
templates:
  - path: spec.yml
    tag: a@1.0
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	actions := []compose.Action{}
	for _, s := range plan.Steps {
		actions = append(actions, s.Action)
	}
	want := []compose.Action{
		compose.ActionRegister, compose.ActionTagMove, compose.ActionDeploy,
		compose.ActionUndeploy, compose.ActionTemplateDelete,
	}
	if !sameActions(actions, want) {
		t.Errorf("actions: %+v\nwant: %+v", actions, want)
	}
}

func TestComputePlan_RemoveFromManifest(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "compose:p:legacy@0.9", "")
	srv.State.SetTemplateState(hash, "deployed")
	_, m := makeManifest(t, "project: p\n")
	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	actions := []compose.Action{}
	for _, s := range plan.Steps {
		actions = append(actions, s.Action)
	}
	want := []compose.Action{compose.ActionUndeploy, compose.ActionTagDelete, compose.ActionTemplateDelete}
	if !sameActions(actions, want) {
		t.Errorf("actions: %+v\nwant: %+v", actions, want)
	}
}

func TestComputePlan_RestartOnFailure_Failed(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	dir, m := makeManifest(t, `project: p
templates:
  - path: spec.yml
    tag: a@1.0
instances:
  - template: a@1.0
    name: hello
    restart: on_failure
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	hash, body, err := compose.ResolveTemplate(m.Templates[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "compose:p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "compose:p:hello"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", State: "failed"})
	now := time.Now()
	srv.State.SetInstanceTerminated(inst.ID, &now)

	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	actions := []compose.Action{}
	for _, s := range plan.Steps {
		actions = append(actions, s.Action)
	}
	if !containsAction(actions, compose.ActionInstanceDelete) || !containsAction(actions, compose.ActionInstanceCreate) {
		t.Errorf("missing delete+create; got %+v", actions)
	}
}

func TestComputePlan_NonTerminalOrphan(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "compose:p:a@1.0", "")
	srv.State.SetTemplateState(hash, "deployed")
	key := "compose:p:orphan"
	if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
		t.Fatal(err)
	}
	_, m := makeManifest(t, "project: p\n")
	state, _ := compose.QueryState(context.Background(), c, m.Project)
	_, err := compose.ComputePlan(context.Background(), c, m, state)
	if err == nil {
		t.Fatal("want error")
	}
	var perr *compose.ErrComposePlan
	if !errors.As(err, &perr) {
		t.Errorf("want ErrComposePlan, got %T", err)
	}
}

func TestComputePlan_IdempotentReregister(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	dir, m := makeManifest(t, `project: p
templates:
  - path: spec.yml
    tag: a@1.0
    state: deployed
instances:
  - template: a@1.0
    name: hello
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	hash, body, err := compose.ResolveTemplate(m.Templates[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "compose:p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "compose:p:hello"
	if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
		t.Fatal(err)
	}

	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary.Changes != 0 {
		t.Errorf("plan should be empty; got %d steps: %+v", plan.Summary.Changes, plan.Steps)
	}
}

func TestComputePlan_ParamsDriftWarning(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	dir, m := makeManifest(t, `project: p
templates:
  - path: spec.yml
    tag: a@1.0
    state: deployed
instances:
  - template: a@1.0
    name: hello
    params:
      count: 5
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	hash, body, err := compose.ResolveTemplate(m.Templates[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "compose:p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "compose:p:hello"
	if _, _, err := srv.State.CreateInstance(hash, &key, map[string]any{"count": 7}); err != nil {
		t.Fatal(err)
	}

	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}

	_ = w.Close()
	stderrBytes, _ := io.ReadAll(r)
	os.Stderr = origStderr

	for _, s := range plan.Steps {
		if s.InstanceKey == key {
			t.Errorf("unexpected step for drifted instance %s: %+v", key, s)
		}
	}
	if !plan.HasDriftWarnings {
		t.Errorf("HasDriftWarnings: got false; want true")
	}

	stderr := string(stderrBytes)
	want := "warning: params drift on running instance " + key
	count := strings.Count(stderr, want)
	if count != 1 {
		t.Errorf("warning appearances: got %d; stderr=%q", count, stderr)
	}
}

func TestAggregateOutcome_NonFreshIsFailure(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	dir, m := makeManifest(t, `project: p
templates:
  - path: spec.yml
    tag: a@1.0
instances:
  - template: a@1.0
    name: hello
    restart: on_failure
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	hash, body, err := compose.ResolveTemplate(m.Templates[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "compose:p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "compose:p:hello"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", State: "running"})
	now := time.Now()
	srv.State.SetInstanceTerminated(inst.ID, &now)

	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	actions := []compose.Action{}
	for _, s := range plan.Steps {
		actions = append(actions, s.Action)
	}
	if !containsAction(actions, compose.ActionInstanceDelete) {
		t.Errorf("missing instance-delete; got %+v", actions)
	}
	if !containsAction(actions, compose.ActionInstanceCreate) {
		t.Errorf("missing instance-create (non-fresh treated as success?); got %+v", actions)
	}
}

func sameActions(a, b []compose.Action) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsAction(list []compose.Action, target compose.Action) bool {
	for _, a := range list {
		if a == target {
			return true
		}
	}
	return false
}
