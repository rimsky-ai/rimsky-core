// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// instances.go — `instance create/list/get/delete/nodes/events`.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"
)

// uuidPattern is the canonical lowercase hex UUID shape (v4 or v5).
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// LooksLikeUUID reports whether s is the canonical UUID shape.
func LooksLikeUUID(s string) bool { return uuidPattern.MatchString(s) }

// parseParams reads --params flag value, accepting either inline JSON or
// "@file" syntax pointing at a JSON file.
func parseParams(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	var raw []byte
	if strings.HasPrefix(s, "@") {
		b, err := os.ReadFile(strings.TrimPrefix(s, "@"))
		if err != nil {
			return nil, err
		}
		raw = b
	} else {
		raw = []byte(s)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("--params: %w", err)
	}
	return out, nil
}

// RunInstanceCreate implements `instance create`.
func RunInstanceCreate(ctx context.Context, args []string) int {
	var params, instanceKey string
	fs, common, endpoint, code := runWithCommon("instance create", args, func(fs *flag.FlagSet) {
		fs.StringVar(&params, "params", "", "JSON object or @file path")
		fs.StringVar(&instanceKey, "instance-key", "", "instance_key for the new row")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky instance create <template-ref> [--params ...] [--instance-key ...]")
		return 2
	}
	pp, err := parseParams(params)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	body := CreateInstanceRequest{Template: rest[0], Params: pp}
	if instanceKey != "" {
		k := instanceKey
		body.InstanceKey = &k
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	inst, err := c.CreateInstance(ctx, body)
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, inst)
		return 0
	}
	pairs := [][2]string{
		{"instance_id", inst.UUID()},
		{"template_hash", inst.TemplateHash},
	}
	if inst.InstanceKey != nil {
		pairs = append(pairs, [2]string{"instance_key", *inst.InstanceKey})
	}
	pairs = append(pairs, [2]string{"node_count", fmt.Sprintf("%d", inst.NodeCount)})
	EmitKV(os.Stdout, pairs)
	return 0
}

// RunInstanceList implements `instance list`.
func RunInstanceList(ctx context.Context, args []string) int {
	var template, keyPrefix string
	fs, common, endpoint, code := runWithCommon("instance list", args, func(fs *flag.FlagSet) {
		fs.StringVar(&template, "template", "", "filter by template hash")
		fs.StringVar(&keyPrefix, "key-prefix", "", "client-side filter on instance_key prefix")
	})
	if code != 0 {
		return code
	}
	_ = fs
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	all, err := pagedListInstances(ctx, c, ListInstancesQuery{TemplateHash: template})
	if err != nil {
		return reportError(err)
	}
	if keyPrefix != "" {
		filtered := all[:0]
		for _, inst := range all {
			if inst.InstanceKey != nil && strings.HasPrefix(*inst.InstanceKey, keyPrefix) {
				filtered = append(filtered, inst)
			}
		}
		all = filtered
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, all)
		return 0
	}
	rows := make([][]string, 0, len(all))
	for _, inst := range all {
		key := ""
		if inst.InstanceKey != nil {
			key = *inst.InstanceKey
		}
		state := "running"
		if inst.TerminatedAt != nil {
			state = "terminal"
		}
		rows = append(rows, []string{inst.UUID(), TruncHash(inst.TemplateHash), key, state})
	}
	EmitTable(os.Stdout, []string{"ID", "TEMPLATE", "KEY", "STATE"}, rows)
	return 0
}

func pagedListInstances(ctx context.Context, c *Client, q ListInstancesQuery) ([]Instance, error) {
	var all []Instance
	for {
		page, err := c.ListInstances(ctx, q)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Instances...)
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	return all, nil
}

// RunInstanceGet implements `instance get`.
func RunInstanceGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("instance get", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky instance get <id-or-key>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	inst, err := c.GetInstance(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, inst)
		return 0
	}
	pairs := [][2]string{
		{"id", inst.UUID()},
		{"template_hash", inst.TemplateHash},
	}
	if inst.InstanceKey != nil {
		pairs = append(pairs, [2]string{"instance_key", *inst.InstanceKey})
	}
	pairs = append(pairs, [2]string{"created_at", inst.CreatedAt})
	if inst.TerminatedAt != nil {
		pairs = append(pairs, [2]string{"terminated_at", *inst.TerminatedAt})
	}
	EmitKV(os.Stdout, pairs)
	return 0
}

// RunInstanceDelete implements `instance delete`.
func RunInstanceDelete(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("instance delete", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky instance delete <id-or-key>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if err := c.DeleteInstance(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "%s deleted\n", rest[0])
	return 0
}

// RunInstanceNodes implements `instance nodes`.
func RunInstanceNodes(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("instance nodes", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky instance nodes <id-or-key>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	resp, err := c.ListInstanceNodes(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, resp.Nodes)
		return 0
	}
	rows := make([][]string, 0, len(resp.Nodes))
	for _, n := range resp.Nodes {
		rows = append(rows, []string{n.ID, n.NodeType, n.State, n.Executor})
	}
	EmitTable(os.Stdout, []string{"ID", "TYPE", "STATE", "EXECUTOR"}, rows)
	return 0
}

// RunInstanceEvents implements `instance events`. With --follow, polls
// GET /events?instance_id=… until interrupted.
func RunInstanceEvents(ctx context.Context, args []string) int {
	var follow bool
	var pollInterval time.Duration
	fs, common, endpoint, code := runWithCommon("instance events", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&follow, "follow", false, "stream new events")
		fs.DurationVar(&pollInterval, "poll-interval", time.Second, "polling interval when --follow")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky instance events <id-or-key> [--follow]")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))

	id := rest[0]
	if !LooksLikeUUID(id) {
		// Resolve key → UUID via GET /instances/{key}.
		inst, err := c.GetInstance(ctx, id)
		if err != nil {
			return reportError(err)
		}
		id = inst.UUID()
	}

	signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// Track the highest event ID seen across iterations independent of
	// the page-level NextCursor. The live control-api only sets
	// NextCursor when a page is full (limit reached); on partial pages
	// it returns NextCursor="". Without an iteration-spanning watermark
	// the follow loop would re-fetch the same partial page every poll
	// and re-emit duplicates. lastSeenID is the source of truth for
	// "events after this point"; we pass it back as the cursor on each
	// poll so the server-side filter trims everything we've already
	// printed.
	var lastSeenID int64
	cursor := ""
	for {
		page, err := c.ListEvents(signalCtx, ListEventsQuery{InstanceID: id, Cursor: cursor, Limit: 100})
		if err != nil {
			if signalCtx.Err() != nil {
				return 0
			}
			return reportError(err)
		}
		for _, e := range page.Events {
			if e.ID <= lastSeenID {
				continue
			}
			lastSeenID = e.ID
			if common.Format == FormatJSON {
				_ = EmitJSON(os.Stdout, e)
			} else {
				fmt.Fprintf(os.Stdout, "%s\t%d\t%s\n", e.OccurredAt, e.ID, e.Kind)
			}
		}
		if page.NextCursor != "" {
			// Full page: continue draining without sleeping.
			cursor = page.NextCursor
			continue
		}
		if !follow {
			return 0
		}
		// Partial page (or empty): wait, then resume from the
		// last-seen ID rather than the empty NextCursor.
		cursor = ""
		if lastSeenID > 0 {
			cursor = fmt.Sprintf("%d", lastSeenID)
		}
		select {
		case <-signalCtx.Done():
			return 0
		case <-time.After(pollInterval):
		}
	}
}
