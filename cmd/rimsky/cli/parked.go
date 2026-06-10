// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// parked.go — `rimsky parked list`. Surfaces the spec-named
// `/diagnostics/parked` endpoint (F7) as a table. The admin alias
// path `/admin/diagnostics/parked-nodes` still resolves the same
// handler server-side for backwards compatibility.
//
//	@concept: parked-state
package cli

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"
)

// RunParkedList implements `parked list`.
//
// Usage:
//
//	rimsky parked list [--reason=<kind>] [--older-than=<dur>] [--instance=<uuid>]
func RunParkedList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("parked list", flag.ContinueOnError)
	var common CommonFlags
	RegisterCommonFlags(fs, &common)
	reason := fs.String("reason", "", "filter by parked reason (snake_case ParkReason; e.g. awaiting_human)")
	olderThan := fs.Duration("older-than", 0, "filter to rows parked longer ago than this duration")
	instance := fs.String("instance", "", "filter to a specific instance id")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	if err := common.ResolveFormat(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	SetActiveCommonFlags(&common)

	cfgPath, _ := DefaultConfigPath()
	endpoint, err := ResolveEndpoint(common.Endpoint, os.Getenv("RIMSKY_CONTROL_API"), cfgPath, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	q := url.Values{}
	if *reason != "" {
		q.Set("reason", *reason)
	}
	// G5 forwards to F7's spec-named path; the admin-named path stays
	// available server-side as a backwards-compat alias.
	path := "/v1/diagnostics/parked"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	client := NewClient(endpoint)
	client.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	resp, err := client.GetParkedNodes(ctx, path)
	if err != nil {
		return reportError(err)
	}

	rows := resp.ParkedNodes
	if *olderThan > 0 {
		cutoff := time.Now().Add(-*olderThan)
		filtered := rows[:0]
		for _, r := range rows {
			if r.ParkedAt.Before(cutoff) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if *instance != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.InstanceID == *instance {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, rows)
		return 0
	}
	fmt.Println("instance\tnode_id\tparked_at\tresume_at\treason\treason_note")
	for _, r := range rows {
		resumeAt := ""
		if r.ResumeAt != nil {
			resumeAt = r.ResumeAt.UTC().Format(time.RFC3339)
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n",
			r.InstanceID, r.NodeID,
			r.ParkedAt.UTC().Format(time.RFC3339),
			resumeAt, r.Reason, r.ReasonNote)
	}
	return 0
}
