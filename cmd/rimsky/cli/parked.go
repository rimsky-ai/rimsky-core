// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: parked-state
package cli

import (
	"context"
	"flag"
	"os"
	"time"
)

func RunParkedList(ctx context.Context, args []string) int {
	var olderThan time.Duration
	var instance string
	_, common, endpoint, code := runWithCommon("parked list", "[--older-than <duration>] [--instance <id>]", HasTable, args, func(fs *flag.FlagSet) {
		fs.DurationVar(&olderThan, "older-than", 0, "filter to rows parked longer ago than this duration")
		fs.StringVar(&instance, "instance", "", "filter to a specific instance id")
	})
	if common == nil {
		return code
	}

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

	return Render(common.Format, rows, func() {
		table := make([][]string, 0, len(rows))
		for _, r := range rows {
			resumeAt := ""
			if r.ResumeAt != nil {
				resumeAt = r.ResumeAt.UTC().Format(time.RFC3339)
			}
			table = append(table, []string{
				r.InstanceID, r.NodeID,
				r.ParkedAt.UTC().Format(time.RFC3339),
				resumeAt,
			})
		}
		EmitTable(os.Stdout, []string{"INSTANCE", "NODE_ID", "PARKED_AT", "RESUME_AT"}, table)
	})
}
