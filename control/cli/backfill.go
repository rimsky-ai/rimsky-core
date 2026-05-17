// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// backfill.go — `rimsky-cli backfill {create,list,show,cancel,partitions}`
// (plan G2). Thin wrappers over F4 control-api routes.
//
//	@concept: backfill
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// RunBackfillCreate implements `backfill create`.
//
// Usage:
//
//	rimsky-cli backfill create --instance <id> --node <node_type> \
//	    [--range 2024-01-01..2024-09-30] [--reason "...""]
//
// `--range start..end` is shorthand that becomes the JSON
// `{"date_range": {"start": "<start>", "end": "<end>"}}` payload that
// the receiving node's `{{trigger.message.payload.date_range}}`
// substitution reads.
func RunBackfillCreate(ctx context.Context, args []string) int {
	var (
		instance, node, dateRange, reason, paramsRaw string
	)
	fs, common, endpoint, code := runWithCommon("backfill create", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
		fs.StringVar(&node, "node", "", "target node_type within the instance (required)")
		fs.StringVar(&dateRange, "range", "", "shorthand date range: start..end (RFC3339 or YYYY-MM-DD)")
		fs.StringVar(&reason, "reason", "", "operator-visible reason")
		fs.StringVar(&paramsRaw, "params", "", "raw JSON payload override (or @file)")
	})
	if code != 0 {
		return code
	}
	_ = fs
	if instance == "" || node == "" {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli backfill create --instance <id> --node <type> [--range start..end] [--reason ...]")
		return 2
	}

	body := CreateBackfillRequest{TargetNode: node, Reason: reason}
	if dateRange != "" {
		parts := strings.SplitN(dateRange, "..", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			fmt.Fprintln(os.Stderr, "--range: expected start..end (got "+dateRange+")")
			return 2
		}
		payload := map[string]any{
			"date_range": map[string]any{
				"start": parts[0],
				"end":   parts[1],
			},
		}
		raw, _ := json.Marshal(payload)
		body.PartitionRequestOverride = raw
	} else if paramsRaw != "" {
		pp, err := parseParams(paramsRaw)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		raw, _ := json.Marshal(pp)
		body.PartitionRequestOverride = raw
	}

	c := NewClient(endpoint)
	id := instance
	if !LooksLikeUUID(id) {
		inst, err := c.GetInstance(ctx, id)
		if err != nil {
			return reportError(err)
		}
		id = inst.UUID()
	}
	resp, err := c.CreateBackfill(ctx, id, body)
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, resp)
		return 0
	}
	EmitKV(os.Stdout, [][2]string{
		{"backfill_operation_id", resp.BackfillOperationID},
		{"message_id", resp.MessageID},
	})
	return 0
}

// RunBackfillList implements `backfill list --instance <id>`.
func RunBackfillList(ctx context.Context, args []string) int {
	var instance string
	fs, common, endpoint, code := runWithCommon("backfill list", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
	})
	if code != 0 {
		return code
	}
	_ = fs
	if instance == "" {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli backfill list --instance <id-or-key>")
		return 2
	}
	c := NewClient(endpoint)
	id := instance
	if !LooksLikeUUID(id) {
		inst, err := c.GetInstance(ctx, id)
		if err != nil {
			return reportError(err)
		}
		id = inst.UUID()
	}
	resp, err := c.ListBackfills(ctx, id)
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, resp.Backfills)
		return 0
	}
	rows := make([][]string, 0, len(resp.Backfills))
	for _, b := range resp.Backfills {
		delivered := ""
		if b.DeliveredAt != nil {
			delivered = b.DeliveredAt.UTC().Format(time.RFC3339)
		}
		cancelled := ""
		if b.Cancelled {
			cancelled = "cancelled"
		}
		rows = append(rows, []string{
			b.OperationID, b.TargetNode, b.ReceivedAt.UTC().Format(time.RFC3339), delivered, cancelled,
		})
	}
	EmitTable(os.Stdout, []string{"OPERATION_ID", "TARGET", "RECEIVED_AT", "DELIVERED_AT", "STATE"}, rows)
	return 0
}

// RunBackfillShow implements `backfill show <op-id> [--partitions]`.
func RunBackfillShow(ctx context.Context, args []string) int {
	var withPartitions bool
	fs, common, endpoint, code := runWithCommon("backfill show", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&withPartitions, "partitions", false, "include per-child run drill-down")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli backfill show <operation-id> [--partitions]")
		return 2
	}
	c := NewClient(endpoint)
	b, err := c.GetBackfill(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		out := map[string]any{"backfill": b}
		if withPartitions {
			parts, err := c.GetBackfillPartitions(ctx, rest[0])
			if err != nil {
				return reportError(err)
			}
			out["partitions"] = parts.Partitions
		}
		_ = EmitJSON(os.Stdout, out)
		return 0
	}
	delivered := ""
	if b.DeliveredAt != nil {
		delivered = b.DeliveredAt.UTC().Format(time.RFC3339)
	}
	pairs := [][2]string{
		{"operation_id", b.OperationID},
		{"target_node", b.TargetNode},
		{"received_at", b.ReceivedAt.UTC().Format(time.RFC3339)},
		{"delivered_at", delivered},
	}
	if b.FrameID != "" {
		pairs = append(pairs, [2]string{"frame_id", b.FrameID})
	}
	if b.Reason != "" {
		pairs = append(pairs, [2]string{"reason", b.Reason})
	}
	if b.Cancelled {
		pairs = append(pairs, [2]string{"cancelled", "true"})
	}
	EmitKV(os.Stdout, pairs)
	if withPartitions {
		parts, err := c.GetBackfillPartitions(ctx, rest[0])
		if err != nil {
			return reportError(err)
		}
		rows := make([][]string, 0, len(parts.Partitions))
		for _, p := range parts.Partitions {
			rows = append(rows, []string{p.RunID, p.NodeID, p.ChildKey, p.State, p.LastOutcome})
		}
		fmt.Println()
		EmitTable(os.Stdout, []string{"RUN_ID", "NODE_ID", "CHILD_KEY", "STATE", "LAST_OUTCOME"}, rows)
	}
	return 0
}

// RunBackfillCancel implements `backfill cancel <op-id>`.
func RunBackfillCancel(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("backfill cancel", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli backfill cancel <operation-id>")
		return 2
	}
	c := NewClient(endpoint)
	out, err := c.CancelBackfill(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, out)
		return 0
	}
	fmt.Fprintf(os.Stdout, "cancelled (operation_id=%s, messages_voided=%v)\n", rest[0], out["messages_voided"])
	return 0
}
