// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instances.go — `instance create/list/get/status/delete/kill/nodes/events`.
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

// RunInstanceKill implements `instance kill`: force-terminate a (possibly
// stuck) instance via POST /instances/{idOrKey}/terminate. Termination is
// destructive — it abandons any in-flight node-runs — so it refuses unless
// the operator opts in with --force or the common --yes flag.
//
// kill makes the instance terminal but does NOT free its instance_key; the
// follow-up `rimsky instance delete <id>` removes the row (its terminal
// guard now passes) and frees the key.
func RunInstanceKill(ctx context.Context, args []string) int {
	var reason string
	var force bool
	fs, common, endpoint, code := runWithCommon("instance kill", args, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "reason recorded on the teardown audit event")
		fs.BoolVar(&force, "force", false, "confirm forced termination")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky instance kill <id-or-key> [--reason ...] --force")
		return 2
	}
	if !force && !common.Yes {
		fmt.Fprintln(os.Stderr, "refusing to terminate without --force")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	inst, err := c.TerminateInstance(ctx, rest[0], reason)
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
	if inst.TerminatedAt != nil {
		pairs = append(pairs, [2]string{"terminated_at", *inst.TerminatedAt})
	}
	EmitKV(os.Stdout, pairs)
	fmt.Fprintf(os.Stdout, "\ninstance terminated; run `rimsky instance delete %s` to free the instance key\n", rest[0])
	return 0
}

// statusReportEventLimit bounds the recent-events and pending-hits fan-out
// for `instance status`. Status is a snapshot, not a full history dump.
const statusReportEventLimit = 50

// InstanceStatus is the assembled one-shot snapshot `instance status`
// renders: the instance projection plus the three per-instance read
// fan-outs (nodes, recent events, pending breakpoint hits). The JSON
// shape doubles as the `-o json` envelope.
type InstanceStatus struct {
	Instance       *Instance        `json:"instance"`
	Nodes          []Node           `json:"nodes"`
	RecentEvents   []Event          `json:"recent_events"`
	BreakpointHits []map[string]any `json:"breakpoint_hits"`
}

// gatherInstanceStatus fans out GetInstance + ListInstanceNodes +
// ListEvents + ListBreakpointHits for one already-resolved instance UUID
// and assembles them into an InstanceStatus, the one-shot snapshot
// `instance status` renders. The four reads are independent; a failure on
// any is returned to the caller. (`watch` does not use this — it runs its
// own incremental poll loop over the same read sources.)
func gatherInstanceStatus(ctx context.Context, c *Client, uuid string) (*InstanceStatus, error) {
	inst, err := c.GetInstance(ctx, uuid)
	if err != nil {
		return nil, err
	}
	nodesResp, err := c.ListInstanceNodes(ctx, uuid)
	if err != nil {
		return nil, err
	}
	eventsResp, err := c.ListEvents(ctx, ListEventsQuery{InstanceID: uuid, Limit: statusReportEventLimit})
	if err != nil {
		return nil, err
	}
	hitsResp, err := c.ListBreakpointHits(ctx, uuid, 0, statusReportEventLimit)
	if err != nil {
		return nil, err
	}
	return &InstanceStatus{
		Instance:       inst,
		Nodes:          nodesResp.Nodes,
		RecentEvents:   eventsResp.Events,
		BreakpointHits: hitsResp.Hits,
	}, nil
}

// RunInstanceStatus implements `instance status`: a client-side aggregator
// that fans out across the existing per-instance read endpoints (instance
// projection, node states, recent events, pending breakpoint hits) and
// renders one combined snapshot. No new server endpoint — purely a
// composition over reads.
func RunInstanceStatus(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("instance status", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky instance status <id-or-key>")
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

	status, err := gatherInstanceStatus(ctx, c, id)
	if err != nil {
		return reportError(err)
	}

	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, status)
		return 0
	}
	printInstanceStatus(status)
	return 0
}

// printInstanceStatus renders the human view of an InstanceStatus: an
// instance header (KV), a per-node state table, a recent-events table, and
// a pending-hits table.
func printInstanceStatus(status *InstanceStatus) {
	inst := status.Instance
	state := "running"
	if inst.TerminatedAt != nil {
		state = "terminal"
	}
	pairs := [][2]string{
		{"id", inst.UUID()},
		{"state", state},
		{"template_hash", inst.TemplateHash},
	}
	if inst.InstanceKey != nil {
		pairs = append(pairs, [2]string{"instance_key", *inst.InstanceKey})
	}
	if inst.TerminatedAt != nil {
		pairs = append(pairs, [2]string{"terminated_at", *inst.TerminatedAt})
	}
	EmitKV(os.Stdout, pairs)

	fmt.Fprintln(os.Stdout, "\nNodes:")
	nodeRows := make([][]string, 0, len(status.Nodes))
	for _, n := range status.Nodes {
		hb := ""
		if n.LastHeartbeatAt != nil {
			hb = *n.LastHeartbeatAt
		}
		nodeRows = append(nodeRows, []string{
			n.ID, n.NodeType, n.State, n.CurrentErrorClass,
			fmt.Sprintf("%d", n.RetryCounter), hb,
		})
	}
	EmitTable(os.Stdout, []string{"ID", "TYPE", "STATE", "ERROR_CLASS", "RETRIES", "LAST_HEARTBEAT"}, nodeRows)

	fmt.Fprintln(os.Stdout, "\nRecent events:")
	eventRows := make([][]string, 0, len(status.RecentEvents))
	for _, e := range status.RecentEvents {
		eventRows = append(eventRows, []string{e.OccurredAt, fmt.Sprintf("%d", e.ID), e.Kind})
	}
	EmitTable(os.Stdout, []string{"OCCURRED_AT", "ID", "KIND"}, eventRows)

	fmt.Fprintln(os.Stdout, "\nPending breakpoint hits:")
	hitRows := make([][]string, 0, len(status.BreakpointHits))
	for _, h := range status.BreakpointHits {
		hitRows = append(hitRows, []string{
			fmt.Sprintf("%v", h["seq"]),
			fmt.Sprintf("%v", h["checkpoint"]),
			fmt.Sprintf("%v", h["mode"]),
			fmt.Sprintf("%v", h["hit_id"]),
		})
	}
	EmitTable(os.Stdout, []string{"SEQ", "CHECKPOINT", "MODE", "HIT_ID"}, hitRows)
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

	// Cursor discipline mirrors the live control-api's keyset pagination
	// (foundation/persistence/{sqlite,postgres}/events.go): the event log
	// is read newest-first ((occurred_at, id) DESC) and NextCursor is an
	// OPAQUE base64 keyset token that walks backward through history. The
	// CLI must pass that exact token back — fabricating a numeric cursor
	// (the old fmt.Sprintf("%d", lastSeenID)) makes the server 500 with
	// "events.list: bad cursor" on the first advance (issue #1). NextCursor
	// is set only on a full page; a partial page returns "" to signal the
	// backlog is drained.
	//
	// lastSeenID is a purely-local dedup high-watermark and is NEVER sent
	// as a cursor. Because pages arrive newest-first, the skip test uses a
	// per-poll snapshot (prevSeen) rather than the running max: the first
	// event of a full backlog is the global newest, so updating the
	// watermark mid-drain would suppress every older (lower-ID) event on
	// the following pages. We compare each event against the watermark as
	// it stood at the start of the poll and only advance the committed
	// watermark after the whole backlog is drained.
	var lastSeenID int64
	for {
		prevSeen := lastSeenID
		nextCursor := "" // opaque server token; empty re-scans the newest page
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
				if common.Format == FormatJSON {
					_ = EmitJSON(os.Stdout, e)
				} else {
					fmt.Fprintf(os.Stdout, "%s\t%d\t%s\n", e.OccurredAt, e.ID, e.Kind)
				}
			}
			if page.NextCursor == "" {
				break // partial page: backlog drained for this poll
			}
			// Full page: continue draining older events via the opaque
			// token, without sleeping.
			nextCursor = page.NextCursor
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
