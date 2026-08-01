// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"fmt"
	"os"
)

func RunNodeGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("node get", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky node get <id>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	n, err := c.GetNode(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, n)
		return 0
	}
	pairs := [][2]string{
		{"id", n.ID},
		{"instance_id", n.InstanceID},
		{"node_type", n.NodeType},
		{"executor", n.Executor},
	}
	if s := n.RunSummary; s != nil {
		pairs = append(pairs,
			[2]string{"runs_active", fmt.Sprintf("%d", s.ActiveCount)},
			[2]string{"runs_pending", fmt.Sprintf("%d", s.PendingCount)},
			[2]string{"runs_fresh", fmt.Sprintf("%d", s.FreshCount)},
			[2]string{"runs_failed", fmt.Sprintf("%d", s.FailedCount)},
		)
	}
	EmitKV(os.Stdout, pairs)
	return 0
}
