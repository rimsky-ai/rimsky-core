// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"fmt"
	"os"
)

func RunNodeGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("node get", "<id>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	n, err := c.GetNode(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	return Render(common.Format, n, func() {
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
	})
}
