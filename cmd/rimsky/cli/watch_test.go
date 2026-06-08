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

// TestRunWatch_Chronological pins the advertised contract: `rimsky watch`
// renders the instance's event log (breakpoint hits included as
// `breakpoint.hit` rows) and the terminal line in one timestamp-ordered
// feed, not a source-grouped one.
//
// Seed a single poll window with three /events rows whose timestamps are NOT
// in arrival order — a breakpoint.hit between two ordinary events:
//
//	event A         occurred_at = t1
//	breakpoint.hit  occurred_at = t2   (t1 < t2 < t3)
//	event B         occurred_at = t3
//
// A faithful chronological feed prints A, then the hit, then B, then the
// terminal line — the breakpoint-hit line must sit strictly BETWEEN the two
// event lines. The /events page arrives newest-first, so a feed that printed
// the page verbatim (B, hit, A) would fail the order check; the within-cycle
// timestamp sort is what restores A, hit, B.
func TestRunWatch_Chronological(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	// Three timestamps strictly ordered t1 < t2 < t3, all inside one poll
	// window. The hit's t2 sits between the two events' t1/t3, so a
	// source-grouped feed cannot reproduce the true chronological order.
	t1 := "2026-06-07T00:00:01Z"
	t2 := "2026-06-07T00:00:02Z"
	t3 := "2026-06-07T00:00:03Z"

	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "event_a", OccurredAt: t1, Payload: map[string]any{}})
	// The breakpoint hit is part of the unified /events stream (a
	// `breakpoint.hit` row, co-transactional with the hit) — NOT a separate
	// pending-hits read. watch drains /events alone, so seed it as an event;
	// printWatchEvent renders breakpoint.hit with its checkpoint/mode detail.
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
		// A terminal instance must exit on the first iteration regardless of
		// the (deliberately long) poll interval; the timeout guards against a
		// regression that would hang the loop.
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

	// Locate the three lines by stable markers. Each source prints its own
	// line; we compare line indices, not byte offsets, so trailing fields on
	// a line cannot perturb the ordering check.
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

	// The breakpoint-hit line (t2) must sit strictly between event A (t1) and
	// event B (t3): true timestamp order is A, hit, B. A source-grouped feed
	// places the hit after both events (idxHit > idxB), failing this.
	if !(idxA < idxHit && idxHit < idxB) {
		t.Errorf("watch feed not in timestamp order: want A(%d) < hit(%d) < B(%d); output:\n%s",
			idxA, idxHit, idxB, out)
	}
}
