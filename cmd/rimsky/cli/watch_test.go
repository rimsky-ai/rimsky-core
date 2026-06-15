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

	t1 := "2026-06-07T00:00:01Z"
	t2 := "2026-06-07T00:00:02Z"
	t3 := "2026-06-07T00:00:03Z"

	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "event_a", OccurredAt: t1, Payload: map[string]any{}})
	// @deliberate: breakpoint hits ride the unified /events stream as
	// `breakpoint.hit` rows (co-transactional with the hit), not a separate
	// pending-hits read. watch drains /events alone, so the seed shape must
	// match production; printWatchEvent renders the checkpoint/mode detail.
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
		// @constraint: a terminal instance must exit on the first iteration
		// regardless of the (deliberately long) poll interval; the 5s timeout
		// guards against a regression that would hang the loop on the 10s tick.
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

	// @deliberate: compare line indices, not byte offsets — trailing fields
	// on a line (varying detail strings) must not perturb the ordering check.
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
