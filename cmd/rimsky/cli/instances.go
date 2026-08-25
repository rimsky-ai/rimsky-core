// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// @concept: anonymous-mode
func ResolveTargetDaemon(explicit, apiKey string) string {
	if explicit != "" {
		return explicit
	}
	if apiKey != "" {
		return ""
	}
	path, err := hostdaemon.IdentityFilePath()
	if err != nil {
		return ""
	}
	id, err := hostdaemon.EnsureIdentity(path)
	if err != nil {
		return ""
	}
	return id.RoutingIdentity
}

func LooksLikeUUID(s string) bool { return uuidPattern.MatchString(s) }

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

func RunInstanceCreate(ctx context.Context, args []string) int {
	return runInstanceCreateNamed(ctx, "instance create", args)
}

func runInstanceCreateNamed(ctx context.Context, name string, args []string) int {
	var params, instanceKey, daemon string
	fs, common, endpoint, code := runWithCommon(name, "<template-ref> [--params ...] [--instance-key ...] [--daemon <silly-name>]", NoTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&params, "params", "", "JSON object or @file path")
		fs.StringVar(&instanceKey, "instance-key", "", "instance_key for the new row")
		fs.StringVar(&daemon, "daemon", "", "anonymous target host-daemon's routing label (silly-name); overrides the local identity file")
	})
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
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
	apiKey := common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY"))
	body.TargetDaemon = ResolveTargetDaemon(daemon, apiKey)
	c := NewClient(endpoint)
	c.SetAPIKey(apiKey)
	inst, err := c.CreateInstance(ctx, body)
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, inst, func() {
		pairs := [][2]string{
			{"instance_id", inst.UUID()},
			{"template_hash", inst.TemplateHash},
		}
		if inst.InstanceKey != nil {
			pairs = append(pairs, [2]string{"instance_key", *inst.InstanceKey})
		}
		pairs = append(pairs, [2]string{"node_count", fmt.Sprintf("%d", inst.NodeCount)})
		EmitKV(os.Stdout, pairs)
	})
}

func RunInstanceList(ctx context.Context, args []string) int {
	return runInstanceListNamed(ctx, "instance list", args)
}

func runInstanceListNamed(ctx context.Context, name string, args []string) int {
	var template, keyPrefix string
	_, common, endpoint, code := runWithCommon(name, "[--template <hash>] [--key-prefix <prefix>]", HasTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&template, "template", "", "filter by template hash")
		fs.StringVar(&keyPrefix, "key-prefix", "", "client-side filter on instance_key prefix")
	})
	if common == nil {
		return code
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	all, err := PagedListInstances(ctx, c, ListInstancesQuery{TemplateHash: template})
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
	return Render(common.Format, all, func() {
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
	})
}

func PagedListInstances(ctx context.Context, c *Client, q ListInstancesQuery) ([]Instance, error) {
	return PageAll(func(cursor string) ([]Instance, string, error) {
		q.Cursor = cursor
		page, err := c.ListInstances(ctx, q)
		if err != nil {
			return nil, "", err
		}
		return page.Instances, page.NextCursor, nil
	})
}

func RunInstanceGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("instance get", "<id-or-key>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	inst, err := c.GetInstance(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, inst, func() {
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
	})
}

func RunInstanceDelete(ctx context.Context, args []string) int {
	return runInstanceDeleteNamed(ctx, "instance delete", args)
}

func runInstanceDeleteNamed(ctx context.Context, name string, args []string) int {
	fs, common, endpoint, code := runWithCommon(name, "<id-or-key>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	if !ConfirmDestructiveTargets(common.Yes, "delete instance "+rest[0]) {
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if err := c.DeleteInstance(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	return reportRemoval(common.Format, removalResult{Ref: rest[0], Removed: true},
		fmt.Sprintf("%s deleted", rest[0]))
}

func RunInstanceKill(ctx context.Context, args []string) int {
	var reason string
	var force bool
	fs, common, endpoint, code := runWithCommon("instance kill", "<id-or-key> [--reason ...] --force", NoTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "reason recorded on the teardown audit event")
		fs.BoolVar(&force, "force", false, "confirm forced termination")
	})
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	if !force {
		fmt.Fprintln(os.Stderr, "refusing to terminate without --force")
		return 2
	}
	if !ConfirmDestructiveTargets(common.Yes, "terminate instance "+rest[0]) {
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	inst, err := c.TerminateInstance(ctx, rest[0], reason)
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, inst, func() {
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
	})
}

const statusReportEventLimit = 50

type InstanceStatus struct {
	Instance       *Instance        `json:"instance"`
	Nodes          []Node           `json:"nodes"`
	RecentEvents   []Event          `json:"recent_events"`
	BreakpointHits []map[string]any `json:"breakpoint_hits"`
}

func gatherInstanceStatus(ctx context.Context, c *Client, inst *Instance) (*InstanceStatus, error) {
	uuid := inst.UUID()
	nodes, err := PagedListInstanceNodes(ctx, c, uuid, ListNodesQuery{})
	if err != nil {
		return nil, err
	}
	eventsResp, err := c.ListEvents(ctx, ListEventsQuery{InstanceID: uuid, Limit: statusReportEventLimit})
	if err != nil {
		return nil, err
	}
	hitsResp, err := c.ListBreakpointHits(ctx, uuid, statusReportEventLimit, "")
	if err != nil {
		return nil, err
	}
	return &InstanceStatus{
		Instance:       inst,
		Nodes:          nodes,
		RecentEvents:   eventsResp.Events,
		BreakpointHits: hitsResp.Hits,
	}, nil
}

func RunInstanceStatus(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("instance status", "<id-or-key>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))

	inst, err := c.GetInstance(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}

	status, err := gatherInstanceStatus(ctx, c, inst)
	if err != nil {
		return reportError(err)
	}

	return Render(common.Format, status, func() {
		printInstanceStatus(status)
	})
}

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
		nodeRows = append(nodeRows, []string{
			n.ID, n.NodeType,
			formatNodeRunCounts(n.RunSummary),
		})
	}
	EmitTable(os.Stdout, []string{"ID", "TYPE", "RUNS"}, nodeRows)

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

// @concept: node
func RunInstanceNodes(ctx context.Context, args []string) int {
	var tag, tagPrefix string
	fs, common, endpoint, code := runWithCommon("instance nodes", "<id-or-key> [--tag <tag>] [--tag-prefix <prefix>]",
		HasTable, args, func(fs *flag.FlagSet) {
			fs.StringVar(&tag, "tag", "", "only nodes carrying this tag")
			fs.StringVar(&tagPrefix, "tag-prefix", "", "client-side filter: only nodes carrying a tag with this prefix")
		})
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	nodes, err := PagedListInstanceNodes(ctx, c, rest[0], ListNodesQuery{Tag: tag})
	if err != nil {
		return reportError(err)
	}
	nodes = filterNodesByTagPrefix(nodes, tagPrefix)
	return Render(common.Format, nodes, func() {
		rows := make([][]string, 0, len(nodes))
		for _, n := range nodes {
			rows = append(rows, []string{
				n.ID, n.NodeType, formatNodeRunCounts(n.RunSummary), n.Executor, strings.Join(n.Tags, ","),
			})
		}
		EmitTable(os.Stdout, []string{"ID", "TYPE", "RUNS", "EXECUTOR", "TAGS"}, rows)
	})
}

// @concept: node
func filterNodesByTagPrefix(nodes []Node, prefix string) []Node {
	if prefix == "" {
		return nodes
	}
	kept := nodes[:0]
	for _, n := range nodes {
		for _, t := range n.Tags {
			if strings.HasPrefix(t, prefix) {
				kept = append(kept, n)
				break
			}
		}
	}
	return kept
}

// @concept: node
func formatNodeRunCounts(s *NodeRunSummary) string {
	if s == nil {
		return "active=0 pending=0 fresh=0 failed=0"
	}
	return fmt.Sprintf("active=%d pending=%d fresh=%d failed=%d",
		s.ActiveCount, s.PendingCount, s.FreshCount, s.FailedCount)
}

func RunInstanceEvents(ctx context.Context, args []string) int {
	return runInstanceEventsNamed(ctx, "instance events", args)
}

func runInstanceEventsNamed(ctx context.Context, name string, args []string) int {
	var follow bool
	var pollInterval time.Duration
	var since, until string
	fs, common, endpoint, code := runWithCommon(name, "<id-or-key> [--follow] [--since <RFC3339>] [--until <RFC3339>]", NoTable, args, func(fs *flag.FlagSet) {
		fs.BoolVar(&follow, "follow", false, "stream new events")
		// @decision: short-flags-single-letter
		fs.BoolVar(&follow, "f", false, "short for --follow")
		fs.DurationVar(&pollInterval, "poll-interval", time.Second, "polling interval when --follow")
		fs.StringVar(&since, "since", "", "only events at or after this RFC3339 timestamp")
		fs.StringVar(&until, "until", "", "only events at or before this RFC3339 timestamp")
	})
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
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

	// @decision: graceful-shutdown
	signalCtx, stopSignals := serverkit.ShutdownContext(ctx, slog.Default())
	defer stopSignals()

	var lastSeenID int64
	for {
		prevSeen := lastSeenID
		nextCursor := ""
		for {
			page, err := c.ListEvents(signalCtx, ListEventsQuery{InstanceID: id, Since: since, Until: until, Cursor: nextCursor, Limit: 100})
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
				if common.Format.Structured() {
					_ = EmitStructured(os.Stdout, common.Format, e)
				} else {
					fmt.Fprintf(os.Stdout, "%s\t%d\t%s\n", e.OccurredAt, e.ID, e.Kind)
				}
			}
			if page.NextCursor == "" || page.NextCursor == nextCursor || len(page.Events) == 0 {
				break
			}
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
