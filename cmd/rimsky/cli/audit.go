// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: event-log
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// @story: audit-log-read
func RunAudit(ctx context.Context, args []string) int {
	var q ListAuditQuery
	fs, common, endpoint, code := runWithCommon("audit",
		"[--since <RFC3339>] [--until <RFC3339>] [filters...]", HasTable, args, func(fs *flag.FlagSet) {
			fs.StringVar(&q.Since, "since", "", "only rows at or after this RFC3339 timestamp")
			fs.StringVar(&q.Until, "until", "", "only rows at or before this RFC3339 timestamp")
			fs.StringVar(&q.Kind, "kind", "", "only rows of this auth event kind")
			fs.StringVar(&q.KeyID, "key-id", "", "only rows for this api-key id")
			fs.StringVar(&q.KeyName, "key-name", "", "only rows for this api-key name")
			fs.StringVar(&q.Action, "action", "", "only rows for this exact action; name this or --action-prefix, not both")
			fs.StringVar(&q.ActionPrefix, "action-prefix", "", "only rows whose action carries this prefix; name this or --action, not both")
			fs.StringVar(&q.Target, "target", "", "only rows for this request path")
			fs.StringVar(&q.Mode, "mode", "", "only rows recorded in this request mode")
			fs.IntVar(&q.Status, "status", 0, "only rows carrying this response status")
		})
	if common == nil {
		return code
	}
	if len(fs.Args()) != 0 {
		return UsageError(fs)
	}
	if q.Action != "" && q.ActionPrefix != "" {
		fmt.Fprintln(os.Stderr,
			"rimsky audit: --action and --action-prefix select the same field; the route honors --action and drops --action-prefix. Name one.")
		return UsageError(fs)
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	rows, more, err := readAudit(ctx, c, q)
	if err != nil {
		return reportError(err)
	}
	if more {
		fmt.Fprintf(os.Stderr,
			"rimsky audit: showing the most recent %d rows; name --since/--until to read a whole window\n", auditPageSize)
	}
	return Render(common.Format, rows, func() {
		table := make([][]string, 0, len(rows))
		for _, e := range rows {
			table = append(table, []string{
				e.OccurredAt, fmt.Sprintf("%d", e.ID), e.Kind,
				auditPayloadField(e, "key_name"),
				auditPayloadField(e, "action"),
				auditPayloadField(e, "response_status"),
			})
		}
		EmitTable(os.Stdout, []string{"OCCURRED_AT", "ID", "KIND", "KEY", "ACTION", "STATUS"}, table)
	})
}

const auditPageSize = 100

func readAudit(ctx context.Context, c *Client, q ListAuditQuery) ([]Event, bool, error) {
	q.Limit = auditPageSize
	if q.Since == "" && q.Until == "" {
		page, err := c.ListAudit(ctx, q)
		if err != nil {
			return nil, false, err
		}
		return page.Audit, page.NextCursor != "", nil
	}
	rows, err := PageAll(func(cursor string) ([]Event, string, error) {
		q.Cursor = cursor
		page, err := c.ListAudit(ctx, q)
		if err != nil {
			return nil, "", err
		}
		return page.Audit, page.NextCursor, nil
	})
	return rows, false, err
}

func auditPayloadField(e Event, key string) string {
	v, ok := e.Payload[key]
	if !ok || v == nil {
		return ""
	}
	if f, isFloat := v.(float64); isFloat {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%v", v)
}
