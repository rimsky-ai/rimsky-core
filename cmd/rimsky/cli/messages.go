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

func RunMessagesTail(ctx context.Context, args []string) int {
	var (
		instance, msgType, senderKind string
		follow                        bool
		pollInterval                  time.Duration
	)
	fs, common, endpoint, code := runWithCommon("messages tail", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
		fs.StringVar(&msgType, "type", "", "filter by message type (e.g. ping/recheck)")
		fs.StringVar(&senderKind, "sender-kind", "", "filter by sender_kind (operator|publisher|instance)")
		fs.BoolVar(&follow, "follow", false, "long-poll for new messages")
		fs.DurationVar(&pollInterval, "poll-interval", time.Second, "poll interval when --follow")
	})
	if code != 0 {
		return code
	}
	_ = fs
	if instance == "" {
		fmt.Fprintln(os.Stderr, "usage: rimsky messages tail --instance <id-or-key> [filters...]")
		return 2
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

	var lastSeen time.Time
	seenAtLastSeen := map[string]struct{}{}
	for {
		page, err := c.ListInstanceMessages(signalCtx, id, ListMessagesQuery{
			Type:           msgType,
			SenderKind:     senderKind,
			DeliveredAfter: "",
			Limit:          100,
		})
		if err != nil {
			if signalCtx.Err() != nil {
				return 0
			}
			return reportError(err)
		}
		watermark, seenAtWatermark := lastSeen, seenAtLastSeen
		var printed []MessageItem
		for _, m := range page.Messages {
			if m.ReceivedAt.Before(watermark) {
				continue
			}
			if m.ReceivedAt.Equal(watermark) {
				if _, dup := seenAtWatermark[m.ID]; dup {
					continue
				}
			}
			printed = append(printed, m)
			if common.Format == FormatJSON {
				_ = EmitJSON(os.Stdout, m)
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
	fs, common, endpoint, code := runWithCommon("messages show", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky messages show <message-id>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	m, err := c.GetMessage(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, m)
		return 0
	}
	pairs := [][2]string{
		{"id", m.ID},
		{"instance_id", m.InstanceID},
		{"type", m.Type},
		{"sender", m.Sender},
		{"sender_kind", m.SenderKind},
		{"received_at", m.ReceivedAt.UTC().Format(time.RFC3339)},
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
	return 0
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
