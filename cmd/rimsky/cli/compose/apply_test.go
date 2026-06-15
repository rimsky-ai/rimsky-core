// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
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

// TestRunComposePlan_ParamsDriftExit3 covers issue-12: params drift on a
// non-terminal compose-owned instance has zero plan steps but must still
// fail CI gating (exit 3, mirroring `terraform plan -detailed-exitcode`).
func TestRunComposePlan_ParamsDriftExit3(t *testing.T) {
	_ = setupServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.yml"), []byte(`name: x
version: "1.0"
frame_resolution_mode: coalesce
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
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, "compose:p:other@1", "")
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
	srv.State.AddNode(insts[0].ID, cli.Node{ID: "n", InstanceID: insts[0].ID, NodeType: "a", State: "failed"})
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
	// @constraint: test stdin is a pipe (not a TTY); destructive ops without --yes must exit 2.
	if got := compose.RunComposeUp(context.Background(), []string{"-f", mf}); got != 2 {
		t.Errorf("exit %d", got)
	}
}

// captureInstanceServer is a minimal httptest.Server that accepts
// POST /v1/instances and records the decoded request body for the
// TerminateAfterRun-propagation tests. The harness lets the tests
// drive ApplyPlan with a hand-crafted Plan (one ActionInstanceCreate
// step) without going through manifest parsing — the unit under test
// is the body-shape contract between ApplyPlan and the control-api,
// nothing more.
type captureInstanceServer struct {
	mu       sync.Mutex
	bodies   []map[string]any
	httptest *httptest.Server
}

func newCaptureInstanceServer(t *testing.T) *captureInstanceServer {
	t.Helper()
	cs := &captureInstanceServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cs.mu.Lock()
		cs.bodies = append(cs.bodies, body)
		cs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"instance_id":"inst-1","template_hash":"h","node_count":1}`))
	})
	cs.httptest = httptest.NewServer(mux)
	t.Cleanup(cs.httptest.Close)
	return cs
}

func (c *captureInstanceServer) URL() string { return c.httptest.URL }

func (c *captureInstanceServer) lastBody(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		t.Fatal("no instance bodies captured")
	}
	return c.bodies[len(c.bodies)-1]
}

// TestApplyPlan_TerminateAfterRunPropagates is the load-bearing test
// for @decision: instance-self-termination — the verb's terminal-
// wait loop sees terminated_at flip only when CreateInstance carries
// terminate_after_run=true, so ApplyOpts.TerminateAfterRun must
// reach the wire body. With ApplyOpts{} (default), the field stays
// false (and `omitempty` keeps it absent from the encoded JSON).
func TestApplyPlan_TerminateAfterRunPropagates(t *testing.T) {
	srv := newCaptureInstanceServer(t)
	c := cli.NewClient(srv.URL())
	c.SetComposeOrigin(true)
	step := compose.Step{
		Action:      compose.ActionInstanceCreate,
		Kind:        compose.KindInstance,
		TemplateTag: "compose:p:a@1.0",
		InstanceKey: "compose:p:hello",
		Params:      map[string]any{"k": "v"},
	}
	plan := &compose.Plan{Project: "p", Steps: []compose.Step{step}}

	t.Run("on=true sets terminate_after_run=true", func(t *testing.T) {
		var sink bytes.Buffer
		created, err := compose.ApplyPlan(context.Background(), c, plan, compose.ApplyOpts{Logger: &sink, TerminateAfterRun: true})
		if err != nil {
			t.Fatalf("ApplyPlan: %v", err)
		}
		if len(created) != 1 || created[0].ID != "inst-1" || created[0].Key != "compose:p:hello" {
			t.Errorf("created = %+v, want one entry with key=compose:p:hello id=inst-1", created)
		}
		body := srv.lastBody(t)
		got, ok := body["terminate_after_run"].(bool)
		if !ok || !got {
			t.Fatalf("terminate_after_run: want true, got %v (body=%+v)", body["terminate_after_run"], body)
		}
	})

	t.Run("default leaves field absent (omitempty)", func(t *testing.T) {
		var sink bytes.Buffer
		if _, err := compose.ApplyPlan(context.Background(), c, plan, compose.ApplyOpts{Logger: &sink}); err != nil {
			t.Fatalf("ApplyPlan: %v", err)
		}
		body := srv.lastBody(t)
		if v, present := body["terminate_after_run"]; present {
			// @constraint: json `omitempty` on a false bool keeps the field absent on the wire, preserving durable-by-default semantics of up/down/plan/status verbs.
			t.Fatalf("default ApplyOpts must omit terminate_after_run; got %v", v)
		}
	})
}
