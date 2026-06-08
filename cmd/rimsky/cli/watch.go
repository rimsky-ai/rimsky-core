// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// watch.go — `watch <id>`, a top-level live-tail verb.
//
// watch is a streaming client-side tail of one instance's event log. The
// log is a single chronological stream: breakpoint hits land on it as
// `breakpoint.hit` rows, co-transactional with the hit (per
// S-observability-breakpoint-hit-event), so draining `/events` alone yields
// the whole timestamp-ordered feed — frame starts, state transitions, node
// terminations, and breakpoint hits all interleaved by their own
// `Event.OccurredAt` (RFC3339). The loop polls the event log (high-watermark
// cursor, the same pattern `instance events --follow` uses) plus the instance
// terminal flag, and exits when the instance terminates.
//
// It deliberately does NOT read the pending-breakpoint-hits route: that route
// is a point-in-time status surface (`instance status`, the MCP hits
// resource), not part of the chronological feed. Reading it here would print
// every hit twice — once as its `/events` row, once as the pending-state row.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"
)

// RunWatch implements the top-level `watch <id>` verb: poll the instance's
// event log (breakpoint hits included as `breakpoint.hit` rows) + terminal
// flag, printing the chronological feed until the instance terminates (or
// the user interrupts).
//
// The events high-watermark (lastSeenID) and full-page-drain logic mirror
// RunInstanceEvents' follow loop.
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
	for {
		// Accumulate this cycle's new event-log rows into one slice, then
		// stable-sort by timestamp and render. The event log is a single
		// stream (breakpoint hits included as `breakpoint.hit` rows), so one
		// drain is the whole chronological feed. lastSeenID does cross-cycle
		// dedup; the sort is within-cycle — pages arrive newest-first, so the
		// sort restores chronological print order.
		var batch []watchLine

		// Drain new event-log rows. Mirror the events follow-loop cursor
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
				e := e // capture for the render closure
				batch = append(batch, watchLine{
					ts:     parseWatchTime(e.OccurredAt),
					render: func() { printWatchEvent(common.Format, e) },
				})
			}
			if page.NextCursor == "" {
				break
			}
			nextCursor = page.NextCursor
		}

		// Render the cycle's rows in timestamp order. SliceStable keeps
		// equal-timestamp rows in drain order; pages arrive newest-first, so
		// the sort restores chronological print order within the cycle.
		sort.SliceStable(batch, func(i, j int) bool {
			return batch[i].ts.Before(batch[j].ts)
		})
		for _, line := range batch {
			line.render()
		}

		// Check the terminal flag; exit when set.
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

// watchLine is one accumulated row in a poll cycle's merged feed: its parsed
// timestamp drives the within-cycle chronological sort, and render emits the
// row (event or hit) once its sorted position is reached. Carrying a closure
// (rather than a tagged union of Event/hit) keeps the source-specific
// rendering — printWatchEvent vs printWatchHit, JSON vs text — local to the
// drain loops that built the row.
type watchLine struct {
	ts     time.Time
	render func()
}

// parseWatchTime parses a watch row's timestamp (RFC3339, the layout the
// server emits for both Event.OccurredAt and hit hit_at). An unparseable or
// empty value yields the zero time, sorting the row to the front of the cycle
// — a deterministic placement that never drops the row from the feed.
func parseWatchTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// printWatchEvent renders one event-log row in the watch feed. The event's
// own Kind labels the line (frame starts, state transitions, node
// terminations, etc. all surface here under their native kind). A
// `breakpoint.hit` row — the unified-stream form of a breakpoint hit — also
// surfaces its checkpoint/mode from the payload, so the feed shows where the
// run paused without a separate pending-hits read.
func printWatchEvent(format Format, e Event) {
	if format == FormatJSON {
		_ = EmitJSON(os.Stdout, map[string]any{"source": "event", "event": e})
		return
	}
	if e.Kind == "breakpoint.hit" {
		fmt.Fprintf(os.Stdout, "%s\tevent\tbreakpoint.hit\tcheckpoint=%v\tmode=%v\t%s\n",
			e.OccurredAt, e.Payload["checkpoint"], e.Payload["mode"], watchEventDetail(e))
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
