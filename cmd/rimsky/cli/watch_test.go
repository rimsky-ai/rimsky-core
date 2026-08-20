// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

func waitForListEventsCalls(t *testing.T, srv *clitest.Server, n int64) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("the follow loop to call ListEvents %d time(s): the backlog's pages, then the "+
		"poll that finds nothing new", n),
		func() bool { return srv.ListEventsHitCount() >= n })
}

func waitForInstancePolls(t *testing.T, srv *clitest.Server, n int64) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("watch to read the instance %d time(s), which it does once per poll", n),
		func() bool { return srv.GetInstanceHitCount() >= n })
}

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
		exit = <-done
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

func TestRunWatch_KindFlagFiltersServerSide(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	t1 := "2026-06-07T00:00:01Z"
	t2 := "2026-06-07T00:00:02Z"
	t3 := "2026-06-07T00:00:03Z"

	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "work_started", OccurredAt: t1, Payload: map[string]any{}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "work_completed", OccurredAt: t2, Payload: map[string]any{}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "work_started", OccurredAt: t3, Payload: map[string]any{}})

	terminatedAt, err := time.Parse(time.RFC3339, "2026-06-07T00:00:04Z")
	if err != nil {
		t.Fatal(err)
	}
	srv.State.SetInstanceTerminated(inst.ID, &terminatedAt)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "10s", "--kind", "work_started", inst.ID})
		}()
		exit = <-done
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}

	startedCount := strings.Count(out, "\twork_started\t")
	completedCount := strings.Count(out, "\twork_completed\t")
	if startedCount != 2 {
		t.Fatalf("watch --kind work_started rendered %d work_started lines, want 2 (server-side kind filter not threaded through); output:\n%s", startedCount, out)
	}
	if completedCount != 0 {
		t.Fatalf("watch --kind work_started rendered %d work_completed lines, want 0 (the --kind filter must exclude other kinds); output:\n%s", completedCount, out)
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
		exit = <-done
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

func TestRunWatch_DoesNotRescanFullHistoryOnSubsequentPolls(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.SetInstanceActivity(inst.ID, 0, 0)

	const total = 250
	base := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		srv.State.AddEvent(cli.Event{
			InstanceID: inst.ID,
			Kind:       "seq_event",
			OccurredAt: base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			Payload:    map[string]any{"seq": i},
		})
	}

	done := make(chan int, 1)
	exit := -1
	captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "10ms", inst.ID})
		}()
		exit = <-done
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0", exit)
	}

	const wantPages = 4
	if got := srv.ListEventsHitCount(); got != wantPages {
		t.Fatalf("ListEvents was called %d times across two polls of a 250-event backlog, want %d (watch is re-scanning full history on every poll)", got, wantPages)
	}
}

func TestRunWatch_PendingMessagesBlockIdle(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.SetInstanceActivityScript(inst.ID,
		clitest.InstanceActivity{PendingMessages: 1},
		clitest.InstanceActivity{PendingMessages: 1},
		clitest.InstanceActivity{PendingMessages: 1},
		clitest.InstanceActivity{},
		clitest.InstanceActivity{},
	)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "1ms", inst.ID})
		}()
		exit = <-done
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}
	if got := srv.State.InstanceActivityReads(inst.ID); got != 5 {
		t.Fatalf("watch read the instance's activity %d time(s), want 5: the three pending-message reads must "+
			"not count towards the idle confirmation, so the exit can only follow the two idle reads that end "+
			"the script", got)
	}
	if !strings.Contains(out, "idle") {
		t.Fatalf("watch output missing idle line; output:\n%s", out)
	}
}

func TestRunWatch_IdleConfirmationResetsOnTransientBusy(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.SetInstanceActivityScript(inst.ID,
		clitest.InstanceActivity{},
		clitest.InstanceActivity{RunningFrames: 1},
		clitest.InstanceActivity{},
		clitest.InstanceActivity{},
	)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "1ms", inst.ID})
		}()
		exit = <-done
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}
	if got := srv.State.InstanceActivityReads(inst.ID); got != 4 {
		t.Fatalf("watch read the instance's activity %d time(s), want 4: an exit at 1 means a single idle read "+
			"confirmed idle, and an exit at 3 means the running frame between the two idle reads did not reset "+
			"the confirmation", got)
	}
	if !strings.Contains(out, "idle") {
		t.Fatalf("watch output missing idle line; output:\n%s", out)
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
		exit = <-done
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
		waitForInstancePolls(t, srv, 4)
		select {
		case exit = <-done:
			t.Errorf("watch --until terminated exited (%d) on a merely-idle instance", exit)
		default:
		}
		terminatedAt, err := time.Parse(time.RFC3339, "2026-06-07T00:00:04Z")
		if err != nil {
			t.Fatal(err)
		}
		srv.State.SetInstanceTerminated(inst.ID, &terminatedAt)
		exit = <-done
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
	srv.State.SetInstanceActivityScript(inst.ID,
		clitest.InstanceActivity{RunningFrames: 1},
		clitest.InstanceActivity{RunningFrames: 1},
		clitest.InstanceActivity{RunningFrames: 1},
		clitest.InstanceActivity{},
		clitest.InstanceActivity{},
	)

	done := make(chan int, 1)
	exit := -1
	out := captureStdout(t, func() {
		go func() {
			done <- cli.RunWatch(context.Background(), []string{"--poll-interval", "1ms", inst.ID})
		}()
		exit = <-done
	})
	if got := srv.State.InstanceActivityReads(inst.ID); got != 5 {
		t.Fatalf("watch read the instance's activity %d time(s), want 5: a running frame must not count towards "+
			"the idle confirmation, so the exit can only follow the two idle reads that end the script", got)
	}
	if exit != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", exit, out)
	}
	if !strings.Contains(out, "idle") {
		t.Fatalf("watch output missing idle line; output:\n%s", out)
	}
}
