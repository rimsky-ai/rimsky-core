// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func RunWatch(ctx context.Context, args []string) int {
	var pollInterval time.Duration
	var until string
	fs, common, endpoint, code := runWithCommon("watch", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&pollInterval, "poll-interval", time.Second, "polling interval")
		fs.StringVar(&until, "until", "idle",
			"exit condition: idle (no open frame and no pending messages) or terminated (operator-terminated instance)")
	})
	if code != 0 {
		return code
	}
	if until != "idle" && until != "terminated" {
		fmt.Fprintln(os.Stderr, "rimsky watch: --until must be idle or terminated")
		return 2
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

	var lastSeenID int64
	var consecutiveIdle int
	for {
		var batch []watchLine

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
				e := e
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

		sort.SliceStable(batch, func(i, j int) bool {
			return batch[i].ts.Before(batch[j].ts)
		})
		for _, line := range batch {
			line.render()
		}

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
		if until == "idle" {
			idle, err := instanceIsIdle(signalCtx, c, id)
			if err != nil {
				if signalCtx.Err() != nil {
					return 0
				}
				return reportError(err)
			}
			if idle {
				consecutiveIdle++
				if consecutiveIdle >= idleConfirmPolls {
					printWatchIdle(common.Format, inst)
					return 0
				}
			} else {
				consecutiveIdle = 0
			}
		}

		select {
		case <-signalCtx.Done():
			return 0
		case <-time.After(pollInterval):
		}
	}
}

const idleConfirmPolls = 2

func instanceIsIdle(ctx context.Context, c *Client, id string) (bool, error) {
	pending := true
	msgs, err := c.ListInstanceMessages(ctx, id, ListMessagesQuery{Pending: &pending, Limit: 1})
	if err != nil {
		return false, err
	}
	if len(msgs.Messages) > 0 {
		return false, nil
	}
	frames, err := c.ListInstanceFrames(ctx, id, "running")
	if err != nil {
		return false, err
	}
	return len(frames.Frames) == 0, nil
}

type watchLine struct {
	ts     time.Time
	render func()
}

func parseWatchTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

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

func watchEventDetail(e Event) string {
	if e.NodeID != "" {
		return "node=" + e.NodeID
	}
	return ""
}

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

func printWatchIdle(format Format, inst *Instance) {
	if format == FormatJSON {
		_ = EmitJSON(os.Stdout, map[string]any{"source": "idle", "instance": inst})
		return
	}
	fmt.Fprintf(os.Stdout, "idle\tinstance %s has no open frame and no pending messages\n", inst.UUID())
}
