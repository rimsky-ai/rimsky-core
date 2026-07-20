// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
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

	signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

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
		for _, m := range page.Messages {
			if m.ReceivedAt.Before(lastSeen) {
				continue
			}
			if m.ReceivedAt.Equal(lastSeen) {
				if _, dup := seenAtLastSeen[m.ID]; dup {
					continue
				}
			} else {
				seenAtLastSeen = map[string]struct{}{}
			}
			lastSeen = m.ReceivedAt
			seenAtLastSeen[m.ID] = struct{}{}
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
