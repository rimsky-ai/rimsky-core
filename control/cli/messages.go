// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// messages.go — `rimsky messages tail|show` (plan G3).
//
// `tail` polls GET /instances/{id}/messages with a watermark on
// `received_at`; `show <id>` fetches one message via GET /messages/{id}.
// Both honour the standard --endpoint / --format flags.
//
//	@concept: message
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

// RunMessagesTail implements `messages tail`.
//
// Usage:
//
//	rimsky messages tail --instance <id> [--kind invalidate] \
//	    [--sender-kind operator] [--target node] \
//	    [--follow] [--poll-interval 1s]
func RunMessagesTail(ctx context.Context, args []string) int {
	var (
		instance, kind, senderKind, target string
		follow                             bool
		pollInterval                       time.Duration
	)
	fs, common, endpoint, code := runWithCommon("messages tail", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
		fs.StringVar(&kind, "kind", "", "filter by message kind (e.g. invalidate)")
		fs.StringVar(&senderKind, "sender-kind", "", "filter by sender_kind (operator|publisher|instance)")
		fs.StringVar(&target, "target", "", "filter by target node type")
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

	// Watermark: track the most recent `received_at` seen so we re-emit
	// only newer rows. Server pagination has its own cursor; this CLI-
	// side filter avoids relying on the empty-page NextCursor.
	var lastSeen time.Time
	for {
		page, err := c.ListInstanceMessages(signalCtx, id, ListMessagesQuery{
			Kind:           kind,
			SenderKind:     senderKind,
			Target:         target,
			DeliveredAfter: "", // we filter on received_at locally
			Limit:          100,
		})
		if err != nil {
			if signalCtx.Err() != nil {
				return 0
			}
			return reportError(err)
		}
		for _, m := range page.Messages {
			if !m.ReceivedAt.After(lastSeen) {
				continue
			}
			lastSeen = m.ReceivedAt
			if common.Format == FormatJSON {
				_ = EmitJSON(os.Stdout, m)
				continue
			}
			delivered := ""
			if m.DeliveredAt != nil {
				delivered = m.DeliveredAt.UTC().Format(time.RFC3339)
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n",
				m.ID, m.Kind, m.SenderKind, m.Sender, m.ReceivedAt.UTC().Format(time.RFC3339), delivered)
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

// RunMessagesShow implements `messages show <id>`.
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
		{"kind", m.Kind},
		{"sender", m.Sender},
		{"sender_kind", m.SenderKind},
		{"target", m.Target},
		{"received_at", m.ReceivedAt.UTC().Format(time.RFC3339)},
	}
	if m.DeliveredAt != nil {
		pairs = append(pairs, [2]string{"delivered_at", m.DeliveredAt.UTC().Format(time.RFC3339)})
	}
	if m.FrameID != "" {
		pairs = append(pairs, [2]string{"frame_id", m.FrameID})
	}
	if m.BackfillOperationID != "" {
		pairs = append(pairs, [2]string{"backfill_operation_id", m.BackfillOperationID})
	}
	if m.Cancelled {
		pairs = append(pairs, [2]string{"cancelled", "true"})
	}
	EmitKV(os.Stdout, pairs)
	return 0
}
