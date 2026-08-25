// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: message
package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

const messagesTailPageSize = 100

func readMessagesPage(ctx context.Context, c *Client, instanceID string, q ListMessagesQuery, wholeWindow bool) ([]MessageItem, bool, error) {
	if !wholeWindow {
		page, err := c.ListInstanceMessages(ctx, instanceID, q)
		if err != nil {
			return nil, false, err
		}
		return page.Messages, page.NextCursor != "", nil
	}
	messages, err := PageAll(func(cursor string) ([]MessageItem, string, error) {
		paged := q
		paged.Cursor = cursor
		page, err := c.ListInstanceMessages(ctx, instanceID, paged)
		if err != nil {
			return nil, "", err
		}
		return page.Messages, page.NextCursor, nil
	})
	return messages, false, err
}

func RunMessagesTail(ctx context.Context, args []string) int {
	var (
		instance, msgType, senderKind string
		since, until                  string
		pending                       bool
		follow                        bool
		pollInterval                  time.Duration
	)
	fs, common, endpoint, code := runWithCommon("messages tail",
		"--instance <id-or-key> [--since <RFC3339>] [--until <RFC3339>] [--pending] [filters...]", NoTable, args, func(fs *flag.FlagSet) {
			fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
			fs.StringVar(&msgType, "type", "", "filter by message type (e.g. ping/recheck)")
			fs.StringVar(&senderKind, "sender-kind", "", "filter by sender_kind (operator|publisher|instance)")
			fs.StringVar(&since, "since", "", "only messages delivered at or after this RFC3339 timestamp; an undelivered message is outside every delivery window, so use --pending to reach one")
			fs.StringVar(&until, "until", "", "only messages delivered at or before this RFC3339 timestamp; an undelivered message is outside every delivery window, so use --pending to reach one")
			fs.BoolVar(&pending, "pending", false, "only messages not yet delivered; incompatible with --since and --until")
			fs.BoolVar(&follow, "follow", false, "long-poll for new messages")
			// @decision: short-flags-single-letter
			fs.BoolVar(&follow, "f", false, "short for --follow")
			fs.DurationVar(&pollInterval, "poll-interval", time.Second, "poll interval when --follow")
		})
	if common == nil {
		return code
	}
	if instance == "" {
		return UsageError(fs)
	}
	if pending && (since != "" || until != "") {
		fmt.Fprintln(os.Stderr,
			"rimsky messages tail: --pending selects messages with no delivery instant, and --since/--until select a window over that instant; no message satisfies both. Name one.")
		return UsageError(fs)
	}

	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	id := instance
	if !LooksLikeUUID(id) {
		inst, err := c.GetInstance(ctx, id)
		if err != nil {
			return reportError(err)
		}
		id = inst.UUID()
	}

	// @decision: graceful-shutdown
	signalCtx, stopSignals := serverkit.ShutdownContext(ctx, slog.Default())
	defer stopSignals()

	query := ListMessagesQuery{
		Type:            msgType,
		SenderKind:      senderKind,
		DeliveredAfter:  since,
		DeliveredBefore: until,
		Limit:           messagesTailPageSize,
	}
	if pending {
		query.Pending = &pending
	}

	wholeWindow := !follow && (since != "" || until != "")

	var lastSeen time.Time
	seenAtLastSeen := map[string]struct{}{}
	for {
		messages, more, err := readMessagesPage(signalCtx, c, id, query, wholeWindow)
		if err != nil {
			if signalCtx.Err() != nil {
				return 0
			}
			return reportError(err)
		}
		watermark, seenAtWatermark := lastSeen, seenAtLastSeen
		var printed []MessageItem
		for _, m := range messages {
			if m.ReceivedAt.Before(watermark) {
				continue
			}
			if m.ReceivedAt.Equal(watermark) {
				if _, dup := seenAtWatermark[m.ID]; dup {
					continue
				}
			}
			printed = append(printed, m)
			if common.Format.Structured() {
				_ = EmitStructured(os.Stdout, common.Format, m)
				continue
			}
			delivered := ""
			if m.DeliveredAt != nil {
				delivered = m.DeliveredAt.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\n",
				m.ID, m.Type, m.SenderKind, m.Sender, m.ReceivedAt.UTC().Format(time.RFC3339), delivered)
		}
		lastSeen, seenAtLastSeen = advanceTailWatermark(watermark, seenAtWatermark, printed)
		if !follow {
			if more {
				fmt.Fprintf(os.Stderr,
					"rimsky messages tail: showing the most recent %d rows; name --since/--until to read a whole window\n",
					messagesTailPageSize)
			}
			return 0
		}
		select {
		case <-signalCtx.Done():
			return 0
		case <-time.After(pollInterval):
		}
	}
}

func RunMessagesShow(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("messages show", "<message-id>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	m, err := c.GetMessage(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, m, func() {
		pairs := [][2]string{
			{"id", m.ID},
			{"instance_id", m.InstanceID},
			{"type", m.Type},
			{"sender", m.Sender},
			{"sender_kind", m.SenderKind},
			{"received_at", m.ReceivedAt.UTC().Format(time.RFC3339)},
		}
		// @decision: message-sender-kind-discriminator
		if m.SenderSubject != "" {
			pairs = append(pairs, [2]string{"sender_subject", m.SenderSubject})
		}
		if m.DeliveredAt != nil {
			pairs = append(pairs, [2]string{"delivered_at", m.DeliveredAt.UTC().Format(time.RFC3339)})
		}
		if m.FrameID != "" {
			pairs = append(pairs, [2]string{"frame_id", m.FrameID})
		}
		if m.Cancelled {
			pairs = append(pairs, [2]string{"cancelled", "true"})
		}
		EmitKV(os.Stdout, pairs)
	})
}

// @concept: message
func advanceTailWatermark(
	watermark time.Time, seenAtWatermark map[string]struct{}, printed []MessageItem,
) (time.Time, map[string]struct{}) {
	next := watermark
	for _, m := range printed {
		if m.ReceivedAt.After(next) {
			next = m.ReceivedAt
		}
	}
	seen := map[string]struct{}{}
	if next.Equal(watermark) {
		for id := range seenAtWatermark {
			seen[id] = struct{}{}
		}
	}
	for _, m := range printed {
		if m.ReceivedAt.Equal(next) {
			seen[m.ID] = struct{}{}
		}
	}
	return next, seen
}
