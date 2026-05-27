// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/control/cli"
	"github.com/rimsky-ai/rimsky-core/control/cli/internal/clitest"
)

func deployedTemplate(t *testing.T, srv *clitest.Server, tag string) string {
	t.Helper()
	hash, _ := srv.State.RegisterTemplate(map[string]any{"name": "x", "version": "1.0", "frame_resolution_mode": "coalesce", "nodes": []any{}}, tag, "")
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

func TestRunInstanceNodes(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", State: "fresh"})
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

// TestRunInstanceEvents_Follow_NoDuplicates regression-tests the
// follow-mode loop's de-duplication. The fake server, like the live
// control-api, returns next_cursor="" on partial pages — so a follow
// loop that re-uses a stale empty cursor would re-fetch and re-print
// the same events on every poll. The test drives multiple poll cycles
// (both with and without new events appearing between cycles) and
// asserts every event ID appears exactly once on stdout.
func TestRunInstanceEvents_Follow_NoDuplicates(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	// Seed two events before the follow loop starts.
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "k1", Payload: map[string]any{}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "k2", Payload: map[string]any{}})

	// Pipe stdout to capture printed lines without forking a process.
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
		// Tight poll-interval so the loop iterates several times in
		// the test's lifespan.
		done <- cli.RunInstanceEvents(ctx, []string{"--follow", "--poll-interval", "20ms", inst.ID})
	}()

	// Let the loop drain seeded events and poll a few times.
	time.Sleep(150 * time.Millisecond)
	// Append a third event mid-flight; the next poll should pick it
	// up exactly once.
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

	// Each event ID must appear exactly once. Lines are tab-separated
	// "occurred_at\tID\tkind".
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
	srv.State.AddNode(inst.ID, cli.Node{ID: "n1", InstanceID: inst.ID, NodeType: "a", State: "fresh"})
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
