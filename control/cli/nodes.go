// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// nodes.go — `node get`.
package cli

import (
	"context"
	"fmt"
	"os"
)

// RunNodeGet implements `node get`.
func RunNodeGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("node get", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli node get <id>")
		return 2
	}
	c := NewClient(endpoint)
	n, err := c.GetNode(ctx, rest[0])
	if err != nil {
		return reportError(err)
	}
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, n)
		return 0
	}
	EmitKV(os.Stdout, [][2]string{
		{"id", n.ID},
		{"instance_id", n.InstanceID},
		{"node_type", n.NodeType},
		{"state", n.State},
		{"executor", n.Executor},
		{"retry_counter", fmt.Sprintf("%d", n.RetryCounter)},
	})
	return 0
}
