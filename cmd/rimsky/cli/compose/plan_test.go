// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	oldHash, _ := srv.State.RegisterTemplate(map[string]any{"name": "old", "version": "1.0", "nodes": []any{}}, "p:a@1.0", "")
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
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "p:legacy@0.9", "")
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

func TestComputePlan_SharedHashSurvivesSiblingTagRemoval(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)

	dir, m := makeManifest(t, `project: p
templates:
  - path: spec.yml
    tag: a@1.0
    state: deployed
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}

	hash, spec, err := compose.ResolveTemplate(m.Templates[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	registeredHash, _ := srv.State.RegisterTemplate(specToMap(t, spec), "p:a@1.0", "")
	if registeredHash != hash {
		t.Fatalf("fixture hash mismatch: registered %q resolved %q", registeredHash, hash)
	}
	srv.State.SetTagHash("p:b@1.0", hash)
	srv.State.SetTemplateState(hash, "deployed")

	state, err := compose.QueryState(context.Background(), c, m.Project)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range plan.Steps {
		if (s.Action == compose.ActionUndeploy || s.Action == compose.ActionTemplateDelete) && s.TemplateHash == hash {
			t.Fatalf("plan tears down shared hash %s still referenced by surviving tag a@1.0: %+v", cli.TruncHash(hash), plan.Steps)
		}
	}
	sawTagDelete := false
	for _, s := range plan.Steps {
		if s.Action == compose.ActionTagDelete && s.Tag == "p:b@1.0" {
			sawTagDelete = true
		}
	}
	if !sawTagDelete {
		t.Fatalf("expected a tag-delete step for removed sibling tag b@1.0: %+v", plan.Steps)
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
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:hello"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FailedCount: 1}})
	now := time.Now()
	srv.State.SetInstanceTerminated(inst.ID, &now)

	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	actions := []compose.Action{}
	deleteIdx, createIdx := -1, -1
	var deletedDestructive bool
	for i, s := range plan.Steps {
		actions = append(actions, s.Action)
		if s.Action == compose.ActionInstanceDelete && s.InstanceKey == key {
			deleteIdx = i
			deletedDestructive = s.Destructive
		}
		if s.Action == compose.ActionInstanceCreate && s.InstanceKey == key {
			createIdx = i
		}
	}
	if !containsAction(actions, compose.ActionInstanceDelete) || !containsAction(actions, compose.ActionInstanceCreate) {
		t.Errorf("missing delete+create; got %+v", actions)
	}
	if deleteIdx < 0 || createIdx < 0 {
		t.Fatalf("could not find delete/create steps for key %s; got %+v", key, plan.Steps)
	}
	if !(deleteIdx < createIdx) {
		t.Errorf("delete step (index %d) must precede the create step (index %d) for the same key", deleteIdx, createIdx)
	}
	if !deletedDestructive {
		t.Errorf("delete step for a failed instance should be marked Destructive")
	}
}

func TestComputePlan_RestartAlways_RecreatesEvenOnSuccess(t *testing.T) {
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
    restart: always
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	hash, body, err := compose.ResolveTemplate(m.Templates[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:hello"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}})
	now := time.Now()
	srv.State.SetInstanceTerminated(inst.ID, &now)

	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}
	actions := []compose.Action{}
	var deletedDestructive bool
	for _, s := range plan.Steps {
		actions = append(actions, s.Action)
		if s.Action == compose.ActionInstanceDelete && s.InstanceKey == key {
			deletedDestructive = s.Destructive
		}
	}
	if !containsAction(actions, compose.ActionInstanceDelete) || !containsAction(actions, compose.ActionInstanceCreate) {
		t.Errorf("restart=always on a successful terminal instance should still delete+recreate; got %+v", actions)
	}
	if deletedDestructive {
		t.Errorf("delete step for a successful instance should not be marked Destructive")
	}
}

func TestComputePlan_RestartNever_DeletesWithoutRecreating(t *testing.T) {
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
    restart: never
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	hash, body, err := compose.ResolveTemplate(m.Templates[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:hello"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FailedCount: 1}})
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
		t.Errorf("restart=never on a terminal instance should still delete it; got %+v", actions)
	}
	if containsAction(actions, compose.ActionInstanceCreate) {
		t.Errorf("restart=never must not recreate; got %+v", actions)
	}
}

func TestComputePlan_InstanceDeletesAreDeterministicallySorted(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "p:a@1.0", "")
	srv.State.SetTemplateState(hash, "deployed")

	keys := []string{"p:zebra", "p:apple", "p:mango", "p:banana", "p:cherry"}
	now := time.Now()
	for _, key := range keys {
		key := key
		inst, _, err := srv.State.CreateInstance(hash, &key, nil)
		if err != nil {
			t.Fatal(err)
		}
		srv.State.SetInstanceTerminated(inst.ID, &now)
	}

	_, m := makeManifest(t, "project: p\n")
	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}

	var deleteKeys []string
	for _, s := range plan.Steps {
		if s.Action == compose.ActionInstanceDelete {
			deleteKeys = append(deleteKeys, s.InstanceKey)
		}
	}
	if len(deleteKeys) != len(keys) {
		t.Fatalf("got %d delete steps, want %d: %+v", len(deleteKeys), len(keys), deleteKeys)
	}
	want := append([]string(nil), keys...)
	sort.Strings(want)
	if !reflect.DeepEqual(deleteKeys, want) {
		t.Errorf("instance-delete steps not deterministically sorted: got %v, want %v", deleteKeys, want)
	}
}

func TestComputePlan_NonTerminalOrphan(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, "p:a@1.0", "")
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:orphan"
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
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:hello"
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
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:hello"
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

func TestComputePlan_ParamsEqualNumericNormalizationAvoidsFalseDrift(t *testing.T) {
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
      count: 7
`)
	for i := range m.Templates {
		m.Templates[i].Path = filepath.Join(dir, m.Templates[i].Path)
	}
	hash, body, err := compose.ResolveTemplate(m.Templates[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:hello"
	if _, _, err := srv.State.CreateInstance(hash, &key, map[string]any{"count": 7}); err != nil {
		t.Fatal(err)
	}

	state, _ := compose.QueryState(context.Background(), c, m.Project)
	plan, err := compose.ComputePlan(context.Background(), c, m, state)
	if err != nil {
		t.Fatal(err)
	}

	if plan.HasDriftWarnings {
		t.Errorf("HasDriftWarnings: got true; want false for materially-equal params (manifest int 7 vs stored float64 7 must not false-positive)")
	}
	for _, s := range plan.Steps {
		if s.InstanceKey == key {
			t.Errorf("unexpected step for a non-drifted instance %s: %+v", key, s)
		}
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
	gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "p:a@1.0", "")
	if gotHash != hash {
		t.Fatalf("fake hash %q != canonical %q", gotHash, hash)
	}
	srv.State.SetTemplateState(hash, "deployed")
	key := "p:hello"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}})
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
