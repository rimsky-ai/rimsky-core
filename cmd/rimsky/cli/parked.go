// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: parked-state
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

func RunParkedList(ctx context.Context, args []string) int {
	var olderThan time.Duration
	var instance string
	fs, common, endpoint, code := runWithCommon("parked list", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&olderThan, "older-than", 0, "filter to rows parked longer ago than this duration")
		fs.StringVar(&instance, "instance", "", "filter to a specific instance id")
	})
	if code != 0 {
		return code
	}
	_ = fs

	path := "/v1/admin/diagnostics/parked-nodes"

	client := NewClient(endpoint)
	client.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	resp, err := client.GetParkedNodes(ctx, path)
	if err != nil {
		return reportError(err)
	}

	rows := resp.ParkedNodes
	if olderThan > 0 {
		cutoff := time.Now().Add(-olderThan)
		filtered := rows[:0]
		for _, r := range rows {
			if r.ParkedAt.Before(cutoff) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if instance != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.InstanceID == instance {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, rows)
		return 0
	}
	fmt.Fprintln(os.Stdout, "instance\tnode_id\tparked_at\tresume_at")
	for _, r := range rows {
		resumeAt := ""
		if r.ResumeAt != nil {
			resumeAt = r.ResumeAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\n",
			r.InstanceID, r.NodeID,
			r.ParkedAt.UTC().Format(time.RFC3339),
			resumeAt)
	}
	return 0
}
