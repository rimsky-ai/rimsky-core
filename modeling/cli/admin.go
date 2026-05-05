// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// admin.go — `admin force-fire/invalidate/reset`.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// RunAdminForceFire implements `admin force-fire`.
func RunAdminForceFire(ctx context.Context, args []string) int {
	fs, _, endpoint, code := runWithCommon("admin force-fire", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli admin force-fire <node-id>")
		return 2
	}
	c := NewClient(endpoint)
	if err := c.AdminForceFire(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "force-fire dispatched for %s\n", rest[0])
	return 0
}

// RunAdminInvalidate implements `admin invalidate`.
func RunAdminInvalidate(ctx context.Context, args []string) int {
	var reason string
	fs, _, endpoint, code := runWithCommon("admin invalidate", args, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "human-readable reason for the audit log")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli admin invalidate <node-id> [--reason ...]")
		return 2
	}
	c := NewClient(endpoint)
	if err := c.InvalidateNode(ctx, rest[0], InvalidateNodeRequest{Reason: reason}); err != nil {
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
