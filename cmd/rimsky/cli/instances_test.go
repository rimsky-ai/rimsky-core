// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 64*1024)
		tmp := make([]byte, 4096)
		for {
			n, rerr := r.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if rerr != nil {
				break
			}
		}
		out <- string(buf)
	}()
	fn()
	os.Stdout = saved
	_ = w.Close()
	return <-out
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 64*1024)
		tmp := make([]byte, 4096)
		for {
			n, rerr := r.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if rerr != nil {
				break
			}
		}
		out <- string(buf)
	}()
	fn()
	os.Stderr = saved
	_ = w.Close()
	return <-out
}

func deployedTemplate(t *testing.T, srv *clitest.Server, tag string) string {
	t.Helper()
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "nodes": []any{}}, tag, "")
	srv.State.SetTemplateState(hash, "deployed")
	return hash
}

func TestRunInstanceCreate_OK(t *testing.T) {
	srv := setupClitest(t)
	deployedTemplate(t, srv, "v1")
	if got := cli.RunInstanceCreate(context.Background(), []string{"--key", "k1", "v1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunInstanceList(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	key := "p:n"
	if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
		t.Fatal(err)
	}
	if got := cli.RunInstanceList(context.Background(), []string{"--key-prefix", "p:"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunInstanceDelete_Conflict(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	key := "k"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	if got := cli.RunInstanceDelete(context.Background(), []string{inst.ID}); got != 1 {
		t.Errorf("exit %d, want 1", got)
	}
	now := time.Now()
	srv.State.SetInstanceTerminated(inst.ID, &now)
	if got := cli.RunInstanceDelete(context.Background(), []string{inst.ID}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunInstanceKill_RefusedWithoutForce(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	if got := cli.RunInstanceKill(context.Background(), []string{inst.ID}); got != 2 {
		t.Errorf("exit %d, want 2", got)
	}
	if srv.State.IsTerminated(inst.ID) {
		t.Error("instance terminated despite refusal")
	}
}

func TestRunInstanceKill_Force(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	if got := cli.RunInstanceKill(context.Background(), []string{"--force", "--reason", "stuck", inst.ID}); got != 0 {
		t.Errorf("exit %d, want 0", got)
	}
	if !srv.State.IsTerminated(inst.ID) {
		t.Error("instance not terminal after kill --force")
	}
	if got := cli.RunInstanceKill(context.Background(), []string{"--yes", inst.ID}); got != 0 {
		t.Errorf("exit %d (--yes), want 0", got)
	}
}

func TestRunInstanceStatus_JSONHasAllSections(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "work_started", Payload: map[string]any{}})
	srv.State.AddBreakpointHit(inst.ID, map[string]any{"checkpoint": "pre_dispatch", "mode": "stop"})

	var exit int
	out := captureStdout(t, func() {
		exit = cli.RunInstanceStatus(context.Background(), []string{"-o", "json", inst.ID})
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode status JSON: %v; output:\n%s", err, out)
	}
	for _, section := range []string{"instance", "nodes", "recent_events", "breakpoint_hits"} {
		if _, ok := got[section]; !ok {
			t.Errorf("status JSON missing section %q; output:\n%s", section, out)
		}
	}

	var nodes []cli.Node
	if err := json.Unmarshal(got["nodes"], &nodes); err != nil || len(nodes) != 1 {
		t.Errorf("nodes section: err=%v len=%d", err, len(nodes))
	}
	var events []cli.Event
	if err := json.Unmarshal(got["recent_events"], &events); err != nil || len(events) != 1 {
		t.Errorf("recent_events section: err=%v len=%d", err, len(events))
	}
	var hits []map[string]any
	if err := json.Unmarshal(got["breakpoint_hits"], &hits); err != nil || len(hits) != 1 {
		t.Errorf("breakpoint_hits section: err=%v len=%d", err, len(hits))
	}
}

func TestRunInstanceStatus_KeyResolution(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	key := "p:n"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}})
	if got := cli.RunInstanceStatus(context.Background(), []string{key}); got != 0 {
		t.Errorf("exit %d, want 0", got)
	}
}

func TestRunInstanceStatus_SingleGetInstanceRoundTrip(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	key := "p:n"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}})

	before := srv.GetInstanceHitCount()
	if got := cli.RunInstanceStatus(context.Background(), []string{key}); got != 0 {
		t.Fatalf("exit %d, want 0", got)
	}
	hits := srv.GetInstanceHitCount() - before
	if hits != 1 {
		t.Fatalf("RunInstanceStatus with a non-UUID key issued %d GET /v1/instances/{id} requests, want exactly 1", hits)
	}
}

func TestRunInstanceGet_ByKeyAndUUID(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	key := "p:n"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)

	var exit int
	out := captureStdout(t, func() {
		exit = cli.RunInstanceGet(context.Background(), []string{key})
	})
	if exit != 0 {
		t.Fatalf("RunInstanceGet by key: exit %d, want 0; output:\n%s", exit, out)
	}
	if !strings.Contains(out, inst.ID) {
		t.Fatalf("RunInstanceGet by key: output missing instance id, got:\n%s", out)
	}

	out = captureStdout(t, func() {
		exit = cli.RunInstanceGet(context.Background(), []string{inst.ID})
	})
	if exit != 0 {
		t.Fatalf("RunInstanceGet by uuid: exit %d, want 0; output:\n%s", exit, out)
	}
	if !strings.Contains(out, inst.ID) {
		t.Fatalf("RunInstanceGet by uuid: output missing instance id, got:\n%s", out)
	}

	out = captureStdout(t, func() {
		exit = cli.RunInstanceGet(context.Background(), []string{"-o", "json", inst.ID})
	})
	if exit != 0 {
		t.Fatalf("RunInstanceGet --output json: exit %d, want 0; output:\n%s", exit, out)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("RunInstanceGet --output json produced invalid JSON: %v; output:\n%s", err, out)
	}
}

func TestRunInstanceGet_NotFound(t *testing.T) {
	srv := setupClitest(t)
	_ = srv
	if got := cli.RunInstanceGet(context.Background(), []string{"ghost-id"}); got != 1 {
		t.Fatalf("RunInstanceGet(ghost-id): exit %d, want 1", got)
	}
}

func TestRunInstanceGet_WrongArgCount(t *testing.T) {
	if got := cli.RunInstanceGet(context.Background(), nil); got != 2 {
		t.Fatalf("RunInstanceGet(no args): exit %d, want 2", got)
	}
	if got := cli.RunInstanceGet(context.Background(), []string{"a", "b"}); got != 2 {
		t.Fatalf("RunInstanceGet(two args): exit %d, want 2", got)
	}
}

func TestRunWatch_ExitsOnTerminal(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "work_started", Payload: map[string]any{}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "breakpoint.hit", OccurredAt: "2026-06-07T00:00:01Z", Payload: map[string]any{"checkpoint": "pre_dispatch", "mode": "stop"}})
	now := time.Now()
	srv.State.SetInstanceTerminated(inst.ID, &now)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "10s", inst.ID})
		}()
		exit = <-done
	})
	if exit != 0 {
		t.Errorf("exit %d, want 0", exit)
	}
	if !strings.Contains(out, "terminal") {
		t.Errorf("watch output missing terminal line; output:\n%s", out)
	}
	if !strings.Contains(out, "work_started") {
		t.Errorf("watch output missing the seeded event; output:\n%s", out)
	}
	if !strings.Contains(out, "breakpoint.hit") {
		t.Errorf("watch output missing the seeded breakpoint hit; output:\n%s", out)
	}
}

func TestRunWatch_DrainsAllEventsBeforeTerminal(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "breakpoint.hit", OccurredAt: "2026-06-07T00:00:01Z", Payload: map[string]any{"checkpoint": "tail_marker", "mode": "stop"}})
	for i := 0; i < 100; i++ {
		srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "filler", Payload: map[string]any{}})
	}
	now := time.Now()
	srv.State.SetInstanceTerminated(inst.ID, &now)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "10s", inst.ID})
		}()
		exit = <-done
	})
	if exit != 0 {
		t.Errorf("exit %d, want 0", exit)
	}
	if !strings.Contains(out, "checkpoint=tail_marker") {
		t.Errorf("watch dropped the event-backlog tail (no checkpoint=tail_marker); output:\n%s", out)
	}
}

func TestRunInstanceNodes(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}})
	if got := cli.RunInstanceNodes(context.Background(), []string{inst.ID}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunInstanceEvents_NoFollow(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "x", Payload: map[string]any{}})
	if got := cli.RunInstanceEvents(context.Background(), []string{inst.ID}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunInstanceEvents_KeyResolution(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	key := "p:n"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "x", Payload: map[string]any{}})
	if got := cli.RunInstanceEvents(context.Background(), []string{key}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunInstanceEvents_SinceUntilNarrows(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "before", Payload: map[string]any{}, OccurredAt: t0.Format(time.RFC3339)})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "within", Payload: map[string]any{}, OccurredAt: t0.Add(time.Hour).Format(time.RFC3339)})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "after", Payload: map[string]any{}, OccurredAt: t0.Add(2 * time.Hour).Format(time.RFC3339)})

	since := t0.Add(30 * time.Minute).Format(time.RFC3339)
	until := t0.Add(90 * time.Minute).Format(time.RFC3339)
	out := captureStdout(t, func() {
		if got := cli.RunInstanceEvents(context.Background(), []string{"--since", since, "--until", until, inst.ID}); got != 0 {
			t.Errorf("exit %d", got)
		}
	})

	if !strings.Contains(out, "\twithin\n") {
		t.Fatalf("expected the in-window event to appear; output:\n%s", out)
	}
	if strings.Contains(out, "\tbefore\n") || strings.Contains(out, "\tafter\n") {
		t.Fatalf("--since/--until must narrow out events outside the window; output:\n%s", out)
	}
}

func TestRunInstanceEvents_Follow_NoDuplicates(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "k1", Payload: map[string]any{}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "k2", Payload: map[string]any{}})

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = wOut
	t.Cleanup(func() { os.Stdout = saved })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- cli.RunInstanceEvents(ctx, []string{"--follow", "--poll-interval", "20ms", inst.ID})
	}()

	waitForListEventsCalls(t, srv, 2)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "k3", Payload: map[string]any{}})
	afterAdd := srv.ListEventsHitCount()
	waitForListEventsCalls(t, srv, afterAdd+2)

	cancel()
	_ = wOut.Close()
	exit := <-done
	if exit != 0 {
		t.Errorf("exit %d", exit)
	}
	buf := make([]byte, 64*1024)
	n, _ := rOut.Read(buf)
	out := string(buf[:n])

	wantIDs := []string{"\t1\tk1", "\t2\tk2", "\t3\tk3"}
	for _, want := range wantIDs {
		if c := strings.Count(out, want); c != 1 {
			t.Errorf("event %q appeared %d times, want 1; output:\n%s", want, c, out)
		}
	}
}

func TestRunNodeGet_NotFound(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunNodeGet(context.Background(), []string{"missing"}); got != 1 {
		t.Errorf("exit %d", got)
	}
}

func TestRunNodeGet_Found(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}})
	if got := cli.RunNodeGet(context.Background(), []string{"n1"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestLooksLikeUUID(t *testing.T) {
	if !cli.LooksLikeUUID("0fb9b8a5-7c1c-4cb9-8c1f-cf1f8fcb1234") {
		t.Error("uuid not detected")
	}
	if cli.LooksLikeUUID("p:n") {
		t.Error("non-uuid detected as uuid")
	}
}
