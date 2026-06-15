// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// admin.go — `admin reset`. The `admin invalidate` subcommand retired
// with the 2026-06-14 message-schema-layer reshape (operators who want
// to invalidate post a typed message via `POST /instances/{id}/messages`
// with a template-declared `messages:` type). The `admin force-fire`
// subcommand retired with the 2026-05-15 data-platform-extensions plan
// B10 / D7 / E16 schedule-retirement cascade; cron firing is owned by
// a cron sensor, which is not part of this repo.
package cli

import (
	"context"
	"fmt"
	"os"
)

// RunAdminReset implements `admin reset`.
func RunAdminReset(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("admin reset", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky admin reset <node-id>")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if err := c.ResetNode(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "reset %s\n", rest[0])
	return 0
}
