// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// asset.go — `rimsky asset {list,show,materialize,versions,
// delete,lineage}` (plan G1). Thin wrapper over F5 + F6 control-api
// routes.
//
//	@concept: asset
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

// RunAssetList implements `asset list --instance <id>`.
func RunAssetList(ctx context.Context, args []string) int {
	var instance string
	fs, common, endpoint, code := runWithCommon("asset list", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
	})
	if code != 0 {
		return code
	}
	_ = fs
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

// resolveInstanceUUID accepts either a UUID or an instance_key. When
// the input is not the UUID shape we GET /instances/{key} to resolve.
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

// RunAssetShow implements `asset show --instance <id> <alias>`.
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

// RunAssetMaterialize implements
// `asset materialize --instance <id> <alias> [--reason ...]
// [--payload @file|inline]`.
func RunAssetMaterialize(ctx context.Context, args []string) int {
	var instance, reason, payloadRaw string
	fs, common, endpoint, code := runWithCommon("asset materialize", args, func(fs *flag.FlagSet) {
		fs.StringVar(&instance, "instance", "", "instance UUID or instance_key (required)")
		fs.StringVar(&reason, "reason", "", "operator-visible reason")
		fs.StringVar(&payloadRaw, "payload", "", "JSON object or @file path")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if instance == "" || len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky asset materialize --instance <id-or-key> <alias> [--reason ...] [--payload ...]")
		return 2
	}
	body := MaterializeAssetRequest{Reason: reason}
	if payloadRaw != "" {
		pp, err := parseParams(payloadRaw)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		raw, _ := json.Marshal(pp)
		body.Payload = raw
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	id, err := resolveInstanceUUID(ctx, c, instance)
	if err != nil {
		return reportError(err)
	}
	out, err := c.MaterializeAsset(ctx, id, rest[0], body)
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, out)
		return 0
	}
	if mid, ok := out["message_id"].(string); ok {
		fmt.Fprintf(os.Stdout, "materialize message_id=%s\n", mid)
	} else {
		fmt.Fprintln(os.Stdout, "materialize accepted")
	}
	return 0
}

// RunAssetVersions implements `asset versions --instance <id> <alias>`.
// Wraps GET /instances/{id}/assets/{alias}/versions. V1 server returns
// 501; the CLI surfaces the precise body error so operators know the
// follow-up status.
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
		id, _ := vv["version_id"].(string)
		ts, _ := vv["committed_at"].(string)
		rows = append(rows, []string{id, ts})
	}
	EmitTable(os.Stdout, []string{"VERSION_ID", "COMMITTED_AT"}, rows)
	return 0
}

// RunAssetDelete implements `asset delete --instance <id> <alias>`.
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

// RunAssetLineage implements
// `asset lineage --instance <id> <alias> [--version v] [--depth N]`.
// Resolves the alias to a claim_handle_id via GET /assets/{alias}
// then walks GET /lineage/claims/{claim_handle_id}/ancestors.
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

// recordCarriesVersion reports whether the JSON record's `version_id`
// field equals `want`. Used to client-side-filter ancestors by version
// without pushing predicates into the URL.
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

// truncateSnippet is a small printable abbreviator for raw-JSON record
// previews in the table format.
func truncateSnippet(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
