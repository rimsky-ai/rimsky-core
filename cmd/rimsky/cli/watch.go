// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// watch.go — `watch <id>`, a top-level live-tail verb.
//
// watch is a client-side aggregator (like `instance status`, but
// streaming): it interleaves three per-instance read sources into one
// chronological feed — the event log (high-watermark cursor, the same
// pattern `instance events --follow` uses), pending breakpoint hits
// (since-seq watermark), and the instance terminal flag — and exits when
// the instance terminates. No new server endpoint; it composes existing
// reads plus the breakpoint-hits route.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

// RunWatch implements the top-level `watch <id>` verb: poll the instance's
// event log + pending breakpoint hits + terminal flag, printing a combined
// feed until the instance terminates (or the user interrupts).
//
// The events high-watermark (lastSeenID) and full-page-drain logic mirror
// RunInstanceEvents' follow loop; the hits watermark (sinceSeq) mirrors the
// breakpoint-hits route's since cursor.
func RunWatch(ctx context.Context, args []string) int {
	var pollInterval time.Duration
	fs, common, endpoint, code := runWithCommon("watch", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&pollInterval, "poll-interval", time.Second, "polling interval")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky watch <id-or-key> [--poll-interval ...]")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))

	id := rest[0]
	if !LooksLikeUUID(id) {
		inst, err := c.GetInstance(ctx, id)
		if err != nil {
			return reportError(err)
		}
		id = inst.UUID()
	}

	signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	var lastSeenID int64 // event-log high-watermark
	var sinceSeq int64   // breakpoint-hit seq watermark
	for {
		// 1. Drain new events. Mirror the events follow-loop cursor
		// discipline (RunInstanceEvents): the live control-api reads the
		// event log newest-first ((occurred_at, id) DESC) and NextCursor is
		// an OPAQUE base64 keyset token — pass that exact token back to walk
		// the backlog, never a fabricated numeric seq (which 500s with
		// "events.list: bad cursor", issue #1). NextCursor is set only on a
		// full page; "" signals the backlog is drained.
		//
		// lastSeenID is a purely-local dedup high-watermark, never sent as a
		// cursor. Pages arrive newest-first, so the skip test compares
		// against a per-poll snapshot (prevSeen): advancing the watermark
		// mid-drain would suppress every older event on the following pages.
		prevSeen := lastSeenID
		nextCursor := ""
		for {
			page, err := c.ListEvents(signalCtx, ListEventsQuery{InstanceID: id, Cursor: nextCursor, Limit: 100})
			if err != nil {
				if signalCtx.Err() != nil {
					return 0
				}
				return reportError(err)
			}
			for _, e := range page.Events {
				if e.ID <= prevSeen {
					continue
				}
				if e.ID > lastSeenID {
					lastSeenID = e.ID
				}
				printWatchEvent(common.Format, e)
			}
			if page.NextCursor == "" {
				break
			}
			nextCursor = page.NextCursor
		}

		// 2. Drain new breakpoint hits past the seq watermark. Drain ALL
		// pages this cycle (not just the first) so a backlog larger than
		// one page is fully surfaced before the terminal check below —
		// otherwise a terminating instance with >limit pending hits would
		// exit with its hit tail unprinted. Mirrors the event drain above.
		for {
			hits, err := c.ListBreakpointHits(signalCtx, id, sinceSeq, 100)
			if err != nil {
				if signalCtx.Err() != nil {
					return 0
				}
				return reportError(err)
			}
			for _, h := range hits.Hits {
				printWatchHit(common.Format, h)
			}
			if hits.NextSince > sinceSeq {
				sinceSeq = hits.NextSince
			}
			if !hits.Truncated {
				break
			}
		}

		// 3. Check the terminal flag; exit when set.
		inst, err := c.GetInstance(signalCtx, id)
		if err != nil {
			if signalCtx.Err() != nil {
				return 0
			}
			return reportError(err)
		}
		if inst.TerminatedAt != nil {
			printWatchTerminal(common.Format, inst)
			return 0
		}

		select {
		case <-signalCtx.Done():
			return 0
		case <-time.After(pollInterval):
		}
	}
}

// printWatchEvent renders one event-log row in the watch feed. The event's
// own Kind labels the line (frame starts, state transitions, node
// terminations, etc. all surface here under their native kind).
func printWatchEvent(format Format, e Event) {
	if format == FormatJSON {
		_ = EmitJSON(os.Stdout, map[string]any{"source": "event", "event": e})
		return
	}
	fmt.Fprintf(os.Stdout, "%s\tevent\t%s\t%s\n", e.OccurredAt, e.Kind, watchEventDetail(e))
}

// watchEventDetail extracts a terse node/frame identifier from an event for
// the human feed, falling back to empty when the payload carries neither.
func watchEventDetail(e Event) string {
	if e.NodeID != "" {
		return "node=" + e.NodeID
	}
	return ""
}

// printWatchHit renders one pending breakpoint hit in the watch feed.
func printWatchHit(format Format, h map[string]any) {
	if format == FormatJSON {
		_ = EmitJSON(os.Stdout, map[string]any{"source": "breakpoint.hit", "hit": h})
		return
	}
	fmt.Fprintf(os.Stdout, "%v\tbreakpoint.hit\tseq=%v\tcheckpoint=%v\tmode=%v\n",
		h["hit_at"], h["seq"], h["checkpoint"], h["mode"])
}

// printWatchTerminal renders the final terminal line and ends the feed.
func printWatchTerminal(format Format, inst *Instance) {
	if format == FormatJSON {
		_ = EmitJSON(os.Stdout, map[string]any{"source": "terminal", "instance": inst})
		return
	}
	terminatedAt := ""
	if inst.TerminatedAt != nil {
		terminatedAt = *inst.TerminatedAt
	}
	fmt.Fprintf(os.Stdout, "%s\tterminal\tinstance %s terminated\n", terminatedAt, inst.UUID())
}
