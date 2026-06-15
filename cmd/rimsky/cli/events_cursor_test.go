// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

// TestEventsFollowOpaqueCursor drives `instance events --follow` across more
// than one full page of events against the honest clitest mock — the mock
// now mirrors the live control-api's real cursor contract: results are
// newest-first ((occurred_at, id) DESC), `next_cursor` is an opaque base64
// keyset token, and a non-base64 cursor is rejected with a 500
// (`events.list: bad cursor`) exactly as the real persistence layer rejects
// it.
//
// This is the regression test for issue #1: the CLI used to fabricate a
// numeric cursor (`fmt.Sprintf("%d", lastSeenID)`), which the real server —
// and now the honest mock — rejects, so `rimsky watch` / `instance events
// --follow` 500'd on the first cursor advance. The fixed CLI passes the
// server's opaque `next_cursor` token straight back and keeps `lastSeenID`
// purely as a local dedup high-watermark.
//
// The assertion is the cross-page invariant: every seeded event ID prints
// exactly once and the follow loop never errors. With >1 full page this
// only holds if (a) the CLI pages through the backlog via the opaque token
// rather than a numeric seq, and (b) the dedup high-watermark survives the
// newest-first page order (an event on an older page must still print even
// though a higher-ID event on the newer page was seen first).
func TestEventsFollowOpaqueCursor(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	// @constraint: seed two full pages plus a partial third (limit is 100);
	// distinct strictly-increasing occurred_at timestamps make the keyset
	// total and page boundaries deterministic regardless of seeding speed.
	const total = 250
	base := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		srv.State.AddEvent(cli.Event{
			InstanceID: inst.ID,
			Kind:       fmt.Sprintf("k%d", i+1),
			Payload:    map[string]any{},
			OccurredAt: base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
		})
	}

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = wOut
	t.Cleanup(func() { os.Stdout = saved })

	collected := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 256*1024)
		tmp := make([]byte, 4096)
		for {
			n, rerr := rOut.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if rerr != nil {
				break
			}
		}
		collected <- string(buf)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- cli.RunInstanceEvents(ctx, []string{"--follow", "--poll-interval", "20ms", inst.ID})
	}()

	// @deliberate: 400ms lets the 20ms poll page through all three pages and
	// poll a few more times after draining, before cancel.
	time.Sleep(400 * time.Millisecond)
	cancel()
	exit := <-done
	os.Stdout = saved
	_ = wOut.Close()
	out := <-collected

	if exit != 0 {
		t.Fatalf("follow loop errored (exit %d); a non-base64 cursor was rejected by the honest mock. output:\n%s", exit, out)
	}

	for i := 0; i < total; i++ {
		want := fmt.Sprintf("\t%d\tk%d\n", i+1, i+1)
		if c := strings.Count(out, want); c != 1 {
			t.Fatalf("event id=%d appeared %d times, want 1 (cross-page dedup / pagination broken)", i+1, c)
		}
	}
}
