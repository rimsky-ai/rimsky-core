// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func TestRunWatch_Chronological(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	t1 := "2026-06-07T00:00:01Z"
	t2 := "2026-06-07T00:00:02Z"
	t3 := "2026-06-07T00:00:03Z"

	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "event_a", OccurredAt: t1, Payload: map[string]any{}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "breakpoint.hit", OccurredAt: t2, Payload: map[string]any{"checkpoint": "between_events", "mode": "stop"}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "event_b", OccurredAt: t3, Payload: map[string]any{}})

	terminatedAt, err := time.Parse(time.RFC3339, "2026-06-07T00:00:04Z")
	if err != nil {
		t.Fatal(err)
	}
	srv.State.SetInstanceTerminated(inst.ID, &terminatedAt)

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
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	idxA, idxHit, idxB := -1, -1, -1
	for i, ln := range lines {
		switch {
		case strings.Contains(ln, "\tevent_a\t"):
			idxA = i
		case strings.Contains(ln, "\tevent_b\t"):
			idxB = i
		case strings.Contains(ln, "breakpoint.hit") && strings.Contains(ln, "checkpoint=between_events"):
			idxHit = i
		}
	}
	if idxA < 0 || idxHit < 0 || idxB < 0 {
		t.Fatalf("watch output missing one of the seeded lines (A=%d hit=%d B=%d); output:\n%s",
			idxA, idxHit, idxB, out)
	}

	if !(idxA < idxHit && idxHit < idxB) {
		t.Errorf("watch feed not in timestamp order: want A(%d) < hit(%d) < B(%d); output:\n%s",
			idxA, idxHit, idxB, out)
	}
}

func TestRunWatch_DrainsFullBacklogAcrossMultiplePagesInChronologicalOrder(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	const total = 150
	base := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		occurredAt := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		srv.State.AddEvent(cli.Event{
			InstanceID: inst.ID,
			Kind:       "seq_event",
			OccurredAt: occurredAt,
			Payload:    map[string]any{"seq": i},
		})
	}

	terminatedAt := base.Add(time.Duration(total) * time.Second)
	srv.State.SetInstanceTerminated(inst.ID, &terminatedAt)

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
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var seqLines []string
	for _, ln := range lines {
		if strings.Contains(ln, "\tseq_event\t") {
			seqLines = append(seqLines, ln)
		}
	}
	if len(seqLines) != total {
		t.Fatalf("watch rendered %d seq_event lines, want %d (backlog must fully drain across cursor pages, no drops, no dupes); output:\n%s",
			len(seqLines), total, out)
	}

	var lastTS time.Time
	for i, ln := range seqLines {
		fields := strings.SplitN(ln, "\t", 2)
		if len(fields) < 1 {
			t.Fatalf("malformed watch line %q", ln)
		}
		ts, err := time.Parse(time.RFC3339, fields[0])
		if err != nil {
			t.Fatalf("unparsable timestamp in line %q: %v", ln, err)
		}
		if i > 0 && ts.Before(lastTS) {
			t.Fatalf("watch feed not chronologically ordered across paginated pages at line %d: %s", i, ln)
		}
		lastTS = ts
	}
}

func TestRunWatch_ExitsOnIdleInstance(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "terminal/success",
		OccurredAt: "2026-06-07T00:00:01Z", Payload: map[string]any{}})
	srv.State.SetInstanceActivity(inst.ID, 0, 0)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "50ms", inst.ID})
		}()
		select {
		case exit = <-done:
		case <-time.After(5 * time.Second):
			t.Error("watch did not exit on an idle instance (no open frame, no pending messages)")
		}
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}
	if !strings.Contains(out, "idle") {
		t.Fatalf("watch output missing idle line; output:\n%s", out)
	}
}

func TestRunWatch_UntilTerminatedIgnoresIdle(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.SetInstanceActivity(inst.ID, 0, 0)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(),
				[]string{"--poll-interval", "50ms", "--until", "terminated", inst.ID})
		}()
		select {
		case exit = <-done:
			t.Errorf("watch --until terminated exited (%d) on a merely-idle instance", exit)
		case <-time.After(500 * time.Millisecond):
		}
		terminatedAt, err := time.Parse(time.RFC3339, "2026-06-07T00:00:04Z")
		if err != nil {
			t.Fatal(err)
		}
		srv.State.SetInstanceTerminated(inst.ID, &terminatedAt)
		select {
		case exit = <-done:
		case <-time.After(5 * time.Second):
			t.Error("watch --until terminated did not exit after termination")
		}
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}
	if !strings.Contains(out, "terminated") {
		t.Fatalf("watch output missing terminal line; output:\n%s", out)
	}
}

func TestRunWatch_IdleWaitsForOpenFrame(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.SetInstanceActivity(inst.ID, 0, 1)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "50ms", inst.ID})
		}()
		select {
		case exit = <-done:
			t.Errorf("watch exited (%d) while a frame was still running", exit)
		case <-time.After(500 * time.Millisecond):
		}
		srv.State.SetInstanceActivity(inst.ID, 0, 0)
		select {
		case exit = <-done:
		case <-time.After(5 * time.Second):
			t.Error("watch did not exit after the frame resolved")
		}
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}
	if !strings.Contains(out, "idle") {
		t.Fatalf("watch output missing idle line; output:\n%s", out)
	}
}
