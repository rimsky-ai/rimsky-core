// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	key := "compose:p:n"
	if _, _, err := srv.State.CreateInstance(hash, &key, nil); err != nil {
		t.Fatal(err)
	}
	if got := cli.RunInstanceList(context.Background(), []string{"--key-prefix", "compose:p:"}); got != 0 {
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
	key := "compose:p:n"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}})
	if got := cli.RunInstanceStatus(context.Background(), []string{key}); got != 0 {
		t.Errorf("exit %d, want 0", got)
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
		select {
		case exit = <-done:
		case <-time.After(5 * time.Second):
			t.Error("watch did not exit promptly on a terminal instance")
		}
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
		select {
		case exit = <-done:
		case <-time.After(5 * time.Second):
			t.Error("watch did not exit promptly on a terminal instance")
		}
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
	key := "compose:p:n"
	inst, _, _ := srv.State.CreateInstance(hash, &key, nil)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "x", Payload: map[string]any{}})
	if got := cli.RunInstanceEvents(context.Background(), []string{key}); got != 0 {
		t.Errorf("exit %d", got)
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

	time.Sleep(150 * time.Millisecond)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "k3", Payload: map[string]any{}})
	time.Sleep(150 * time.Millisecond)

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
	if cli.LooksLikeUUID("compose:p:n") {
		t.Error("non-uuid detected as uuid")
	}
}
