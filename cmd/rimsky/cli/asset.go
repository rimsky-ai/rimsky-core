// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: asset
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

func RunAssetList(ctx context.Context, args []string) int {
	var instance string
	_, common, endpoint, code := runWithCommon("asset list", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
	})
	if code != 0 {
		return code
	}
	if instance == "" {
		fmt.Fprintln(os.Stderr, "usage: rimsky asset list --instance <id-or-key>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	id, err := resolveInstanceUUID(ctx, c, instance)
	if err != nil {
		return reportError(err)
	}
	resp, err := c.ListAssets(ctx, id)
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, resp.Assets)
		return 0
	}
	rows := make([][]string, 0, len(resp.Assets))
	for _, a := range resp.Assets {
		rows = append(rows, []string{
			a.Alias, a.NodeType, a.ProducerName, a.VersionID,
			a.ClaimedAt.UTC().Format(time.RFC3339),
		})
	}
	EmitTable(os.Stdout, []string{"ALIAS", "NODE_TYPE", "PRODUCER", "VERSION", "CLAIMED_AT"}, rows)
	return 0
}

func resolveInstanceUUID(ctx context.Context, c *Client, ref string) (string, error) {
	if LooksLikeUUID(ref) {
		return ref, nil
	}
	inst, err := c.GetInstance(ctx, ref)
	if err != nil {
		return "", err
	}
	return inst.UUID(), nil
}

func RunAssetShow(ctx context.Context, args []string) int {
	var instance string
	fs, common, endpoint, code := runWithCommon("asset show", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if instance == "" || len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky asset show --instance <id-or-key> <node_type>.<claim_alias>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	id, err := resolveInstanceUUID(ctx, c, instance)
	if err != nil {
		return reportError(err)
	}
	a, err := c.GetAsset(ctx, id, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, a)
		return 0
	}
	pairs := [][2]string{
		{"alias", a.Alias},
		{"claim_id", a.ClaimID},
		{"producer_name", a.ProducerName},
		{"node_type", a.NodeType},
		{"version_id", a.VersionID},
		{"claimed_at", a.ClaimedAt.UTC().Format(time.RFC3339)},
	}
	EmitKV(os.Stdout, pairs)
	return 0
}

func RunAssetVersions(ctx context.Context, args []string) int {
	var instance string
	fs, common, endpoint, code := runWithCommon("asset versions", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if instance == "" || len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky asset versions --instance <id-or-key> <alias>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	id, err := resolveInstanceUUID(ctx, c, instance)
	if err != nil {
		return reportError(err)
	}
	v, err := c.GetAssetVersions(ctx, id, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, v)
		return 0
	}
	if v.Error != "" {
		fmt.Fprintf(os.Stderr, "%s\n", v.Error)
		return 1
	}
	rows := make([][]string, 0, len(v.Versions))
	for _, vv := range v.Versions {
		versionID, _ := vv["version_id"].(string)
		ts, _ := vv["committed_at"].(string)
		rows = append(rows, []string{versionID, ts})
	}
	EmitTable(os.Stdout, []string{"VERSION_ID", "COMMITTED_AT"}, rows)
	return 0
}

func RunAssetDelete(ctx context.Context, args []string) int {
	var instance string
	fs, common, endpoint, code := runWithCommon("asset delete", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if instance == "" || len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky asset delete --instance <id-or-key> <alias>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	id, err := resolveInstanceUUID(ctx, c, instance)
	if err != nil {
		return reportError(err)
	}
	if err := c.DeleteAsset(ctx, id, rest[0]); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "deleted asset %s on instance %s\n", rest[0], id)
	return 0
}

func RunAssetLineage(ctx context.Context, args []string) int {
	var instance, version string
	var depth int
	fs, common, endpoint, code := runWithCommon("asset lineage", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
		fs.StringVar(&version, "version", "", "filter to one version_id (client-side)")
		fs.IntVar(&depth, "depth", 3, "walk depth (default 3, max 50)")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if instance == "" || len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky asset lineage --instance <id-or-key> <alias> [--version v] [--depth N]")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	id, err := resolveInstanceUUID(ctx, c, instance)
	if err != nil {
		return reportError(err)
	}
	a, err := c.GetAsset(ctx, id, rest[0])
	if err != nil {
		return reportError(err)
	}
	resp, err := c.GetClaimAncestors(ctx, a.ClaimID, depth)
	if err != nil {
		return reportError(err)
	}
	rows := resp.Ancestors
	if version != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if recordCarriesVersion(r.Record, version) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, rows)
		return 0
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{
			r.ID, r.RecordKind,
			r.ObservedAt.UTC().Format(time.RFC3339),
			truncateSnippet(string(r.Record), 60),
		})
	}
	EmitTable(os.Stdout, []string{"ID", "KIND", "OBSERVED_AT", "RECORD"}, table)
	return 0
}

func recordCarriesVersion(record json.RawMessage, want string) bool {
	if len(record) == 0 || want == "" {
		return false
	}
	var probe struct {
		VersionID string `json:"version_id"`
	}
	if err := json.Unmarshal(record, &probe); err != nil {
		return false
	}
	return probe.VersionID == want
}

func truncateSnippet(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
