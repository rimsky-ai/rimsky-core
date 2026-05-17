// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// admin.go — `admin invalidate/reset`. The `admin force-fire`
// subcommand retired with the 2026-05-15 data-platform-extensions plan
// B10 / D7 / E16 schedule-retirement cascade; cron firing is owned by
// the bundled `sensors/sensor-cron/` service.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// RunAdminInvalidate implements `admin invalidate`.
func RunAdminInvalidate(ctx context.Context, args []string) int {
	var reason, frame string
	fs, _, endpoint, code := runWithCommon("admin invalidate", args, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "human-readable reason for the audit log")
		fs.StringVar(&frame, "frame", "", "per-emit frame discipline (\"in\" or \"next\"; default \"next\")")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli admin invalidate <node-id> [--reason ...] [--frame in|next]")
		return 2
	}
	switch frame {
	case "", "in", "next":
	default:
		fmt.Fprintln(os.Stderr, "rimsky-cli admin invalidate: --frame must be \"in\" or \"next\"")
		return 2
	}
	c := NewClient(endpoint)
	if err := c.InvalidateNode(ctx, rest[0], InvalidateNodeRequest{Reason: reason, Frame: frame}); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "invalidated %s\n", rest[0])
	return 0
}

// RunAdminReset implements `admin reset`.
func RunAdminReset(ctx context.Context, args []string) int {
	fs, _, endpoint, code := runWithCommon("admin reset", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli admin reset <node-id>")
		return 2
	}
	c := NewClient(endpoint)
	if err := c.ResetNode(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "reset %s\n", rest[0])
	return 0
}
